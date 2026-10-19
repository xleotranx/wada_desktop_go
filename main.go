package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// attachState is the file-attach dialog, quoting a workspace file into
// the draft.
type attachState struct {
	request requestState

	open  bool
	draft string
	note  string
}

// renameState is the rename dialog of one conversation.
type renameState struct {
	open  bool
	sid   string
	draft string
}

// confirmState is the delete-confirmation dialog of one conversation.
type confirmState struct {
	open bool
	sid  string
	what string
}

// serverScope is one server's slice of the app state: the conversations it
// lists, the paging cursor, the models it offers. Scopes outlive switches,
// so returning to a server shows its chats while the pipe dials it again;
// the runner's own tests stand in for a server's freshness on dial. The
// transcript and the picker stay on the app, since one conversation renders.
type serverScope struct {
	pinOrder []string
	pinned   map[string]bool

	sessions       []opencode.Session
	sessionsLoaded bool
	nextCursor     string
	moreLoading    bool
	models         []opencode.Model
	modelsLoaded   bool
	// The model the server itself would run, for when the catalog offers
	// nothing this app knows; zero until a dial reads it.
	serverDefault opencode.ModelRef
	// The folders the server works in, as its project list says them;
	// empty until a dial reads it.
	projects []string
}

// app is the state the window shows.
type app struct {
	runtime      runtimeState
	connections  connectionsState
	conversation conversationState
	workspace    workspaceState
	preferences  preferencesState
	search       searchState
	layout       viewState
}

func newApp() *app {
	home := workdir()
	lifetime, stopLifetime := context.WithCancel(context.Background())
	workCtx, stopWork := context.WithCancel(lifetime)
	a := &app{runtime: runtimeState{lifetime: lifetime,
		stopLifetime: stopLifetime,
		workCtx:      workCtx,
		stopWork:     stopWork},
		connections: connectionsState{registryRequests: map[string]*requestState{},
			servers:      []serverConn{defaultLocalServer()},
			activeServer: localServerID,
			serverHealth: map[string]serverHealth{},
			serverFacts:  map[string]opencode.Facts{},
			scopes:       map[string]*serverScope{},
			modelOff:     map[string]bool{}},
		workspace: workspaceState{policyDirs: make(chan policyTarget, 1),
			policyWrites: make(chan policyChange, 16)},
		preferences: preferencesState{home: home,
			projectPins:         map[string]bool{},
			unavailableProjects: map[string]bool{}},
		search: searchState{searches: make(chan searchRequest, 1)},
		layout: viewState{sidebarW: 320,
			selectedRail:      "home",
			browserCollapsed:  true,
			projectCollapsed:  map[string]bool{},
			projectChatsShown: map[string]bool{},
			browserHistoryAt:  -1,
			expanded:          map[string]bool{},
			feed:              newFeed(),
			settingsTab:       tabServers}}
	a.runtime.pickFolder = a.pickFolderDialog
	a.runtime.probe = a.runProbe
	a.runtime.pair = a.runPair
	a.runtime.copy = a.copyToClipboard
	a.connections.pairLinks = map[string]mintedLink{}
	return a
}

// scope is the active server's slice of the app state, made on first use,
// so the view reads sessions and models straight from the reading side.
func (a *app) scope() *serverScope { return a.scopeFor(a.connections.activeServer) }
func (a *app) scopeFor(id string) *serverScope {
	s := a.connections.scopes[id]
	if s == nil {
		s = &serverScope{pinned: map[string]bool{}}
		a.connections.scopes[id] = s
	}
	return s
}
func (s *serverScope) setPins(ids []string) {
	s.pinOrder = slices.Clone(ids)
	s.pinned = map[string]bool{}
	for _, id := range s.pinOrder {
		s.pinned[id] = true
	}
}

// draftModelRef is what a conversation born here would run on: the model
// the composer's picker chose for the draft, else the app's default.
func (a *app) draftModelRef() opencode.ModelRef {
	if a.conversation.draftModel.ID != "" {
		return a.conversation.draftModel
	}
	return a.defaultModelRef()
}

