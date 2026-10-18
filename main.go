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

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// blockKind names what one transcript row is; blockRow's switch and the
// render test are a new kind's other homes.
type blockKind string

const (
	kindText       blockKind = "text"       // an answer, through the markdown package
	kindUser       blockKind = "user"       // the user's own words
	kindStream     blockKind = "stream"     // an answer still arriving
	kindNote       blockKind = "note"       // engine trouble under the answers
	kindWorking    blockKind = "working"    // the loader of a turn in flight
	kindActivity   blockKind = "activity"   // one tool call of the agent
	kindPermission blockKind = "permission" // an approval waiting on the user
)

// block is one paragraph, bubble or activity line of the transcript.
type block struct {
	id     uint64
	kind   blockKind // what the row is; one of the kind* constants
	text   string
	icon   string
	key    string           // key into app.expanded for collapsible lines
	detail []string         // drawn below the line once expanded
	req    *agentPerm       // the approval request a permission card carries
	stream *strings.Builder // amortized storage while text deltas arrive
	// The stored turn this row came from, and what it spent: the usage
	// line only the store knows waits under the answer that earned it.
	msg    string
	cost   float64
	tokens int
}

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
	sessions       []agentSession
	sessionsLoaded bool
	nextCursor     string
	moreLoading    bool
	models         []agentModel
	modelsLoaded   bool
	// The model the server itself would run, for when the catalog offers
	// nothing this app knows; zero until a dial reads it.
	serverDefault agentModelRef
	// The folders the server works in, as its project list says them;
	// empty until a dial reads it.
	projects []string
}

// app is the state the window shows.
type app struct {
	registryRequests map[string]*requestState
	policyRequest    requestState

	commandError string

	lifetime     context.Context
	stopLifetime context.CancelFunc
	workCtx      context.Context
	stopWork     context.CancelFunc

	background func(work func() func())
	closed     bool

	selected    string
	sidebarW    float32
	home        string
	projects    []string
	projectPins map[string]bool
	pinOrder    []string
	// win is the window the view draws in; the folder chooser lands its
	// answer through it, as the agent pipe does.
	win                 *mygo.Window
	metadata            *metadataWriter
	metadataVersion     uint64
	metadataSaving      bool
	metadataError       string
	unavailableProjects map[string]bool

	// Navigation and pinned chats belong to the Codex sidebar, while the
	// browser history and active tool belong to the native right pane.
	sidebarCollapsed bool
	browserCollapsed bool
	selectedRail     string
	projectsExpanded bool
	// The project tree: folders fold their chats away, and a folder with
	// many chats previews a few before showing the rest.
	projectCollapsed  map[string]bool
	projectChatsShown map[string]bool
	pinned            map[string]bool
	searchOpen        bool
	browserAddress    string
	browserPage       string
	browserTool       string
	// changes is the workspace diff the Changes tool holds; files is the
	// tree the Files tool holds. Both nil until their tool opens.
	changes          *changesState
	files            *filesState
	browserHistory   []string
	browserHistoryAt int

	policyDir, policyLevel string
	policyDirs             chan string
	policyWrites           chan policyChange

	// expanded holds collapsible lines ("activity:…").
	expanded map[string]bool

	draft        string
	draftProject string
	// draftModel is what a conversation born here runs on once the
	// composer's picker has chosen one: a draft has no session yet, so its
	// pick waits here until the first words open one.
	draftModel        agentModelRef
	draftVersion      uint64
	projectPickerOpen bool
	projectFilter     string

	feed        ui.ListState
	resultsList ui.ListState
	blocks      []block
	nextBlockID uint64

	// md draws the transcript's markdown, one set of options shared by
	// every message, tuned to the palette each frame.
	md      markdown.Options
	mdTheme markdown.Theme

	// The open conversation and its streaming state; one transcript
	// renders no matter which server owns it.
	selectedSession string
	busySession     string // the conversation whose turn is running
	pending         map[string]*agentPerm

	// The folder chooser behind "Add folder" and "New project…": the
	// system's own panel, stood in for in tests. selectAddedProject hands
	// a picked folder to the draft the composer asked for.
	pickFolder         func(done func(path string))
	selectAddedProject bool
	// probe tests one server connection in the background, stood in for
	// in tests as pickFolder is; pair redeems a pairing link the same way.
	probe func(cfg serverConn)
	pair  func(link string, done func(addr, token string, err error))
	// copy puts a minted pairing link on the system clipboard, stood in for
	// in tests as probe is; the links it made wait in pairLinks.
	copy      func(text string) error
	pairLinks map[string]mintedLink

	// The composer's attach drawer and the conversation rows' rename and
	// delete dialogs: one state struct each, nil until its dialog opens.
	// open is DialogBase's own switch, which the backdrop and Escape clear.
	attach  *attachState
	rename  *renameState
	confirm *confirmState
	// browse is the server-folder dialog a remote draft's picker and the
	// sidebar's Add folder open.
	browse *browseState

	// Each server's conversations and catalog, keyed by server id; a
	// new server's is empty until the first dial fills it.
	scopes map[string]*serverScope

	// The chats search: what the field shows, the results it last got
	// and whether one is on.
	searchText    string
	searchResults []agentSession
	searchLoading bool
	searchVersion uint64
	searches      chan searchRequest
	// focusSearch hands the field the keyboard once, from the header's
	// search shape.
	focusSearch bool

	// The picker's popover over the active server's catalog.
	pickerOpen  bool
	modelFilter string

	// The settings: the page itself, its tab, the server connections and
	// health, the sketch of the server being edited or deleted, and the
	// models the composer's picker leaves out.
	settingsOpen  bool
	settingsTab   string
	servers       []serverConn
	activeServer  string
	serverHealth  map[string]serverHealth
	serverFacts   map[string]serverFacts
	serverEditor  *serverEditor
	serverConfirm *confirmState
	modelsQuery   string
	modelsList    ui.ListState
	modelOff      map[string]bool // modelKey of every model turned off
	// The pins are the active server's; every server's list is kept so a
	// switch hands them back.
	pinsByServer map[string][]string

	// The agent runner feeds the jobs; the rest is its bookkeeping of
	// what is streaming where.
	route      uint64 // UI-owned generation of the active connection
	jobs       chan agentJob
	live       map[string]int    // streaming row by message id and ordinal
	toolNames  map[string]string // tool call id -> tool name
	toolCmds   map[string]string // tool call id -> command line
	toolBlocks map[string]int    // tool call id -> activity row index
}

