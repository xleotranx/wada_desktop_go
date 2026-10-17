package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// agent drives a headless opencode server: the app spawns `opencode serve`,
// speaks its /api HTTP surface and streams its /api/event SSE feed into the
// transcript, so the real agent answers where the demo copy used to.
type agent struct {
	base string // like http://127.0.0.1:4096/api
	pw   string // the server password this app chose for serve
	http *http.Client
	cmd  *exec.Cmd
}

// agentEvent is one line of the opencode event stream, decoded to its
// envelope; the payload sits in Data and its shape depends on Type.
type agentEvent struct {
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

// startAgent spawns opencode serve rooted at dir, waits for its API to
// answer and hands back a client. The server dies with this process
// (Pdeathsig), so dev rebuilds don't leave orphans behind.
func startAgent(ctx context.Context, dir string) (*agent, error) {
	exe, err := exec.LookPath("opencode")
	if err != nil {
		return nil, fmt.Errorf("opencode not found in PATH: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	rand.Read(key)
	pw := base64.RawURLEncoding.EncodeToString(key)

	cmd := exec.CommandContext(ctx, exe, "serve",
		"--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+pw)
	// serve prints its random password only when none is set; ours is
	// already in cmd.Env, so the app never parses that line.
	ownChild(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	a := &agent{
		base: "http://127.0.0.1:" + strconv.Itoa(port) + "/api",
		pw:   pw,
		http: &http.Client{},
		cmd:  cmd,
	}
	if err := a.waitReady(ctx, 20*time.Second); err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	return a, nil
}

// waitReady polls /api/info until the server names its version, the
// deadline passes or the app walks away.
func (a *agent) waitReady(ctx context.Context, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw, err := a.call(ctx, "GET", "/info", nil)
		if err == nil && strings.Contains(string(raw), "version") {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("opencode serve did not come up in %s", within)
}

// freePort finds a port for serve by reserving and releasing one.
func freePort() (int, error) {
	row, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer row.Close()
	return row.Addr().(*net.TCPAddr).Port, nil
}

// call is one API round trip. opencode answers errors as tagged JSON bodies
// like {"_tag":…,"message":…}, which this turns into Go errors.
func (a *agent) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", a.pw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var tagged struct {
			Tag     string `json:"_tag"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &tagged) == nil && tagged.Message != "" {
			return nil, fmt.Errorf("%s: %s", tagged.Tag, tagged.Message)
		}
		return nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	return raw, nil
}

// createSession opens a fresh conversation for this app, working in the
// directory given: the server's own when none is named.
func (a *agent) createSession(ctx context.Context, dir string) (string, agentModelRef, error) {
	var body any = struct{}{}
	if dir != "" {
		body = map[string]any{
			"location": map[string]any{"directory": dir},
		}
	}
	raw, err := a.call(ctx, "POST", "/session", body)
	if err != nil {
		return "", agentModelRef{}, err
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", agentModelRef{}, err
	}
	return out.Data.ID, agentModelRef{providerID: out.Data.Model.ProviderID,
		id: out.Data.Model.ID, variant: out.Data.Model.Variant}, nil
}

// prompt queues one user turn; the answer streams back over events, so the
// call only needs to enqueue, not wait.
func (a *agent) prompt(ctx context.Context, sessionID, text string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/prompt",
		map[string]any{"text": text})
	return err
}

// sessionModel is the model every conversation this app opens runs on: a
// zero-cost one, so live turns never spend tokens, unless WADA_MODEL asks
// for something else ("provider/model").
func sessionModel() (string, string) {
	if env := os.Getenv("WADA_MODEL"); env != "" {
		prov, name, ok := strings.Cut(env, "/")
		if ok && prov != "" && name != "" {
			return prov, name
		}
	}
	return "opencode-go", "longcat-2.5-preview-free"
}

// pickModel names one conversation's model; without it, the server runs
// the config default, which in this app is not free.
func (a *agent) pickModel(ctx context.Context, sessionID string) error {
	prov, name := sessionModel()
	return a.setModel(ctx, sessionID, prov, name, "")
}

// setModel points one conversation at a model of the catalog, with its
// reasoning effort when it has one. The switch folds back over the
// session.model.selected event.
func (a *agent) setModel(ctx context.Context, sessionID, providerID, id, variant string) error {
	ref := map[string]any{"providerID": providerID, "id": id}
	if variant != "" {
		ref["variant"] = variant
	}
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/model",
		map[string]any{"model": ref})
	return err
}

// interrupt stops the conversation's running turn; the server answers
// {"interrupted":…} and says so again over the event stream.
func (a *agent) interrupt(ctx context.Context, sessionID string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/interrupt", struct{}{})
	return err
}

// replyPermission answers one approval the agent asked for. Once the
// server takes it, the turn carries on ("always" remembers the pattern),
// and the card leaves the transcript.
func (a *agent) replyPermission(ctx context.Context, sessionID, requestID, decision string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/permission/"+requestID+"/reply",
		map[string]any{"decision": decision})
	return err
}

// renameSession sets one conversation's title; the rename comes back as
// an event, which folds it into the sidebar row.
func (a *agent) renameSession(ctx context.Context, sessionID, title string) error {
	_, err := a.call(ctx, "PATCH", "/session/"+sessionID, map[string]any{"title": title})
	return err
}

// deleteSession drops one conversation from the store for good.
func (a *agent) deleteSession(ctx context.Context, sessionID string) error {
	_, err := a.call(ctx, "DELETE", "/session/"+sessionID, nil)
	return err
}

// agentSession is the sidebar row of one real conversation, with the
// folder it works in — the list spans every project the store knows.
type agentSession struct {
	id      string
	title   string
	dir     string
	updated int64
	model   agentModelRef // what it runs on
}

// agentModelRef is the model a conversation points at, as stored rows
// and events both name it: a provider detail of which reasoning effort.
type agentModelRef struct {
	providerID string
	id         string
	variant    string
}

// agentModel is one entry of the server's catalog, with whether running
// it costs anything — the picker marks free models.
type agentModel struct {
	providerID string
	id         string
	name       string
	variants   []string
	free       bool
}

// listModels reads the catalog the signed-in server offers.
func (a *agent) listModels(ctx context.Context) ([]agentModel, error) {
	raw, err := a.call(ctx, "GET", "/model", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Name       string `json:"name"`
			Variants   []struct {
				ID string `json:"id"`
			} `json:"variants"`
			Cost []struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
			} `json:"cost"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	all := []agentModel{}
	for _, m := range out.Data {
		row := agentModel{id: m.ID, providerID: m.ProviderID, name: m.Name, free: true}
		for _, v := range m.Variants {
			row.variants = append(row.variants, v.ID)
		}
		for _, c := range m.Cost {
			if c.Input != 0 || c.Output != 0 {
				row.free = false
			}
		}
		all = append(all, row)
	}
	return all, nil
}

// listSessions reads the conversations, newest first. The server pages
// them; cursor "" starts at the newest page and a later page picks up
// where the last one left off, with "" again when the list runs out. A
// search narrows the whole store to what the titles match, and it pages
// the same way.
func (a *agent) listSessions(ctx context.Context, cursor string, search string) ([]agentSession, string, error) {
	path := "/session?limit=50"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	if search != "" {
		path += "&search=" + url.QueryEscape(search)
	}
	raw, err := a.call(ctx, "GET", path, nil)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Data []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
			Time struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			} `json:"time"`
		} `json:"data"`
		Cursor struct {
			Next string `json:"next"`
		} `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	all := []agentSession{}
	for _, s := range out.Data {
		row := agentSession{id: s.ID, title: s.Title, dir: canonDir(s.Location.Directory), updated: s.Time.Updated,
			model: agentModelRef{providerID: s.Model.ProviderID, id: s.Model.ID, variant: s.Model.Variant}}
		if row.updated == 0 {
			row.updated = s.Time.Created
		}
		all = append(all, row)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].updated > all[j].updated })
	return all, out.Cursor.Next, nil
}

// agentMessage is a stored turn of a session: what the user said or what
// the assistant answered in parts. The user's words arrive at the top
// level; the queued turn's payload shape decodes too. Shapes the
// transcript ignores (such as the "idle" marker) decode fine and get
// skipped.
type agentMessage struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Payload struct {
		Text string `json:"text"`
	} `json:"payload"`
	Model struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
		Variant    string `json:"variant"`
	} `json:"model"`
	Content []agentPart `json:"content"`
}

// agentPart is one piece of an assistant message: text or one tool call.
type agentPart struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status   string          `json:"status"`
		Input    json.RawMessage `json:"input"`
		Content  []toolOut       `json:"content"`
		Metadata struct {
			Exit      int  `json:"exit"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	} `json:"state"`
}

// sessionMessages reads one conversation's stored turns, oldest first.
func (a *agent) sessionMessages(ctx context.Context, id string) ([]agentMessage, error) {
	raw, err := a.call(ctx, "GET", "/session/"+id+"/message?order=asc", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []agentMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// events follows the SSE feed and calls h per event until ctx ends.
func (a *agent) events(ctx context.Context, h func(agentEvent)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.base+"/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", a.pw)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: %s", resp.Status)
	}
	// One data line is a whole event envelope; tool outputs can make them
	// chunky, so size the scanner accordingly.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), "data: ")
		if line == sc.Text() { // comments like ": heartbeat" carry no data:
			continue
		}
		var ev agentEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			h(ev)
		}
	}
	return sc.Err()
}