// defaultModelRef is what a new conversation starts on: the app's free
// default while the catalog still offers it, else a model that runs for
// nothing, else the server's own default, else the first model still on.
// A server whose providers differ is never told about a model it has not
// heard of, and a free model is never passed over for a paid one.
func (a *app) defaultModelRef() opencode.ModelRef {
	prov, name := opencode.DefaultModel()
	free := opencode.ModelRef{ProviderID: prov, ID: name}
	def := a.scope().serverDefault
	if len(a.scope().models) == 0 {
		// Nothing to choose from yet: the server's own default stands when
		// a dial has read it, the app's free default otherwise.
		if def.ID != "" {
			return def
		}
		return free
	}
	var freeOffered, paidOffered *opencode.Model
	for _, m := range a.scope().models {
		if a.modelDisabled(m) {
			continue
		}
		if m.ProviderID == prov && m.ID == name {
			return free
		}
		if m.Free {
			if freeOffered == nil || modelLess(m, *freeOffered) {
				freeOffered = &m
			}
			continue
		}
		if paidOffered == nil || modelLess(m, *paidOffered) {
			paidOffered = &m
		}
	}
	if freeOffered != nil {
		return opencode.ModelRef{ProviderID: freeOffered.ProviderID, ID: freeOffered.ID}
	}
	if def.ID != "" && !a.connections.modelOff[modelKey(a.connections.activeServer, def.ProviderID, def.ID)] {
		return def
	}
	if paidOffered != nil {
		return opencode.ModelRef{ProviderID: paidOffered.ProviderID, ID: paidOffered.ID}
	}
	return free
}

// modelLess orders the picker: free models first and, inside each group,
// names alphabetically.
func modelLess(one, two opencode.Model) bool {
	if one.Free != two.Free {
		return one.Free
	}
	return one.Name < two.Name
}

// reviseSearch follows the search field: a fresh query queues a lookup
// in the store, and an empty one drops back to the ordinary list at
// once.
func (a *app) reviseSearch() {
	a.search.searchVersion++
	a.search.searchLoading = a.search.searchText != ""
	if !a.search.searchLoading {
		a.search.searchResults = nil
	}
	// Only the newest query matters. Searches never fill the control queue.
	select {
	case <-a.search.searches:
	default:
	}
	a.search.searches <- searchRequest{text: a.search.searchText, version: a.search.searchVersion, config: a.activeServerConn().transport(), route: a.connections.route}
}

// The window controls center in a strip of these: the close button is
// trafficLightButton DIP tall, with trafficLightY of room above and below,
// giving the strip macOS measures -- and the one the top bar, the sidebar's
// header row and the tools toolbar all center on. TrafficLightPosition
// reads the same pair, so the dots cannot drift off the rows' center.
const (
	trafficLightButton = 14
	trafficLightY      = 18
	headerStrip        = trafficLightButton + 2*trafficLightY
)

// view lays the title bar, navigation rail, workspace sidebar, conversation
// and tools pane out over the window's material, which shows where nothing
// is painted.
func (a *app) view(c *ui.Context) {
	a.releaseClosedDialogs()
	a.appShortcuts(c)
	p := paletteFor(c)
	bar := c.TitleBar()
	// The controls center on the header strip, so the top bar and tools
	// toolbar share their center line; a platform that measures no strip
	// falls back to the same height.
	headerH := max(bar.Height, float32(headerStrip))
	// The title bar and rail stand on the window's material, as macOS 26
	// draws them: the desktop shows through, frosted, washed toward the
	// palette so the chrome reads like the app's own surfaces.
	if c.Vibrancy() {
		c.Root().Background(p.chromeWash)
	}

	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.topBar(c, p, headerH, bar.Left)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.navRail(c, p)
			// The sidebar and conversation ride on one card whose top
			// corners follow the window's, so the material frames them.
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).
				Background(p.bg).Radius(cardRadius, cardRadius, 0, 0).Clip().Children(func() {
				if a.layout.settingsOpen {
					a.settingsPane(c, p, headerH)
					return
				}
				if !a.layout.sidebarCollapsed {
					a.sidebarPane(c, p, headerH)
					ui.Box(c).Width(1).FillHeight().Background(p.hairline)
				}
				a.contentPane(c, p)
				if !a.layout.browserCollapsed {
					ui.Box(c).Width(1).FillHeight().Background(p.hairline)
					a.browserPane(c, p, headerH)
				}
			})
		})
	})
	a.attachModal(c, p)
	a.sessionDialogs(c, p)
	a.settingsDialogs(c, p)
	a.browseModal(c, p)
}

