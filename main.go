package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// block is one paragraph, bubble or activity line of the transcript.
type block struct {
	id     uint64
	kind   string // "text", "user", "stream", "note", "working", "activity" or "permission"
	text   string
	icon   *ui.SVG
	key    string                         // key into app.expanded for collapsible lines
	detail func(c *ui.Context, p palette) // drawn below the line once expanded
	req    *agentPerm                     // the approval request a permission card carries
	stream *strings.Builder               // amortized storage while text deltas arrive
}

// app is the state the window shows.
type app struct {
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
	browserHistory    []string
	browserHistoryAt  int

	policyDir, policyLevel string
	policyDirs             chan string
	policyWrites           chan policyChange

	// expanded holds collapsible lines ("activity:…").
	expanded map[string]bool

	draft             string
	draftProject      string
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

	// Real conversations from the agent: what pinned rows and search results show and
	// what the composer talks to.
	sessions        []agentSession
	sessionsLoaded  bool
	selectedSession string
	busySession     string // the conversation whose turn is running
	pending         map[string]*agentPerm

	// The folder chooser behind "Add folder" and "New project…": the
	// system's own panel, stood in for in tests. selectAddedProject hands
	// a picked folder to the draft the composer asked for.
	pickFolder         func(done func(path string))
	selectAddedProject bool

	// The file-attach dialog, quoting a workspace file into the draft.
	attachOpen  bool
	attachDraft string
	attachNote  string

	// The rename and delete dialogs of conversation rows, and where the
	// older conversations pick up once the first page is through.
	renameOpen  bool
	renameSid   string
	renameDraft string
	confirmOpen bool
	confirmSid  string
	confirmWhat string
	nextCursor  string
	moreLoading bool

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

	// The model catalog and the picker's popover.
	models       []agentModel
	modelsLoaded bool
	pickerOpen   bool
	modelFilter  string

	// The agent runner feeds the jobs; the rest is its bookkeeping of
	// what is streaming where.
	jobs       chan agentJob
	live       map[string]int    // streaming row by message id and ordinal
	toolNames  map[string]string // tool call id -> tool name
	toolCmds   map[string]string // tool call id -> command line
	toolBlocks map[string]int    // tool call id -> activity row index
}

func newApp() *app {
	home := workdir()
	a := &app{
		sidebarW:            320,
		home:                home,
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
		feed:                ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth},

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
	return a
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

// view lays the title bar, navigation rail, workspace sidebar, conversation
// and tools pane out over the window's material, which shows where nothing
// is painted.
func (a *app) view(c *ui.Context) {
	p := paletteFor(c)
	bar := c.TitleBar()
	// The window controls are centered in a strip of bar.Height, so the
	// top bar and tools toolbar share their center line.
	headerH := bar.Height
	if headerH < 48 {
		headerH = 48
	}
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
	titleGap := titleStart - (barLeft + 8 + 28 + 28 + 30 + 4*2)
	if titleGap < 8 {
		titleGap = 8
	}
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
		// The title appears once the conversation has one.
		if title := a.selected; title != "" {
			ui.Text(c, title).FontSize(14.5).FontWeight(500).TextColor(p.text).Grow(1).SingleLine()
		} else {
			ui.Spacer(c)
		}

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
			db, err = a.loadSidebar(filepath.Join(dir, "wada-desktop-go", "metadata.db"))
		}
		if err != nil {
			a.metadataError = "Sidebar storage is unavailable: " + err.Error()
			log.Printf("sidebar metadata: %v", err)
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
			// Keep the title strip compact and center its controls on the
			// 50 DIP header (the close button is 14 DIP tall, with 18 DIP
			// of room above and below).
			TrafficLightPosition: &mygo.Point{X: 14, Y: 18},
			StateKey:             "main",
			Content:              ui.View(a.view),
		})
		a.win = win
		if db != nil {
			a.metadata = startMetadataWriter(db, func(version uint64, err error) {
				if err != nil {
					log.Printf("sidebar metadata: %v", err)
				}
				win.Update(func() { a.acceptMetadataWrite(version, err) })
			})
			mygo.App.OnQuit(a.closeMetadata)
		}

		// The agent pipe lives in the background; it publishes what it
		// streams back into the view through Window.Update.
		go a.connect(win, a.jobs, append([]string(nil), a.pinOrder...))

		// WADA_CAPTURE=/path/out.png writes a PNG of the window and quits:
		// a way to see the interface without clicking through it. The wait
		// lets the boot settle onto the newest conversation first.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				time.AfterFunc(3*time.Second, func() {
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
func (a *app) send() {
	if strings.TrimSpace(a.draft) == "" {
		return
	}
	// A turn starting on nothing real opens the conversation: its first
	// words must not land under whatever the mock left on display.
	if a.selectedSession == "" {
		a.freshTranscript()
	}
	a.blocks = append(a.blocks, block{kind: "user", text: a.draft})
	text := strings.TrimSpace(a.draft)
	a.draft = ""
	a.feed.ScrollToEnd()
	// Hand the turn to the agent runner; where none runs, the transcript
	// keeps acting as the design mock it is.
	a.agentSend(text)
}