// The app holds only what the transcript draws. The agent connection lives
// inside connect's goroutine, which publishes every state change through
// Window.Update so all writes happen on the UI thread.

// agentJobKind names what one queued job asks the runner to do.
type agentJobKind string

const (
	jobPrompt agentJobKind = "prompt" // send the user's words
	jobOpen   agentJobKind = "open"   // open a conversation's stored transcript
	jobStop   agentJobKind = "stop"   // interrupt the running turn
	jobAnswer agentJobKind = "answer" // reply to an approval
	jobRename agentJobKind = "rename" // retitle a conversation
	jobDelete agentJobKind = "delete" // delete a conversation
	jobModel  agentJobKind = "model"  // switch the conversation's model
	jobMore   agentJobKind = "more"   // load the next page of the list
)

// agentJob is one request for the runner: a prompt to send, a conversation
// to open, a brand-new chat to create, a running turn to stop, an
// approval to answer or a list change to make.
type agentJob struct {
	kind     agentJobKind // what to do; one of the job* constants
	sid      string       // the session to act on; empty for a new one
	dir      string       // where a new chat should work; empty for the app's
	text     string       // the user's words or a new title, for jobPrompt and jobRename
	cursor   string       // the next page of conversations, for jobMore
	provider string       // the model's provider, for jobModel
	model    string       // the model id, for jobModel
	variant  string       // its reasoning effort, when it has one
	request  string       // the permission request being answered
	decision string       // "once", "always" or "reject"
	draft    uint64       // identifies a blank draft until its server session arrives
}