// cardRadius rounds the card's top corners, a little tighter than the
// window's own.
const cardRadius float32 = 12

// topBar is the full-width title strip: the window's controls and the
// browser's history on the material, the conversation's title standing
// over it, and the tools toggle at its end, as macOS 26 draws them.
func (a *app) topBar(c *ui.Context, p palette, headerH, barLeft float32) {
	// The title starts where the conversation does, past the sidebar, and
	// follows it in when the sidebar folds away.
	titleStart := float32(navRailWidth) + 12
	if !a.layout.sidebarCollapsed {
		titleStart += a.layout.sidebarW
	}
	// Back and forward are 28 wide, the sidebar toggle 30, and four gaps
	// of the row's 2 stand between them and the title.
	titleGap := max(float32(8), titleStart-(barLeft+8+28+28+30+4*2))
	toolsLabel := "Show tools"
	if !a.layout.browserCollapsed {
		toolsLabel = "Hide tools"
	}

	ui.Row(c).FillWidth().Height(headerH).DragWindow().AlignItems(ui.Center).
		Padding(0, 12, 0, barLeft+8).Gap(2).Children(func() {
		a.browserIconButton(c, p, "Back", icBack, a.browserBack, a.layout.browserHistoryAt <= 0)
		a.browserIconButton(c, p, "Forward", icForward, a.browserForward,
			a.layout.browserHistoryAt < 0 || a.layout.browserHistoryAt >= len(a.layout.browserHistory)-1)

		label := "Hide sidebar"
		if a.layout.sidebarCollapsed {
			label = "Show sidebar"
		}
		a.navButton(c, p, label, icPanel, func() {
			a.layout.sidebarCollapsed = !a.layout.sidebarCollapsed
		}, p.text)

		ui.Box(c).Width(titleGap)
		// The title appears once the conversation has one, and names the
		// page while the settings are up. What the chat has spent rides
		// beside it, quiet for the free models the app runs by default.
		title := a.conversation.selected
		if a.layout.settingsOpen {
			title = "Settings"
		}
		if title != "" {
			ui.Text(c, title).FontSize(14.5).FontWeight(500).TextColor(p.text).
				Shrink(1).MinWidth(0).SingleLine()
			if !a.layout.settingsOpen {
				if cost := a.spentOf(string(a.conversation.target.session)); cost > 0 {
					ui.Text(c, spentLine(cost)).FontSize(12.5).TextColor(p.muted).
						MarginStart(8).Shrink(0).SingleLine()
				}
			}
		}
		ui.Spacer(c)

		a.navButton(c, p, toolsLabel, icPanelRight, func() {
			a.layout.browserCollapsed = !a.layout.browserCollapsed
		}, p.text)
	})
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--reset-metadata" {
		path, err := metadataPath()
		if err == nil {
			err = resetMetadata(path)
		}
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Reset application metadata at %s", path)
		return
	}
	a := newApp()
	mygo.App.OnQuit(a.closeWork)
	mygo.App.WhenReady(func() {
		// A fixed app directory is shared by plain, dev and packaged builds.
		var db *metadataStore
		path, err := metadataPath()
		if err == nil {
			db, err = a.loadState(path)
		}
		if err != nil {
			a.preferences.metadataError = "Settings storage is unavailable: " + err.Error()
			log.Printf("metadata: %v", err)
		}
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Codex",
			Width:     1600,
			Height:    1000,
			MinWidth:  1120,
			MinHeight: 700,
			// The title bar and rail stand on the window's material, as
			// macOS 26 draws them, and the desktop shows through it.
			TitleBarStyle: mygo.TitleBarHidden,
			Vibrancy:      mygo.VibrancyTitlebar,
			// The lights share the header strip the top rows center on;
			// the geometry is the constant trio by view().
			TrafficLightPosition: &mygo.Point{X: 14, Y: trafficLightY},
			StateKey:             "main",
			Content:              ui.View(a.view),
		})
		a.runtime.win = win
		a.runtime.background = func(work func() func()) {
			go func() { win.Update(work()) }()
		}
		win.OnClosed(a.closeWork)
		a.refreshWorkspacePolicy()
		if db != nil {
			a.preferences.metadata = startMetadataWriter(db, func(version uint64, err error) {
				if err != nil {
					log.Printf("metadata: %v", err)
				}
				win.Update(func() { a.acceptMetadataWrite(version, err) })
			})
			mygo.App.OnQuit(a.closeMetadata)
		}

		engine := runner.New(a.runtime.lifetime, win.Update, runnerSink{a}, runner.Options{})
		a.runtime.submitCommand = engine.Submit
		a.runtime.connectTo = engine.Select
		a.runtime.connectTo(runner.Generation(a.connections.route), a.runnerConnection())
		go watchPolicy(a.runtime.lifetime, a.workspace.policyDirs, a.workspace.policyWrites, func(result policyResult) { win.Update(func() { a.acceptPolicy(result) }) })
		go searchChats(a.runtime.lifetime, a.search.searches, func(ctx context.Context, query searchRequest) ([]opencode.Session, error) {
			client, err := opencode.NewClient(query.config)
			if err != nil {
				return nil, err
			}
			rows, _, err := client.ListSessions(ctx, "", query.text, "")
			return rows, err
		}, win.Update, a.acceptSearch)

		// WADA_CAPTURE=/path/out.png writes a PNG of the window and quits:
		// a way to see the interface without clicking through it. The wait
		// lets the boot settle onto the newest conversation first, and
		// WADA_SETTINGS=(1|servers|models) opens the settings page for it.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				wait := 3 * time.Second
				if tab, ok := os.LookupEnv("WADA_SETTINGS"); ok {
					if tab == "" || tab == "1" {
						tab = tabServers
					}
					win.Update(func() { a.openSettings(tab) })
					wait = 6 * time.Second // the catalog fills in behind the page
				}
				if _, remote := os.LookupEnv("WADA_CAPTURE_WAIT"); remote {
					wait = 8 * time.Second // a quiet server takes its dial
				}
				time.AfterFunc(wait, func() {
					win.Update(func() {
						if png, err := win.CapturePage(); err == nil {
							os.WriteFile(out, png, 0o644)
						}
						mygo.App.Exit(0)
					})
				})
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// send appends the draft to the transcript as the user's own words and
// brings the latest block in view.
func (a *app) send() bool {
	text := strings.TrimSpace(a.conversation.draft)
	if text == "" || !a.agentSend(text) {
		return false
	}
	if a.conversation.target.isDraft() {
		a.freshTranscript()
	}
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindUser, Text: a.conversation.draft})
	a.conversation.draft = ""
	a.closeAttach()
	a.layout.feed.ScrollToEnd()
	return true
}