// agentSend queues one user turn for the runner, addressed to the open
// conversation or to a fresh one when none is open.
func (a *app) agentSend(text string) {
	select {
	case a.jobs <- agentJob{kind: "prompt", sid: a.selectedSession, text: text, dir: a.chatDir(), draft: a.draftVersion}:
	default:
	}
}

// stopTurn asks the runner to interrupt the running conversation.
func (a *app) stopTurn() {
	select {
	case a.jobs <- agentJob{kind: "stop", sid: a.busySession}:
	default:
	}
}

// answerPerm sends the decision for one approval request; the runner
// replies to the server and the card leaves the transcript once it lands.
func (a *app) answerPerm(perm *agentPerm, decision string) {
	if perm == nil {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "answer", sid: perm.session, request: perm.id, decision: decision}:
	default:
	}
}

// chatDir is the directory the open conversation works in.
func (a *app) chatDir() string {
	if a.selectedSession == "" {
		return a.draftProject
	}
	for _, s := range a.sessions {
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
// server chat there, and the folder joins the sidebar, open for it.
func (a *app) startChatIn(path string) {
	clean, err := a.addFolder(path)
	if err != nil {
		a.streamLine("That folder cannot start a chat: " + err.Error())
		return
	}
	a.startNewChat()
	a.draftProject = clean
	a.projectPickerOpen = false
	delete(a.projectCollapsed, clean)
}

// openAttachDialog readies the file-attach dialog for the chat's folder.
func (a *app) openAttachDialog() {
	a.attachDraft = ""
	a.attachNote = ""
	a.attachOpen = true
}

// attachFile quotes a workspace file into the draft, as a fenced block
// the agent reads with the message; typing it again does not repeat it.
func (a *app) attachFile() {
	rel := strings.TrimSpace(a.attachDraft)
	if rel == "" {
		a.attachNote = "Name a file first."
		return
	}
	pth := rel
	if !strings.HasPrefix(rel, "~") && !filepath.IsAbs(rel) {
		pth = filepath.Join(a.chatDir(), rel)
	}
	abs, data, err := expandFile(pth)
	if err != nil {
		a.attachNote = err.Error()
		return
	}
	mark := "(file: " + abs + ")"
	if strings.Contains(a.draft, mark) {
		a.attachNote = "That file is already in the draft."
		return
	}
	a.draft += "\n\n" + mark + "\n```" + codeLang(abs) + "\n" + string(data) + "\n```\n"
	a.attachOpen = false
	a.attachDraft = ""
	a.attachNote = ""
}

// selectedRow is the conversation row the transcript currently looks at.
func (a *app) selectedRow() (agentSession, bool) {
	for _, s := range a.sessions {
		if s.id == a.selectedSession {
			return s, true
		}
	}
	return agentSession{}, false
}

// setAsk moves the shell policy of the chat's folder in the workspace
// config; the note lands in the transcript when the file refuses.
func (a *app) setAsk(ask bool) {
	dir := a.chatDir()
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
	a.renameSid = s.id
	a.renameDraft = s.title
	a.renameOpen = true
}

// saveRename queues the new title; the sidebar row itself updates when
// the rename event comes back over the stream.
func (a *app) saveRename() {
	a.renameOpen = false
	title := strings.TrimSpace(a.renameDraft)
	if a.renameSid == "" || title == "" || title == a.titleOf(a.renameSid) {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "rename", sid: a.renameSid, text: title}:
	default:
	}
}

// titleOf is one conversation's title as the list shows it now.
func (a *app) titleOf(sid string) string {
	for _, s := range a.sessions {
		if s.id == sid {
			return s.title
		}
	}
	return ""
}

// sessionModelOf is the model one conversation points at; the app's free
// default stands in before anything was picked.
func (a *app) sessionModelOf(sid string) agentModelRef {
	for _, s := range a.sessions {
		if s.id == sid {
			if s.model.id != "" {
				return s.model
			}
			break
		}
	}
	prov, name := sessionModel()
	return agentModelRef{providerID: prov, id: name}
}

// modelLabel is the fancy name of a model ref, from the catalog; the ref
// ids read fine while the catalog has not loaded.
func (a *app) modelLabel(ref agentModelRef) (name string, variants []string) {
	name = ref.id
	for _, m := range a.models {
		if m.providerID == ref.providerID && m.id == ref.id {
			name = m.name
			variants = m.variants
		}
	}
	return name, variants
}

// chooseModel queues a switch of what the open conversation runs on.
func (a *app) chooseModel(m agentModel, variant string) {
	a.pickerOpen = false
	select {
	case a.jobs <- agentJob{kind: "model", sid: a.selectedSession, draft: a.draftVersion,
		provider: m.providerID, model: m.id, variant: variant}:
	default:
	}
}

// pickVariant keeps the conversation's model and changes its reasoning
// effort.
func (a *app) pickVariant(variant string) {
	ref := a.sessionModelOf(a.selectedSession)
	for _, m := range a.models {
		if m.providerID == ref.providerID && m.id == ref.id {
			a.chooseModel(m, variant)
			return
		}
	}
}

// confirmDelete shows the dialog that stands between a click and a
// conversation gone for good.
func (a *app) confirmDelete(s agentSession) {
	a.confirmSid = s.id
	a.confirmWhat = s.title
	a.confirmOpen = true
}

// deleteConfirmed queues the deletion after the dialog's say-so.
func (a *app) deleteConfirmed() {
	a.confirmOpen = false
	if a.confirmSid == "" {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "delete", sid: a.confirmSid}:
	default:
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
}

// settleBoot finishes the boot: the conversation list lands and the view
// settles onto the newest conversation, as its click would have; with
// none, the transcript starts clean instead of showing the mock.
func (a *app) settleBoot(ls []agentSession, next string, lerr error) {
	if lerr == nil {
		a.sessions = ls
		a.nextCursor = next
	}
	if lerr == nil && len(ls) > 0 {
		a.adoptSession(ls[0])
		select {
		case a.jobs <- agentJob{kind: "open", sid: ls[0].id}:
		default:
		}
	} else {
		a.freshTranscript()
	}
	a.sessionsLoaded = true
}

// startNewChat clears the view; the conversation itself comes into being
// with the first words, so a new chat is not one until it is spoken in.
func (a *app) startNewChat() {
	dir := a.chatDir()
	a.draftProject = ""
	for _, project := range a.projects {
		if dir == project {
			a.draftProject = dir
			break
		}
	}
	a.draftVersion++
	a.projectPickerOpen = false
	a.selected = ""
	a.selectedSession = ""
	a.draft = ""
	a.freshTranscript()
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
	a.feed = ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
}