// connect runs the pipe for one app lifetime: spawn the server, subscribe
// to its events and then take jobs and stream events in, one task at a
// time between frames. The server going down ends one attempt, not the
// session: the pipe respawns with a stretch of waiting between tries,
// for as long as the window lives.
func (a *app) connect(win *mygo.Window, jobs <-chan agentJob, pinnedIDs []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The server dies with the window, even while startAgent is still
	// bringing it up.
	win.OnClosed(func() { cancel() })
	go watchPolicy(ctx, a.policyDirs, a.policyWrites, func(result policyResult) {
		win.Update(func() {
			if ctx.Err() == nil {
				a.acceptPolicy(result)
			}
		})
	})

	var last *agent
	booted := false // the first refill settles onto the newest chat; later ones keep the view where it is
	for reconnect := 0; ; reconnect++ {
		if last != nil {
			// The seat the server sat in frees itself for the next one,
			// in case the process lingers while its stream died.
			last.cmd.Process.Kill()
		}
		if reconnect > 0 {
			// Jobs queued while the pipe heals replay against the
			// respawned server; the asks of a dead one are gone.
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
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait[0]):
			}
		}
		ag, err := startAgent(ctx, workdir())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			why := err.Error()
			win.Update(func() { a.streamLine("The agent would not come up: " + why) })
			last = nil // nothing to free
			continue
		}
		last = ag
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		go searchChats(attemptCtx, a.searches, func(ctx context.Context, query string) ([]agentSession, error) {
			rows, _, err := ag.listSessions(ctx, "", query)
			return rows, err
		}, win.Update, a.acceptSearch)
		win.Update(func() {
			if a.searchText != "" {
				a.reviseSearch()
			}
		})

		// The stream publishes bounded batches; helpers fill the sidebar
		// from the stored conversations while the loop runs.
		done := make(chan error, 1)
		go func() {
			done <- agentUpdates(attemptCtx, ag.events, win.Update, a.apply)
		}()
		go func() {
			ls, nxt, lerr := ag.listSessions(ctx, "", "")
			win.Update(func() {
				if !booted {
					a.settleBoot(ls, nxt, lerr)
					booted = true
					return
				}
				// A refill keeps what the user was reading: the list
				// lands, the transcript stays.
				if lerr == nil {
					a.sessions = dedupeSessions(a.sessions, ls)
					a.nextCursor = nxt
				}
				a.sessionsLoaded = true
			})
			if lerr == nil {
				extra, err := loadPinnedSessions(attemptCtx, pinnedIDs, ls, nxt, ag.listSessions)
				win.Update(func() {
					if attemptCtx.Err() != nil {
						return
					}
					if err != nil {
						a.streamLine("Pinned chats could not all load: " + err.Error())
						return
					}
					// Ignore pins removed while the lookup was in flight.
					for _, s := range extra {
						if a.pinned[s.id] {
							a.sessions = dedupeSessions(a.sessions, []agentSession{s})
						}
					}
				})
			}
		}()
		go func() {
			ms, merr := ag.listModels(ctx)
			win.Update(func() {
				if merr == nil {
					a.models = ms
				} else {
					why := merr.Error()
					a.streamLine("The model list did not load: " + why)
				}
				a.modelsLoaded = true
			})
		}()

		// A draft reuses its pending server chat until the UI learns its ID.
		var sid string
		var sidDraft uint64
		// pend is a model picked while nothing was open: it waits for the
		// next conversation open creates.
		var pend agentJob
		// open brings a fresh conversation up under the app's free model,
		// working in the directory asked for; a model ref refusing to set does
		// not stop the turn.
		open := func(dir string, draft uint64) bool {
			nid, ref, nerr := ag.createSession(ctx, dir)
			if nerr != nil {
				why := nerr.Error()
				win.Update(func() { a.streamLine("The agent did not open a chat: " + why) })
				return false
			}
			if perr := ag.pickModel(ctx, nid); perr != nil {
				why := perr.Error()
				win.Update(func() { a.streamLine("The app's model was not set, running the config's: " + why) })
			}
			// A model picked while nothing was open applies to this new
			// conversation instead of the default.
			if pend.kind != "" && pend.draft == draft {
				if perr := ag.setModel(ctx, nid, pend.provider, pend.model, pend.variant); perr != nil {
					why := perr.Error()
					win.Update(func() { a.streamLine("The model did not switch: " + why) })
				}
				ref = agentModelRef{providerID: pend.provider, id: pend.model, variant: pend.variant}
				pend = agentJob{}
			}
			sid = nid
			sidDraft = draft
			home := canonDir(dir)
			if home == "" {
				home = canonDir(workdir())
			}
			win.Update(func() {
				if a.draftVersion == draft && a.selectedSession == "" {
					a.selectedSession = nid
					a.selected = "New chat"
				}
				// The untitled row stands at the top; its title fills in
				// over the stream once words run.
				a.sessions = append([]agentSession{{id: nid, dir: home, model: ref}}, a.sessions...)
			})
			return true
		}
		alive := true
		for alive {
			select {
			case <-ctx.Done():
				cancelAttempt()
				return
			case <-done:
				alive = false
			case job := <-jobs:
				switch job.kind {
				case jobOpen:
					msgs, oerr := ag.sessionMessages(ctx, job.sid)
					if oerr != nil {
						why := oerr.Error()
						win.Update(func() { a.streamLine("Could not open that conversation: " + why) })
						continue
					}
					win.Update(func() {
						a.freshTranscript()
						a.blocks = restoreBlocks(msgs)
						a.selectedSession = job.sid
					})
				case jobStop:
					who := job.sid
					if who == "" {
						who = sid
					}
					if who == "" {
						continue
					}
					if serr := ag.interrupt(ctx, who); serr != nil {
						why := serr.Error()
						win.Update(func() { a.streamLine("The turn refused to stop: " + why) })
					}
				case jobAnswer:
					if aerr := ag.replyPermission(ctx, job.sid, job.request, job.decision); aerr != nil {
						why := aerr.Error()
						win.Update(func() { a.streamLine("The approval did not go through: " + why) })
						continue
					}
					win.Update(func() { a.dropPerm(job.request) })
				case jobRename:
					if rerr := ag.renameSession(ctx, job.sid, job.text); rerr != nil {
						why := rerr.Error()
						win.Update(func() { a.streamLine("The rename did not take: " + why) })
					}
					// The new title folds in over the session.renamed event.
				case jobDelete:
					if derr := ag.deleteSession(ctx, job.sid); derr != nil {
						why := derr.Error()
						win.Update(func() { a.streamLine("The chat did not delete: " + why) })
						continue
					}
					win.Update(func() { a.dropSession(job.sid) })
				case jobMore:
					rows, cur, merr := ag.listSessions(ctx, job.cursor, "")
					win.Update(func() {
						if merr == nil {
							a.sessions = dedupeSessions(a.sessions, rows)
							a.nextCursor = cur
						} else {
							why := merr.Error()
							a.streamLine("The older chats did not load: " + why)
						}
						a.moreLoading = false
					})
				case jobModel:
					who := job.sid
					if who == "" && sidDraft == job.draft {
						who = sid
					}
					if who == "" {
						pend = job
						continue
					}
					if merr := ag.setModel(ctx, who, job.provider, job.model, job.variant); merr != nil {
						why := merr.Error()
						win.Update(func() { a.streamLine("The model did not switch: " + why) })
					}
					// The switch folds back over session.model.selected.
				case jobPrompt:
					target := job.sid
					if target == "" {
						if (sid == "" || sidDraft != job.draft) && !open(job.dir, job.draft) {
							continue
						}
						target = sid
					}
					if perr := ag.prompt(ctx, target, job.text); perr != nil {
						why := perr.Error()
						win.Update(func() { a.streamLine("The agent could not take that: " + why) })
					}
				}
			}
		}
		cancelAttempt()
		// The window is gone, or the stream died with the app quitting.
		if ctx.Err() != nil {
			return
		}
	}
}