// agentSend queues one user turn for the runner, addressed to the open
// conversation or to a fresh one when none is open. The turn carries the
// app's default model, so a conversation born here starts on what the
// settings enabled.
func (a *app) agentSend(text string) bool {
	def := a.draftModelRef()
	return a.queueCommand(runner.Prompt{Session: a.conversation.target.session, Text: text, Directory: a.chatDir(), Draft: a.conversation.target.revision, Model: opencode.ModelRef{ProviderID: def.ProviderID, ID: def.ID, Variant: def.Variant}}) == runner.Accepted
}

// stopTurn asks the runner to interrupt the running conversation.
func (a *app) stopTurn() {
	a.queueCommand(runner.Stop{Session: a.conversation.transcript.Busy()})
}

// answerPerm sends the decision for one approval request; the runner
// replies to the server and the card leaves the transcript once it lands.
func (a *app) answerPerm(perm *chat.Permission, decision string) {
	if perm == nil {
		return
	}
	a.queueCommand(runner.Answer{Session: perm.Session, Request: perm.ID, Decision: runner.Decision(decision)})
}

// chatDir is the directory the open conversation works in.
func (a *app) chatDir() string {
	if a.conversation.target.isDraft() {
		return a.conversation.draftProject
	}
	for _, s := range a.scope().sessions {
		if s.ID == string(a.conversation.target.session) {
			return s.Directory
		}
	}
	return ""
}