func newApp() *app {
	home := workdir()
	lifetime, stopLifetime := context.WithCancel(context.Background())
	workCtx, stopWork := context.WithCancel(lifetime)
	a := &app{
		lifetime: lifetime, stopLifetime: stopLifetime, workCtx: workCtx, stopWork: stopWork,
		sidebarW:            320,
		home:                home,
		registryRequests:    map[string]*requestState{},
		selectedRail:        "home",
		browserCollapsed:    true,
		pinned:              map[string]bool{},
		projectPins:         map[string]bool{},
		projectCollapsed:    map[string]bool{},
		projectChatsShown:   map[string]bool{},
		unavailableProjects: map[string]bool{},
		browserHistoryAt:    -1,
		expanded:            map[string]bool{},
		blocks:              []block{},
		feed:                newFeed(),

		settingsTab:  tabServers,
		servers:      []serverConn{defaultLocalServer()},
		activeServer: localServerID,
		serverHealth: map[string]serverHealth{},
		serverFacts:  map[string]serverFacts{},
		scopes:       map[string]*serverScope{},
		modelOff:     map[string]bool{},
		pinsByServer: map[string][]string{},

		jobs:         make(chan agentJob, 16),
		live:         map[string]int{},
		toolNames:    map[string]string{},
		toolCmds:     map[string]string{},
		toolBlocks:   map[string]int{},
		pending:      map[string]*agentPerm{},
		searches:     make(chan searchRequest, 1),
		policyDirs:   make(chan string, 1),
		policyWrites: make(chan policyChange, 16),
	}
	a.pickFolder = a.pickFolderDialog
	a.probe = a.runProbe
	a.pair = a.runPair
	a.copy = a.copyToClipboard
	a.pairLinks = map[string]mintedLink{}
	return a
}

