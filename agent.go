package main

import (
	"wada-desktop-go/internal/opencode"

	"context"

	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// The app holds only what the transcript draws. The agent connection lives
// inside connect's goroutine, which publishes every state change through
// Window.Update so all writes happen on the UI thread.

// agentJobKind names what one queued job asks the runner to do.
type agentJobKind string

const (
	jobPrompt  agentJobKind = "prompt"  // send the user's words
	jobOpen    agentJobKind = "open"    // open a conversation's stored transcript
	jobStop    agentJobKind = "stop"    // interrupt the running turn
	jobAnswer  agentJobKind = "answer"  // reply to an approval
	jobRename  agentJobKind = "rename"  // retitle a conversation
	jobDelete  agentJobKind = "delete"  // delete a conversation
	jobModel   agentJobKind = "model"   // switch the conversation's model
	jobMore    agentJobKind = "more"    // load the next page of the list
	jobRefresh agentJobKind = "refresh" // read the newest page again
	jobUsage   agentJobKind = "usage"   // read one stored turn's spending
	jobServer  agentJobKind = "server"  // dial the server the settings chose
)

// agentJob is one request for the runner: a prompt to send, a conversation
// to open, a brand-new chat to create, a running turn to stop, an
// approval to answer, a list change to make or the server to dial.
type agentJob struct {
	kind     agentJobKind // what to do; one of the job* constants
	route    uint64       // the connection generation this request belongs to
	sid      string       // the session to act on; empty for a new one
	dir      string       // where a new chat should work; empty for the app's
	text     string       // the user's words or a new title, for jobPrompt and jobRename
	cursor   string       // the next page of conversations, for jobMore
	provider string       // the model's provider, for jobModel and jobPrompt
	model    string       // the model id, for jobModel and jobPrompt
	variant  string       // its reasoning effort, when it has one
	msg      string       // the stored turn whose spending to read, for jobUsage
	request  string       // the permission request being answered
	decision string       // "once", "always" or "reject"
	draft    uint64       // identifies a blank draft until its server session arrives
	server   serverConn   // the connection to dial, for jobServer
	pins     []string     // the chats pinned on that server, to load past its first page
}

// connect runs the pipe for one app lifetime: dial the server the settings
// chose, subscribe to its events and then take jobs and stream events in,
// one task at a time between frames. The server going down ends one
// attempt, not the session: the pipe dials again with a stretch of waiting
// between tries, and a server switch jumps the line.
func (a *app) connect(win *mygo.Window, jobs <-chan agentJob, cfg serverConn, pinnedIDs []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The server dies with the window, even while startAgentFor is still
	// bringing it up.
	win.OnClosed(func() { cancel() })
	go watchPolicy(ctx, a.policyDirs, a.policyWrites, func(result policyResult) {
		win.Update(func() {
			if ctx.Err() == nil {
				a.acceptPolicy(result)
			}
		})
	})

	pins := pinnedIDs
	var route uint64
	var last *opencode.Client
	booted := false // the first refill of a server settles onto its newest chat
	switching := false
	waitDue := false
	reconnect := 0
	note := ""           // the last trouble line shown, so a retry does not repeat it
	var stash []agentJob // jobs that landed while nothing was connected

	var attempts atomic.Uint64

	for {
		if last != nil {
			// The seat the server sat in frees itself for the next one,
			// in case the process lingers while its stream died.
			last.Stop()
			last = nil
		}
		if switching {
			// The settings chose another server: dial it at once, and let
			// its own boot settle onto its newest chat.
			switching = false
			waitDue = false
			reconnect = 0
			note = ""
			stash = nil
			win.Update(func() { booted = false })
		}
		if waitDue {
			// Jobs queued while the pipe heals replay against the
			// respawned server; the asks of a dead one are gone. A server
			// switch queued during the wait skips the rest of it.
			waitDue = false
			win.Update(func() {
				a.finishCutTurns()
				a.streamLine("The agent went down; reconnecting…")
			})
			wait := []time.Duration{time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}
			if reconnect-1 < len(wait) {
				wait = wait[reconnect-1:]
			} else {
				wait = wait[len(wait)-1:]
			}
			timer := time.NewTimer(wait[0])
		waiting:
			for {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					break waiting
				case job := <-jobs:
					if job.kind == jobServer {
						cfg, pins, route = job.server, job.pins, job.route
						switching = true
						timer.Stop()
						break waiting
					}
					stash = append(stash, job)
				}
			}
			if switching {
				continue
			}
		}

		attempt := attempts.Add(1)
		generation := route
		post := func(fn func()) {
			win.Update(func() {
				if !a.closed && a.route == generation && attempts.Load() == attempt {
					fn()
				}
			})
		}
		say := func(_ string, text string) { post(func() { a.streamLine(text) }) }
		applyForServer := func(_ serverConn) func(opencode.Event) { return a.apply }
		current := cfg
		post(func() { a.setHealth(current.id, serverHealth{status: healthConnecting, addr: current.url}) })
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		ag, err := opencode.StartClient(attemptCtx, cfg.transport(), workdir())
		if err != nil {
			cancelAttempt()
			if ctx.Err() != nil {
				return
			}
			post(func() {
				a.setHealth(current.id, serverHealth{status: healthDown, detail: err.Error(), addr: current.url})
			})
			if text := "The agent would not come up: " + err.Error(); text != note {
				note = text
				say(cfg.id, text)
			}
			last = nil // nothing to free
			reconnect++
			waitDue = true
			continue
		}
		note = ""
		last = ag
		post(func() {
			a.setHealth(current.id, serverHealth{status: healthReady, detail: ag.Facts.Version, addr: ag.Address})
			a.setFacts(current.id, ag.Facts)
		})
		if reconnect > 0 {
			say(cfg.id, cfg.name+" is back.")
		}
		reconnect = 0
		// srv is this attempt's server: everything the attempt publishes
		// checks the view still shows it before writing.
		srv := cfg
		attemptPins := pins
		go searchChats(attemptCtx, a.searches, func(ctx context.Context, query string) ([]opencode.Session, error) {
			rows, _, err := ag.ListSessions(ctx, "", query, "")
			return rows, err
		}, post, a.acceptSearch)
		post(func() {
			if a.searchText != "" {
				a.reviseSearch()
			}
		})

		// The stream publishes bounded batches; helpers fill the sidebar
		// from the stored conversations while the loop runs.
		done := make(chan error, 1)
		go func() {
			done <- agentUpdates(attemptCtx, ag.Events, post, applyForServer(srv))
		}()
		go func() {
			ls, nxt, lerr := ag.ListSessions(attemptCtx, "", "", "")
			post(func() {
				if a.activeServer != srv.id {
					return
				}
				if !booted {
					a.settleBoot(ls, nxt, lerr)
					booted = true
					return
				}
				// A refill keeps what the user was reading: the newest
				// page lands at the head of the list, the transcript stays.
				if lerr == nil {
					a.scope().sessions = freshSessions(a.scope().sessions, ls)
					a.scope().nextCursor = nxt
				}
				a.scope().sessionsLoaded = true
			})
			if lerr == nil {
				extra, err := loadPinnedSessions(attemptCtx, attemptPins, ls, nxt,
					func(ctx context.Context, cursor, search string) ([]opencode.Session, string, error) {
						return ag.ListSessions(attemptCtx, cursor, search, "")
					})
				post(func() {
					if attemptCtx.Err() != nil || a.activeServer != srv.id {
						return
					}
					if err != nil {
						a.streamLine("Pinned chats could not all load: " + err.Error())
						return
					}
					// Ignore pins removed while the lookup was in flight.
					for _, s := range extra {
						if a.pinned[s.ID] {
							a.scope().sessions = dedupeSessions(a.scope().sessions, []opencode.Session{s})
						}
					}
				})
			}
		}()
		go func() {
			ms, merr := ag.ListModels(attemptCtx)
			def, derr := ag.DefaultModel(attemptCtx)
			post(func() {
				if a.activeServer != srv.id {
					return
				}
				if merr == nil {
					a.scope().models = ms
				} else {
					why := merr.Error()
					a.streamLine("The model list did not load: " + why)
				}
				if derr == nil {
					a.scope().serverDefault = def
				}
				a.scope().modelsLoaded = true
			})
		}()
		go func() {
			ps, perr := ag.ListProjects(attemptCtx)
			post(func() {
				if a.activeServer != srv.id || perr != nil {
					return
				}
				// The server's own list, in its own order, is what the
				// section shows; the folders a chat sits in are unioned
				// where the list is read, so nothing a session named is
				// lost by the swap.
				a.scope().projects = ps
			})
		}()

		// A draft reuses its pending server chat until the UI learns its ID.
		var sid string
		var sidDraft uint64
		// open brings a fresh conversation up under the model wanted --
		// the app's default unless the job names another -- working in
		// the directory asked for; a model ref refusing to set does not
		// stop the turn.
		open := func(dir string, draft uint64, want opencode.ModelRef) bool {
			nid, ref, nerr := ag.CreateSession(attemptCtx, dir)
			if nerr != nil {
				say(srv.id, "The agent did not open a chat: "+nerr.Error())
				return false
			}
			if want.ID == "" {
				prov, name := opencode.DefaultModel()
				want = opencode.ModelRef{ProviderID: prov, ID: name}
			}
			if perr := ag.SetModel(attemptCtx, nid, want.ProviderID, want.ID, want.Variant); perr != nil {
				say(srv.id, "The app's model was not set, running the config's: "+perr.Error())
			}
			sid = nid
			sidDraft = draft
			home := canonDir(dir)
			if home == "" {
				home = canonDir(workdir())
			}
			post(func() {
				if a.activeServer != srv.id {
					return
				}
				if a.draftVersion == draft && a.selectedSession == "" {
					a.selectedSession = nid
					a.selected = "New chat"
				}
				// The untitled row stands at the top; its title fills in
				// over the stream once words run.
				a.scope().sessions = append([]opencode.Session{{ID: nid, Directory: home, Model: ref}}, a.scope().sessions...)
			})
			return true
		}

		// handle runs one job against the connected server; it reports the
		// one job that asks for another server instead.
		handle := func(job agentJob) bool {
			if job.kind != jobServer && job.route != route {
				return false // the request belongs to a connection left behind
			}
			switch job.kind {
			case jobOpen:
				msgs, oerr := ag.SessionMessages(attemptCtx, job.sid)
				if oerr != nil {
					say(srv.id, "Could not open that conversation: "+oerr.Error())
					return false
				}
				post(func() {
					if a.activeServer != srv.id || a.selectedSession != job.sid {
						return
					}
					a.freshTranscript()
					a.transcript.Replace(restoreBlocks(msgs))
					a.selectedSession = job.sid
				})
			case jobStop:
				who := job.sid
				if who == "" {
					who = sid
				}
				if who == "" {
					return false
				}
				if serr := ag.Interrupt(attemptCtx, who); serr != nil {
					say(srv.id, "The turn refused to stop: "+serr.Error())
				}
			case jobAnswer:
				if aerr := ag.ReplyPermission(attemptCtx, job.sid, job.request, job.decision); aerr != nil {
					say(srv.id, "The approval did not go through: "+aerr.Error())
					return false
				}
				post(func() {
					if a.activeServer == srv.id {
						a.dropPerm(job.request)
					}
				})
			case jobRename:
				if rerr := ag.RenameSession(attemptCtx, job.sid, job.text); rerr != nil {
					say(srv.id, "The rename did not take: "+rerr.Error())
				}
				// The new title folds in over the session.renamed event.
			case jobDelete:
				if derr := ag.DeleteSession(attemptCtx, job.sid); derr != nil {
					say(srv.id, "The chat did not delete: "+derr.Error())
					return false
				}
				post(func() {
					if a.activeServer == srv.id {
						a.dropSession(job.sid)
					}
				})
			case jobMore:
				rows, cur, merr := ag.ListSessions(attemptCtx, job.cursor, "", "")
				post(func() {
					if a.activeServer != srv.id {
						return
					}
					if merr == nil {
						a.scope().sessions = dedupeSessions(a.scope().sessions, rows)
						a.scope().nextCursor = cur
					} else {
						a.streamLine("The older chats did not load: " + merr.Error())
					}
					a.scope().moreLoading = false
				})
			case jobUsage:
				m, uerr := ag.SessionMessage(attemptCtx, job.sid, job.msg)
				post(func() {
					if a.activeServer != srv.id || uerr != nil {
						return
					}
					a.acceptUsage(m)
				})
			case jobRefresh:
				// The server's world moved on — a turn of this app's, or
				// another client's chat: the newest page lands at the head
				// of the list, and what the user was reading stays.
				rows, cur, rerr := ag.ListSessions(attemptCtx, "", "", "")
				post(func() {
					if a.activeServer != srv.id || rerr != nil {
						return
					}
					a.scope().sessions = freshSessions(a.scope().sessions, rows)
					a.scope().nextCursor = cur
					a.scope().sessionsLoaded = true
				})
			case jobModel:
				if job.sid == "" {
					return false // a draft's pick waits in the view, not here
				}
				if merr := ag.SetModel(attemptCtx, job.sid, job.provider, job.model, job.variant); merr != nil {
					say(srv.id, "The model did not switch: "+merr.Error())
				}
				// The switch folds back over session.model.selected.
			case jobPrompt:
				target := job.sid
				if target == "" {
					want := opencode.ModelRef{ProviderID: job.provider, ID: job.model, Variant: job.variant}
					if (sid == "" || sidDraft != job.draft) && !open(job.dir, job.draft, want) {
						return false
					}
					target = sid
				}
				if perr := ag.Prompt(attemptCtx, target, job.text); perr != nil {
					say(srv.id, "The agent could not take that: "+perr.Error())
				}
			case jobServer:
				cfg, pins, route = job.server, job.pins, job.route
				switching = true
				return true
			}
			return false
		}

		alive := true
		for alive {
			job, ready := pollAgentJob(jobs, &stash)
			if !ready {
				select {
				case <-ctx.Done():
					cancelAttempt()
					return
				case <-done:
					alive = false
					continue
				case job = <-jobs:
				}
			}
			if handle(job) {
				alive = false
			}
		}
		cancelAttempt()
		if ctx.Err() != nil {
			return
		}
		if !switching {
			reconnect++
			waitDue = true
			post(func() {
				a.setHealth(srv.id, serverHealth{status: healthDown, detail: "the stream ended", addr: ag.Address})
			})
		}
	}
}