// pickFolderDialog is the real folder chooser: the system's own panel,
// opened off the UI thread as the dialogs document, its answer landing on
// it through the window, which redraws the sidebar.
func (a *app) pickFolderDialog(done func(path string)) {
	win := a.runtime.win
	inBackground(a, func(context.Context) ([]string, error) {
		return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "Choose a folder", Message: "The folder becomes a project in the sidebar.", Directory: true, ButtonLabel: "Add folder", CreateDirectories: true, TreatPackagesAsDirectories: true})
	}, func(paths []string, err error) {
		if err != nil {
			a.streamLine("The folder chooser did not open: " + err.Error())
			return
		}
		if len(paths) > 0 {
			done(paths[0])
		}
	})
}

// openProjectDialog asks the system for a folder and saves it in the
// sidebar; a draft that asked for a new project starts working there.
func (a *app) openProjectDialog() {
	wantsDraft := a.layout.selectAddedProject
	a.layout.selectAddedProject = false
	route, draft := a.connections.route, a.conversation.target.revision
	a.runtime.pickFolder(func(path string) {
		if a.runtime.closed || a.connections.route != route || (wantsDraft && a.conversation.target.revision != draft) {
			return
		}
		clean, err := a.addFolder(path)
		if err != nil {
			a.streamLine("The folder was not added: " + err.Error())
			return
		}
		if wantsDraft {
			a.selectDraftProject(clean)
		}
	})
}

// addFolder saves a chosen folder in the sidebar and reports the path it
// landed at, cleaned.
func (a *app) addFolder(path string) (string, error) {
	clean, err := expandPath(path)
	if err != nil {
		return "", err
	}
	a.addProject(clean)
	return clean, nil
}

// startChatIn prepares a draft in a folder; the first message creates its
// server chat there. On the local server the folder joins the sidebar's
// list, and it exists on this machine to begin with; a remote server's
// rows come with its dial, so only the draft moves. The folder naming a
// chat's home is the server's business, not a folder this machine has.
func (a *app) startChatIn(path string) {
	if a.localActive() {
		clean, err := a.addFolder(path)
		if err != nil {
			a.streamLine("That folder cannot start a chat: " + err.Error())
			return
		}
		path = clean
	}
	a.startDraft(path)
	delete(a.layout.projectCollapsed, path)
}

// openAttachDialog readies the file-attach dialog for the chat's folder.
func (a *app) openAttachDialog() {
	a.closeAttach()
	a.layout.attach = &attachState{open: true}
}

// attachFile quotes a workspace file into the draft, as a fenced block
// the agent reads with the message; typing it again does not repeat it.
// On a local server the file is this machine's to read; a remote server
// names the file its API streams from its own folders.
func (a *app) attachFile() {
	st := a.layout.attach
	if st == nil {
		return
	}
	rel := strings.TrimSpace(st.draft)
	if rel == "" {
		st.note = "Name a file first."
		return
	}
	if !a.localActive() {
		a.attachRemote(st, rel)
		return
	}
	pth := rel
	if !strings.HasPrefix(rel, "~") && !filepath.IsAbs(rel) {
		pth = filepath.Join(a.chatDir(), rel)
	}
	draft, dir := a.conversation.target.revision, a.chatDir()
	type quote struct {
		path string
		data []byte
	}
	runRequest(a, &st.request, func(context.Context) (quote, error) {
		abs, data, err := expandFile(pth)
		return quote{abs, data}, err
	}, func(q quote, err error) {
		if a.layout.attach != st || !st.open || a.conversation.target.revision != draft || a.chatDir() != dir {
			return
		}
		if err != nil {
			st.note = err.Error()
			return
		}
		a.attachQuote(st, q.path, q.data)
	})
}