// scope is the active server's slice of the app state, made on first use,
// so the view reads sessions and models straight from the reading side.
func (a *app) scope() *serverScope {
	s := a.scopes[a.activeServer]
	if s == nil {
		s = &serverScope{}
		a.scopes[a.activeServer] = s
	}
	return s
}

// draftModelRef is what a conversation born here would run on: the model
// the composer's picker chose for the draft, else the app's default.
func (a *app) draftModelRef() agentModelRef {
	if a.draftModel.id != "" {
		return a.draftModel
	}
	return a.defaultModelRef()
}

// defaultModelRef is what a new conversation starts on: the app's free
// default while the catalog still offers it, else a model that runs for
// nothing, else the server's own default, else the first model still on.
// A server whose providers differ is never told about a model it has not
// heard of, and a free model is never passed over for a paid one.
func (a *app) defaultModelRef() agentModelRef {
	prov, name := sessionModel()
	free := agentModelRef{providerID: prov, id: name}
	def := a.scope().serverDefault
	if len(a.scope().models) == 0 {
		// Nothing to choose from yet: the server's own default stands when
		// a dial has read it, the app's free default otherwise.
		if def.id != "" {
			return def
		}
		return free
	}
	var freeOffered, paidOffered *agentModel
	for _, m := range a.scope().models {
		if a.modelDisabled(m) {
			continue
		}
		if m.providerID == prov && m.id == name {
			return free
		}
		if m.free {
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
		return agentModelRef{providerID: freeOffered.providerID, id: freeOffered.id}
	}
	if def.id != "" && !a.modelOff[modelKey(a.activeServer, def.providerID, def.id)] {
		return def
	}
	if paidOffered != nil {
		return agentModelRef{providerID: paidOffered.providerID, id: paidOffered.id}
	}
	return free
}

// modelLess orders the picker: free models first and, inside each group,
// names alphabetically.
func modelLess(one, two agentModel) bool {
	if one.free != two.free {
		return one.free
	}
	return one.name < two.name
}

// reviseSearch follows the search field: a fresh query queues a lookup
// in the store, and an empty one drops back to the ordinary list at
// once.
func (a *app) reviseSearch() {
	a.searchVersion++
	a.searchLoading = a.searchText != ""
	if !a.searchLoading {
		a.searchResults = nil
	}
	// Only the newest query matters. Searches never fill the control queue.
	select {
	case <-a.searches:
	default:
	}
	a.searches <- searchRequest{text: a.searchText, version: a.searchVersion}
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
				if a.settingsOpen {
					a.settingsPane(c, p, headerH)
					return
				}
				if !a.sidebarCollapsed {
					a.sidebarPane(c, p, headerH)
					ui.Box(c).Width(1).FillHeight().Background(p.hairline)
				}
				a.contentPane(c, p)
				if !a.browserCollapsed {
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
	if !a.sidebarCollapsed {
		titleStart += a.sidebarW
	}
	// Back and forward are 28 wide, the sidebar toggle 30, and four gaps
	// of the row's 2 stand between them and the title.
	titleGap := max(float32(8), titleStart-(barLeft+8+28+28+30+4*2))
	toolsLabel := "Show tools"
	if !a.browserCollapsed {
		toolsLabel = "Hide tools"
	}

	ui.Row(c).FillWidth().Height(headerH).DragWindow().AlignItems(ui.Center).
		Padding(0, 12, 0, barLeft+8).Gap(2).Children(func() {
		a.browserIconButton(c, p, "Back", icBack, a.browserBack, a.browserHistoryAt <= 0)
		a.browserIconButton(c, p, "Forward", icForward, a.browserForward,
			a.browserHistoryAt < 0 || a.browserHistoryAt >= len(a.browserHistory)-1)

		label := "Hide sidebar"
		if a.sidebarCollapsed {
			label = "Show sidebar"
		}
		a.navButton(c, p, label, icPanel, func() {
			a.sidebarCollapsed = !a.sidebarCollapsed
		}, p.text)

		ui.Box(c).Width(titleGap)
		// The title appears once the conversation has one, and names the
		// page while the settings are up. What the chat has spent rides
		// beside it, quiet for the free models the app runs by default.
		title := a.selected
		if a.settingsOpen {
			title = "Settings"
		}
		if title != "" {
			ui.Text(c, title).FontSize(14.5).FontWeight(500).TextColor(p.text).
				Shrink(1).MinWidth(0).SingleLine()
			if !a.settingsOpen {
				if cost := a.spentOf(a.selectedSession); cost > 0 {
					ui.Text(c, spentLine(cost)).FontSize(12.5).TextColor(p.muted).
						MarginStart(8).Shrink(0).SingleLine()
				}
			}
		}
		ui.Spacer(c)

		a.navButton(c, p, toolsLabel, icPanelRight, func() {
			a.browserCollapsed = !a.browserCollapsed
		}, p.text)
	})
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		// A fixed app directory is shared by plain, dev and packaged builds.
		var db *metadataStore
		dir, err := os.UserConfigDir()
		if err == nil {
			db, err = a.loadState(filepath.Join(dir, "wada-desktop-go", "metadata.db"))
		}
		if err != nil {
			a.metadataError = "Settings storage is unavailable: " + err.Error()
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
		a.win = win
		a.background = func(work func() func()) {
			go func() { win.Update(work()) }()
		}
		win.OnClosed(a.closeWork)
		a.refreshWorkspacePolicy()
		if db != nil {
			a.metadata = startMetadataWriter(db, func(version uint64, err error) {
				if err != nil {
					log.Printf("metadata: %v", err)
				}
				win.Update(func() { a.acceptMetadataWrite(version, err) })
			})
			mygo.App.OnQuit(a.closeMetadata)
		}

		// The agent pipe lives in the background; it dials the active
		// server and publishes what it streams back into the view through
		// Window.Update.
		go a.connect(win, a.jobs, a.activeServerConn(), append([]string(nil), a.pinOrder...))

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
	text := strings.TrimSpace(a.draft)
	if text == "" || !a.agentSend(text) {
		return false
	}
	if a.selectedSession == "" {
		a.freshTranscript()
	}
	a.blocks = append(a.blocks, block{kind: kindUser, text: a.draft})
	a.draft = ""
	a.feed.ScrollToEnd()
	return true
}

// agentSend queues one user turn for the runner, addressed to the open
// conversation or to a fresh one when none is open. The turn carries the
// app's default model, so a conversation born here starts on what the
// settings enabled.
func (a *app) agentSend(text string) bool {
	def := a.draftModelRef()
	return a.queueJob(agentJob{kind: jobPrompt, sid: a.selectedSession, text: text, dir: a.chatDir(),
		draft: a.draftVersion, provider: def.providerID, model: def.id, variant: def.variant}) == admissionAccepted
}

// stopTurn asks the runner to interrupt the running conversation.
func (a *app) stopTurn() {
	a.queueJob(agentJob{kind: jobStop, sid: a.busySession})
}

// answerPerm sends the decision for one approval request; the runner
// replies to the server and the card leaves the transcript once it lands.
func (a *app) answerPerm(perm *agentPerm, decision string) {
	if perm == nil {
		return
	}
	a.queueJob(agentJob{kind: jobAnswer, sid: perm.session, request: perm.id, decision: decision})
}

// chatDir is the directory the open conversation works in.
func (a *app) chatDir() string {
	if a.selectedSession == "" {
		return a.draftProject
	}
	for _, s := range a.scope().sessions {
		if s.id == a.selectedSession {
			return s.dir
		}
	}
	return ""
}

// pickFolderDialog is the real folder chooser: the system's own panel,
// opened off the UI thread as the dialogs document, its answer landing on
// it through the window, which redraws the sidebar.
func (a *app) pickFolderDialog(done func(path string)) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:                     a.win,
			Title:                      "Choose a folder",
			Message:                    "The folder becomes a project in the sidebar.",
			ButtonLabel:                "Add folder",
			Directory:                  true,
			CreateDirectories:          true,
			TreatPackagesAsDirectories: true,
		})
		land := func() {
			if err != nil {
				a.streamLine("The folder chooser did not open: " + err.Error())
				return
			}
			if len(paths) > 0 {
				done(paths[0])
			}
		}
		if a.win == nil { // headless, as tests run the view
			mygo.RunOnMain(land)
			return
		}
		a.win.Update(land)
	}()
}