// attachRemote quotes one file of the chat's folder off the server: an
// absolute name at home in the folder reads against it, the miss of a
// file that is not there shows in the dialog like a local one would, and
// the same size cap keeps megabytes out of the draft.
func (a *app) attachRemote(st *attachState, rel string) {
	dir := a.chatDir()
	if dir == "" {
		st.note = "The chat has no folder of the server to read in yet."
		return
	}
	trimmed := rel
	if strings.HasPrefix(rel, dir+"/") {
		trimmed = strings.TrimPrefix(rel, dir+"/")
	}
	abs := trimmed
	if !filepath.IsAbs(trimmed) {
		abs = filepath.Join(dir, trimmed)
	}
	cfg, draft := a.activeServerConn(), a.conversation.target.revision
	runRequest(a, &st.request, func(ctx context.Context) ([]byte, error) {
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return ag.ReadWorkspaceFile(ctx, dir, trimmed)
	}, func(data []byte, err error) {
		if a.layout.attach != st || !st.open || a.conversation.target.revision != draft || a.chatDir() != dir {
			return
		}
		if err != nil {
			if strings.Contains(err.Error(), "File not found") {
				st.note = "that file is not there"
			} else {
				st.note = err.Error()
			}
			return
		}
		if len(data) > 256*1024 {
			st.note = "that file is too large to quote"
			return
		}
		a.attachQuote(st, abs, data)
	})
}

// attachQuote marks the file into the draft and closes the dialog.
func (a *app) attachQuote(st *attachState, abs string, data []byte) {
	mark := "(file: " + abs + ")"
	if strings.Contains(a.conversation.draft, mark) {
		st.note = "That file is already in the draft."
		return
	}
	a.conversation.draft += "\n\n" + mark + "\n```" + codeLang(abs) + "\n" + string(data) + "\n```\n"
	a.closeAttach()
}