// openProjectDialog asks the system for a folder and saves it in the
// sidebar; a draft that asked for a new project starts working there.
func (a *app) openProjectDialog() {
	wantsDraft := a.selectAddedProject
	a.selectAddedProject = false
	a.pickFolder(func(path string) {
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
	delete(a.projectCollapsed, path)
}

// openAttachDialog readies the file-attach dialog for the chat's folder.
func (a *app) openAttachDialog() {
	a.attach = &attachState{open: true}
}

// attachFile quotes a workspace file into the draft, as a fenced block
// the agent reads with the message; typing it again does not repeat it.
// On a local server the file is this machine's to read; a remote server
// names the file its API streams from its own folders.
func (a *app) attachFile() {
	st := a.attach
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
	draft, dir := a.draftVersion, a.chatDir()
	type quote struct {
		path string
		data []byte
	}
	runRequest(a, &st.request, func(context.Context) (quote, error) {
		abs, data, err := expandFile(pth)
		return quote{abs, data}, err
	}, func(q quote, err error) {
		if a.attach != st || a.draftVersion != draft || a.chatDir() != dir {
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
	cfg, draft := a.activeServerConn(), a.draftVersion
	runRequest(a, &st.request, func(ctx context.Context) ([]byte, error) {
		ag, err := standaloneAgent(cfg)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return ag.readWorkspaceFile(ctx, dir, trimmed)
	}, func(data []byte, err error) {
		if a.attach != st || a.draftVersion != draft || a.chatDir() != dir {
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
	if strings.Contains(a.draft, mark) {
		st.note = "That file is already in the draft."
		return
	}
	a.draft += "\n\n" + mark + "\n```" + codeLang(abs) + "\n" + string(data) + "\n```\n"
	a.attach = nil
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
		cfg := a.activeServerConn()
		inBackground(a, func(ctx context.Context) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return remoteSetPermissionAsk(ctx, cfg, dir, ask)
		}, func(level string, err error) {
			a.acceptPolicy(policyResult{dir: dir, level: level, err: err})
		})
		return
	}
	if dir == "" {
		dir = a.home
	}
	select {
	case a.policyWrites <- policyChange{dir: dir, ask: ask}:
	default:
		a.streamLine("The shell policy is still being updated. Try again shortly.")
	}
}

// requestRename opens the rename dialog for one conversation.
func (a *app) requestRename(s agentSession) {
	a.rename = &renameState{open: true, sid: s.id, draft: s.title}
}

// saveRename queues the new title; the sidebar row itself updates when
// the rename event comes back over the stream.
func (a *app) saveRename() {
	st := a.rename
	if st == nil {
		return
	}
	title := strings.TrimSpace(st.draft)
	if st.sid == "" || title == "" || title == a.titleOf(st.sid) {
		a.rename = nil
		return
	}
	if a.queueJob(agentJob{kind: jobRename, sid: st.sid, text: title}) == admissionAccepted {
		a.rename = nil
	}
}

// acceptUsage gives one stored turn's answer what the store says it spent:
// the words may stand as several rows, and the line belongs under the last
// of them.
func (a *app) acceptUsage(m agentMessage) {
	last := -1
	for i := range a.blocks {
		if a.blocks[i].msg == m.ID {
			last = i
		}
	}
	if last < 0 {
		return
	}
	a.blocks[last].cost = m.Cost
	a.blocks[last].tokens = m.Tokens.Output + m.Tokens.Reasoning
}

// refreshUsage asks the pipe for the stored form of the answer a turn just
// ended on: what it spent is only in the store. Nothing is asked when no
// answer of this app's stands to spend anything.
func (a *app) refreshUsage(sid string) {
	if sid == "" || sid != a.selectedSession {
		return
	}
	msg := ""
	for i := len(a.blocks) - 1; i >= 0; i-- {
		if a.blocks[i].msg != "" {
			msg = a.blocks[i].msg
			break
		}
	}
	if msg == "" {
		return
	}
	a.queueJob(agentJob{kind: jobUsage, sid: sid, msg: msg})
}

// refreshSessions asks the pipe for the newest page of conversations
// again, after a turn: the list's order and the chat's spending moved, and
// another client may have written a chat of its own meanwhile.
func (a *app) refreshSessions() {
	a.queueJob(agentJob{kind: jobRefresh})
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
		if s.id == sid {
			return s.cost
		}
	}
	return 0
}

// titleOf is one conversation's title as the list shows it now.
func (a *app) titleOf(sid string) string {
	for _, s := range a.scope().sessions {
		if s.id == sid {
			return s.title
		}
	}
	return ""
}

// sessionModelOf is the model one conversation points at; a conversation
// without one, and the draft that has none at all, read as what a new one
// would start on.
func (a *app) sessionModelOf(sid string) agentModelRef {
	for _, s := range a.scope().sessions {
		if s.id == sid {
			if s.model.id != "" {
				return s.model
			}
			break
		}
	}
	return a.draftModelRef()
}

// modelLabel is the fancy name of a model ref, from the catalog; the ref
// ids read fine while the catalog has not loaded.
func (a *app) modelLabel(ref agentModelRef) (name string, variants []string) {
	name = ref.id
	for _, m := range a.scope().models {
		if m.providerID == ref.providerID && m.id == ref.id {
			name = m.name
			variants = m.variants
		}
	}
	return name, variants
}

// chooseModel takes the model the picker landed on: the open conversation
// is switched over the pipe, while a draft — which has no conversation yet
// — keeps its pick where the composer can show it, and the first words hand
// it to the chat the server opens.
func (a *app) chooseModel(m agentModel, variant string) {
	a.pickerOpen = false
	if a.selectedSession == "" {
		a.draftModel = agentModelRef{providerID: m.providerID, id: m.id, variant: variant}
		return
	}
	a.queueJob(agentJob{kind: jobModel, sid: a.selectedSession, draft: a.draftVersion,
		provider: m.providerID, model: m.id, variant: variant})
}

// pickVariant keeps the conversation's model and changes its reasoning
// effort.
func (a *app) pickVariant(variant string) {
	ref := a.sessionModelOf(a.selectedSession)
	for _, m := range a.scope().models {
		if m.providerID == ref.providerID && m.id == ref.id {
			a.chooseModel(m, variant)
			return
		}
	}
}

// confirmDelete shows the dialog that stands between a click and a
// conversation gone for good.
func (a *app) confirmDelete(s agentSession) {
	a.confirm = &confirmState{open: true, sid: s.id, what: s.title}
}

// deleteConfirmed queues the deletion after the dialog's say-so.
func (a *app) deleteConfirmed() {
	st := a.confirm
	if st == nil || st.sid == "" {
		return
	}
	if a.queueJob(agentJob{kind: jobDelete, sid: st.sid}) == admissionAccepted {
		a.confirm = nil
	}
}

// adoptSession settles the view onto one conversation: what a conversation
// click does, and what the boot does with the newest conversation.
func (a *app) adoptSession(s agentSession) {
	label := s.title
	if label == "" {
		label = "New chat"
	}
	a.selected = label
	a.selectedSession = s.id
	a.freshTranscript()
	// The pane's tools follow the folder on show, and another chat means
	// another folder.
	a.workspaceChanged()
}

// settleBoot finishes the boot: the conversation list lands and the view
// settles onto the newest conversation, as its click would have; with
// none, the transcript starts clean instead of showing the mock.
func (a *app) settleBoot(ls []agentSession, next string, lerr error) {
	if lerr == nil {
		a.scope().sessions = ls
		a.scope().nextCursor = next
	}
	if lerr == nil && len(ls) > 0 {
		a.adoptSession(ls[0])
		a.queueJob(agentJob{kind: jobOpen, sid: ls[0].id})
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
	a.settingsOpen = false
	a.draftProject = dir
	a.draftVersion++
	a.projectPickerOpen = false
	a.selected = ""
	a.selectedSession = ""
	a.draft = ""
	a.draftModel = agentModelRef{}
	a.freshTranscript()
	a.workspaceChanged()
}

// freshTranscript empties the transcript for another conversation,
// forgetting what was streaming and what tools ran.
func (a *app) freshTranscript() {
	a.blocks = nil
	a.live = map[string]int{}
	a.toolNames = map[string]string{}
	a.toolCmds = map[string]string{}
	a.toolBlocks = map[string]int{}
	a.pending = map[string]*agentPerm{}
	a.busySession = ""
	a.feed = newFeed()
}