// setAsk moves the shell policy of the chat's folder in the workspace
// config; the note lands in the transcript when the file refuses. A
// remote chat carries the rule over the server's own API, into the file
// it watches on its machine.
func (a *app) setAsk(ask bool) {
	dir := a.chatDir()
	if !a.localActive() {
		if dir == "" {
			// No folder of the server's to settle a rule in yet, and
			// naming one of this machine's would file the server a
			// phantom project.
			a.streamLine("The chat has no folder on the server yet, so the shell rule has nowhere to go.")
			return
		}
		a.workspace.policyRequest.stop()
		cfg, version := a.activeServerConn(), a.workspace.policyVersion
		previous := a.workspace.policyTail
		finished := make(chan struct{})
		a.workspace.policyTail = finished
		a.workspace.policyWriteRevision++
		revision := a.workspace.policyWriteRevision
		inBackground(a, func(ctx context.Context) (string, error) {
			defer close(finished)
			if previous != nil {
				select {
				case <-previous:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return remoteSetPermissionAsk(ctx, cfg, dir, ask)
		}, func(level string, err error) {
			if a.workspace.policyWriteRevision == revision {
				a.acceptPolicy(policyResult{dir: dir, level: level, err: err, revision: version})
			}
		})
		return
	}
	if dir == "" {
		dir = a.preferences.home
	}
	select {
	case a.workspace.policyWrites <- policyChange{dir: dir, ask: ask, revision: a.workspace.policyVersion}:
	default:
		a.streamLine("The shell policy is still being updated. Try again shortly.")
	}
}

// requestRename opens the rename dialog for one conversation.
func (a *app) requestRename(s opencode.Session) {
	a.layout.rename = &renameState{open: true, sid: s.ID, draft: s.Title}
}

// saveRename queues the new title; the sidebar row itself updates when
// the rename event comes back over the stream.
func (a *app) saveRename() {
	st := a.layout.rename
	if st == nil {
		return
	}
	title := strings.TrimSpace(st.draft)
	if st.sid == "" || title == "" || title == a.titleOf(st.sid) {
		a.layout.rename = nil
		return
	}
	if a.queueCommand(runner.Rename{Session: chat.SessionID(st.sid), Title: title}) == runner.Accepted {
		a.layout.rename = nil
	}
}

// acceptUsage gives one stored turn's answer what the store says it spent:
// the words may stand as several rows, and the line belongs under the last
// of them.
func (a *app) acceptUsage(m opencode.Message) {
	a.conversation.transcript.Usage(chat.MessageID(m.ID), m.Cost, m.Tokens.Output+m.Tokens.Reasoning)
}

// refreshUsage asks the pipe for the stored form of the answer a turn just
// ended on: what it spent is only in the store. Nothing is asked when no
// answer of this app's stands to spend anything.
func (a *app) refreshUsage(sid string) {
	if sid == "" || sid != string(a.conversation.target.session) {
		return
	}
	msg := ""
	for i := a.conversation.transcript.Len() - 1; i >= 0; i-- {
		if a.conversation.transcript.Row(i).Message != "" {
			msg = string(a.conversation.transcript.Row(i).Message)
			break
		}
	}
	if msg == "" {
		return
	}
	a.queueCommand(runner.Usage{Session: chat.SessionID(sid), Message: chat.MessageID(msg)})
}

// refreshSessions asks the pipe for the newest page of conversations
// again, after a turn: the list's order and the chat's spending moved, and
// another client may have written a chat of its own meanwhile.
func (a *app) refreshSessions() {
	a.queueCommand(runner.Refresh{})
}

// usageLine words what one answer spent: the money it cost and the tokens
// it wrote, either alone when the other is nothing, and nothing at all
// when both are — a free model that answered nothing worth counting.
func usageLine(cost float64, tokens int) string {
	parts := []string{}
	if cost > 0 {
		parts = append(parts, spentLine(cost))
	}
	if tokens > 0 {
		parts = append(parts, tokensLine(tokens)+" tokens")
	}
	return strings.Join(parts, "  ·  ")
}

// tokensLine words a token count compactly: exact under ten thousand, one
// decimal in the tens, whole thousands past that.
func tokensLine(n int) string {
	switch {
	case n < 10000:
		return strconv.Itoa(n)
	case n < 100000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
}

// spentLine words what one conversation has cost: cents, or fractions of
// one while cents would round the whole of it away.
func spentLine(cost float64) string {
	if cost >= 0.01 {
		return fmt.Sprintf("$%.2f", cost)
	}
	return fmt.Sprintf("$%.4f", cost)
}

// spentOf is what one conversation has spent, as the list last read it.
func (a *app) spentOf(sid string) float64 {
	for _, s := range a.scope().sessions {
		if s.ID == sid {
			return s.Cost
		}
	}
	return 0
}

// titleOf is one conversation's title as the list shows it now.
func (a *app) titleOf(sid string) string {
	for _, s := range a.scope().sessions {
		if s.ID == sid {
			return s.Title
		}
	}
	return ""
}

// sessionModelOf is the model one conversation points at; a conversation
// without one, and the draft that has none at all, read as what a new one
// would start on.
func (a *app) sessionModelOf(sid string) opencode.ModelRef {
	for _, s := range a.scope().sessions {
		if s.ID == sid {
			if s.Model.ID != "" {
				return s.Model
			}
			break
		}
	}
	return a.draftModelRef()
}

// modelLabel is the fancy name of a model ref, from the catalog; the ref
// ids read fine while the catalog has not loaded.
func (a *app) modelLabel(ref opencode.ModelRef) (name string, variants []string) {
	name = ref.ID
	for _, m := range a.scope().models {
		if m.ProviderID == ref.ProviderID && m.ID == ref.ID {
			name = m.Name
			variants = m.Variants
		}
	}
	return name, variants
}

// chooseModel takes the model the picker landed on: the open conversation
// is switched over the pipe, while a draft — which has no conversation yet
// — keeps its pick where the composer can show it, and the first words hand
// it to the chat the server opens.
func (a *app) chooseModel(m opencode.Model, variant string) {
	if a.conversation.target.isDraft() {
		a.layout.pickerOpen = false
		a.conversation.draftModel = opencode.ModelRef{ProviderID: m.ProviderID, ID: m.ID, Variant: variant}
		return
	}
	if a.queueCommand(runner.SelectModel{Session: a.conversation.target.session, Model: opencode.ModelRef{ProviderID: m.ProviderID, ID: m.ID, Variant: variant}}) == runner.Accepted {
		a.layout.pickerOpen = false
	}
}

// pickVariant keeps the conversation's model and changes its reasoning
// effort.
func (a *app) pickVariant(variant string) {
	ref := a.sessionModelOf(string(a.conversation.target.session))
	for _, m := range a.scope().models {
		if m.ProviderID == ref.ProviderID && m.ID == ref.ID {
			a.chooseModel(m, variant)
			return
		}
	}
}

// confirmDelete shows the dialog that stands between a click and a
// conversation gone for good.
func (a *app) confirmDelete(s opencode.Session) {
	a.layout.confirm = &confirmState{open: true, sid: s.ID, what: s.Title}
}

// deleteConfirmed queues the deletion after the dialog's say-so.
func (a *app) deleteConfirmed() {
	st := a.layout.confirm
	if st == nil || st.sid == "" {
		return
	}
	if a.queueCommand(runner.Delete{Session: chat.SessionID(st.sid)}) == runner.Accepted {
		a.layout.confirm = nil
	}
}

// adoptSession settles the view onto one conversation: what a conversation
// click does, and what the boot does with the newest conversation.
func (a *app) adoptSession(s opencode.Session) {
	label := s.Title
	if label == "" {
		label = "New chat"
	}
	a.conversation.selected = label
	a.conversation.target.selectSession(chat.SessionID(s.ID))
	a.freshTranscript()
	// The pane's tools follow the folder on show, and another chat means
	// another folder.
	a.workspaceChanged()
}

// settleBoot finishes the boot: the conversation list lands and the view
// settles onto the newest conversation, as its click would have; with
// none, the transcript starts clean instead of showing the mock.
func (a *app) settleBoot(ls []opencode.Session, next string, lerr error) {
	if lerr == nil {
		a.scope().sessions = ls
		a.scope().nextCursor = next
	}
	if lerr == nil && len(ls) > 0 {
		a.openConversation(ls[0])
	} else {
		a.freshTranscript()
	}
	a.scope().sessionsLoaded = true
}

// startNewChat clears the view; the conversation itself comes into being
// with the first words, so a new chat is not one until it is spoken in.
func (a *app) startNewChat() {
	dir := a.chatDir()
	projectDir := ""
	// The folder the view sat on carries into the draft, whatever server
	// lists it as a project row.
	if slices.Contains(a.projectDirectories(), dir) {
		projectDir = dir
	}
	a.startDraft(projectDir)
}

// startDraft opens an empty composer in one workspace.
func (a *app) startDraft(dir string) {
	a.connections.booted = true
	a.layout.settingsOpen = false
	a.resetDraft(dir)
}

// resetDraft is the complete conversation reset shared by navigation paths.
func (a *app) resetDraft(dir string) {
	a.conversation.target.selectSession("")
	a.conversation.selected = ""
	a.conversation.draft = ""
	a.conversation.draftProject = dir
	a.conversation.draftModel = opencode.ModelRef{}
	a.conversation.commandError = ""
	a.layout.projectPickerOpen = false
	a.freshTranscript()
	a.workspaceChanged()
}

// freshTranscript empties the transcript for another conversation,
// forgetting what was streaming and what tools ran.
func (a *app) freshTranscript() { a.conversation.transcript.Reset(); a.layout.feed = newFeed() }
