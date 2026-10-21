package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/runner"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/theme"
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

	sessions       []agent.Session
	sessionsLoaded bool
	nextCursor     string
	moreLoading    bool
	models         []agent.Model
	modelsLoaded   bool
	// The personas a conversation can run under ("build", "plan"), zero
	// until a dial reads the list.
	agents []agent.Persona
	// The model the server itself would run, for when the catalog offers
	// nothing this app knows; zero until a dial reads it.
	serverDefault agent.ModelRef
	// unread and attention mark chats that moved while another one was on
	// show; attention is a background approval waiting for an answer.
	unread    map[string]bool
	attention map[string]bool
	// archived hides chats from the pinned and project lists without
	// deleting them; the Archived chats filter is where they come back.
	archived map[string]bool
	// queued holds the accepted prompts waiting for the runner's one
	// active turn to finish, per chat, oldest first.
	queued map[string][]string
	// drafts holds each chat's unsent words, keyed by session id; the empty
	// key is the new-chat composer.
	drafts map[string]string
	// The folders the server works in, as its project list says them;
	// empty until a dial reads it.
	projects []string
}

// app is the state the window shows.
type app struct {
	registeredActions   []feature.Action
	registeredPanels    []feature.Panel
	registeredShortcuts []shortcutSpec
	registeredSlashes   []slashCommand
	savedModules        map[string]json.RawMessage
	services            featureServices
	backend             agent.Backend
	runtime             runtimeState
	connections         connectionsState
	conversation        conversationState
	preferences         preferencesState
	search              searchState
	layout              viewState
	features            []feature.Module
	featureRevision     uint64
}

func newApp(backend agent.Backend, factories ...feature.Factory) *app {
	home := workdir()
	lifetime, stopLifetime := context.WithCancel(context.Background())
	workCtx, stopWork := context.WithCancel(lifetime)
	a := &app{backend: backend, runtime: runtimeState{lifetime: lifetime,
		stopLifetime:  stopLifetime,
		localPassword: rand.Text(),
		workCtx:       workCtx,
		stopWork:      stopWork},
		connections: connectionsState{registryRequests: map[string]*requestState{},
			servers:      []serverConn{defaultLocalServer()},
			activeServer: localServerID,
			serverHealth: map[string]serverHealth{},
			serverFacts:  map[string]agent.Facts{},
			scopes:       map[string]*serverScope{},
			modelOff:     map[string]bool{}},
		preferences: preferencesState{home: home,
			keymap:              map[string]chord{},
			projectPins:         map[string]bool{},
			projectLinks:        map[string][]string{},
			unavailableProjects: map[string]bool{},
			notifyFinished:      true,
			notifyApprovals:     true},
		search: searchState{searches: make(chan searchRequest, 1)},
		layout: viewState{sidebarW: sidebarDefaultWidth,
			browserW:          browserDefaultWidth,
			selectedRail:      "home",
			browserCollapsed:  true,
			projectCollapsed:  map[string]bool{},
			projectChatsShown: map[string]bool{},
			expanded:          map[string]bool{},
			feed:              newFeed(),
			findShownMatch:    -1,
			settingsTab:       tabServers}}
	a.runtime.pickFolder = a.pickFolderDialog
	a.runtime.pickExport = a.pickSaveDialog
	a.runtime.pickAttach = a.pickAttachDialog
	a.runtime.probe = a.runProbe
	a.runtime.pair = a.runPair
	a.runtime.copy = a.copyToClipboard
	a.installFeatures(factories)
	a.connections.pairLinks = map[string]mintedLink{}
	return a
}

// scope is the active server's slice of the app state, made on first use,
// so the view reads sessions and models straight from the reading side.
func (a *app) scope() *serverScope { return a.scopeFor(a.connections.activeServer) }
func (a *app) scopeFor(id string) *serverScope {
	s := a.connections.scopes[id]
	if s == nil {
		s = &serverScope{pinned: map[string]bool{}, unread: map[string]bool{}, attention: map[string]bool{},
			archived: map[string]bool{}, queued: map[string][]string{}, drafts: map[string]string{}}
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
func (a *app) draftModelRef() agent.ModelRef {
	if a.conversation.draftModel.ID != "" {
		return a.conversation.draftModel
	}
	return a.defaultModelRef()
}

// defaultModelRef preserves the backend default, then offers the first enabled
// catalog choice. An empty choice delegates selection to the backend.
func (a *app) defaultModelRef() agent.ModelRef {
	def := a.scope().serverDefault
	if def.ID != "" && !a.connections.modelOff[modelKey(a.connections.activeServer, def.ProviderID, def.ID)] {
		return def
	}
	for _, model := range a.scope().models {
		if !a.modelDisabled(model) {
			return agent.ModelRef{ProviderID: model.ProviderID, ID: model.ID}
		}
	}
	return agent.ModelRef{}
}

// modelLess orders the picker: free models first and, inside each group,
// names alphabetically.
func modelLess(one, two agent.Model) bool {
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
	a.search.searchSelected = -1
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
	// Onevra's palette is the app's; installing it here gives every MyGo and
	// Onevra widget the same warm surfaces, terracotta accent and type scale
	// the hand-painted panes take from paletteFor.
	theme.Apply(c)
	for _, t := range a.layout.toasts {
		c.AddToast(t)
	}
	a.layout.toasts = nil
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
		c.Root().Background(p.ChromeWash)
	}

	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.topBar(c, p, headerH, bar.Left)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			if !a.layout.fullView {
				a.navRail(c, p)
			}
			// The sidebar and conversation ride on one card whose top
			// corners follow the window's, so the material frames them.
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).
				Background(p.Bg).Radius(cardRadius, cardRadius, 0, 0).Clip().Children(func() {
				if a.layout.settingsOpen {
					a.settingsPane(c, p, headerH)
					return
				}
				a.workspaceLayout(c, p, headerH)
			})
		})
	})
	a.attachModal(c, p)
	a.historyDialog(c, p)
	a.slashHelpDialog(c, p)
	a.shortcutsDialog(c, p)
	a.sessionDialogs(c, p)
	a.moduleDialogs(c)
	a.settingsDialogs(c, p)
	a.browseModal(c, p)
	a.commandDialog(c)
	// Onevra's toast look, below the title strip that carries the window
	// controls.
	components.Toasts(c, components.ToastOptions{
		Placement: components.ToastTopEnd,
		Inset:     headerH,
	})
}

// The workspace's panes, their opening sizes and the room each keeps when
// the window narrows or another pane is dragged wide. The preferred sizes
// stay in viewState, so a pane that had to give way returns to them when
// the room does.
const (
	sidebarDefaultWidth  float32 = 320
	sidebarMinWidth      float32 = 220
	sidebarMaxWidth      float32 = 560
	browserDefaultWidth  float32 = 440
	browserMinWidth      float32 = 320
	browserMaxWidth      float32 = 760
	conversationMinWidth float32 = 380
)

// workspaceLayout places the sidebar and the conversation side by side,
// with Onevra's divider between them. The conversation and the tools pane
// open on a second divider, so both sides of the window can be resized and
// both dividers answer arrow keys, Home and End, and a double click that
// restores the default width.
func (a *app) workspaceLayout(c *ui.Context, p palette, headerH float32) {
	body := func() { a.workspaceBody(c, p, headerH) }
	if a.layout.sidebarCollapsed {
		body()
		return
	}
	split := components.Split(c, &a.layout.sidebarW, func() {
		a.sidebarPane(c, p, headerH)
	}, body, components.SplitOptions{
		FirstMin:  sidebarMinWidth,
		SecondMin: conversationMinWidth,
		Default:   sidebarDefaultWidth,
	})
	split.Label("Resize sidebar").Fill()
	if split.Changed() {
		a.layout.sidebarW = min(max(a.layout.sidebarW, sidebarMinWidth), sidebarMaxWidth)
		a.saveState()
	}
}

// workspaceBody stacks the bottom terminal panel under the conversation
// when the panel is open, with a divider that resizes it. The chat side
// keeps the added room when the window grows, as the tools pane does.
func (a *app) workspaceBody(c *ui.Context, p palette, headerH float32) {
	main := func() { a.workspaceMain(c, p, headerH) }
	if bottom := a.bottom(); bottom != nil {
		bottom.Layout(c, headerH, main)
	} else {
		main()
	}
}

// workspaceMain is the conversation, and the tools pane beside it while
// that pane is open. The divider stores the chat column's width in the
// split; the app keeps the tools pane's width, so a wider window gives the
// conversation the added room instead of stretching the tools.
func (a *app) workspaceMain(c *ui.Context, p palette, headerH float32) {
	if a.layout.browserCollapsed {
		a.contentPane(c, p)
		return
	}
	windowW, _ := c.Size()
	avail := windowW
	if !a.layout.fullView {
		avail -= navRailWidth
	}
	if !a.layout.sidebarCollapsed {
		// The sidebar's own divider takes one DIP before this split.
		avail -= a.layout.sidebarW + 1
	}
	avail = max(avail, 2)
	contentW := avail - 1 - a.layout.browserW
	split := components.Split(c, &contentW, func() {
		a.contentPane(c, p)
	}, func() {
		a.browserPane(c, p, headerH)
	}, components.SplitOptions{
		FirstMin:  conversationMinWidth,
		SecondMin: browserMinWidth,
		Default:   max(0, avail-1-browserDefaultWidth),
	})
	split.Label("Resize tools").Fill()
	if split.Changed() {
		a.layout.browserW = min(max(avail-1-contentW, browserMinWidth), browserMaxWidth)
		a.saveState()
	}
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
	titleStart := float32(12)
	if !a.layout.fullView {
		titleStart += navRailWidth
	}
	if !a.layout.sidebarCollapsed {
		titleStart += a.layout.sidebarW
	}
	// Back and forward are 28 wide, the sidebar toggle 30, and four gaps
	// of the row's 2 stand between them and the title.
	titleGap := max(float32(8), titleStart-(barLeft+8+30+2*2))
	toolsLabel := "Show tools"
	if !a.layout.browserCollapsed {
		toolsLabel = "Hide tools"
	}
	fullLabel, fullIcon := "Enter full view", icExpand
	if a.layout.fullView {
		fullLabel, fullIcon = "Exit full view", icCollapse
	}

	ui.Row(c).FillWidth().Height(headerH).DragWindow().AlignItems(ui.Center).
		Padding(0, 12, 0, barLeft+8).Gap(2).Children(func() {

		label := "Hide sidebar"
		if a.layout.sidebarCollapsed {
			label = "Show sidebar"
		}
		a.navButton(c, p, label, icPanel, a.toggleSidebar, p.Text)

		ui.Box(c).Width(titleGap)
		// The title appears once the conversation has one, and names the
		// page while the settings are up. What the chat has spent rides
		// beside it, quiet for the free models the app runs by default.
		title := a.conversation.selected
		if a.layout.settingsOpen {
			title = "Settings"
		}
		if title != "" {
			ui.Text(c, title).FontSize(14.5).FontWeight(500).TextColor(p.Text).
				Shrink(1).MinWidth(0).SingleLine()
			if !a.layout.settingsOpen {
				if cost := a.spentOf(string(a.conversation.target.session)); cost > 0 {
					ui.Text(c, spentLine(cost)).FontSize(12.5).TextColor(p.Muted).
						MarginStart(8).Shrink(0).SingleLine()
				}
			}
			// The chat's own menu rides the title: rename, export what
			// the view holds, delete.
			if !a.layout.settingsOpen && string(a.conversation.target.session) != "" {
				a.chatMenuButton(c, p, title)
			}
		}
		if !a.layout.settingsOpen {
			if dir := a.chatDir(); dir != "" && string(a.conversation.target.session) != "" {
				a.moduleHeaders(c)
			}
		}
		ui.Spacer(c)

		a.navButton(c, p, fullLabel, fullIcon, a.toggleFullView, p.Text)
		a.navButton(c, p, toolsLabel, icPanelRight, a.toggleTools, p.Text)
	})
}

// chatMenuButton is the title's kebab: the conversation's own actions,
// the same three the sidebar row's kebab carries, with the export taking
// exactly what this view holds.
func (a *app) chatMenuButton(c *ui.Context, p palette, title string) {
	sid := string(a.conversation.target.session)
	c.Key("chat-menu:" + sid)
	kebab := ui.ButtonBase(c).Label("Chat options, "+title).Size(22, 22).Radius(6).Center().
		MarginStart(4).Shrink(0).Tooltip("Rename, export, archive or delete this chat")
	tint := p.Faint
	kebab.Menu(func(m *ui.Menu) {
		if m.Item("Rename…").Chosen() {
			a.requestRename(agent.Session{ID: sid, Title: title})
		}
		if m.Item("Copy transcript").Chosen() {
			a.copyTranscript()
		}
		if m.Item("Export transcript…").Chosen() {
			a.exportTranscript()
		}
		m.Separator()
		pinLabel := "Pin chat"
		if a.scope().pinned[sid] {
			pinLabel = "Unpin from Pinned"
		}
		if m.Item(pinLabel).Chosen() {
			a.togglePin(agent.Session{ID: sid, Title: title})
		}
		unreadLabel := "Mark as unread"
		if a.scope().unread[sid] {
			unreadLabel = "Mark as read"
		}
		if m.Item(unreadLabel).Chosen() {
			a.toggleSessionUnread(agent.Session{ID: sid, Title: title})
		}
		m.Separator()
		if m.Item(a.archiveLabel(sid)).Chosen() {
			a.toggleArchiveChat(agent.Session{ID: sid, Title: title})
		}
		m.Separator()
		if m.Item("Delete").Chosen() {
			a.confirmDelete(agent.Session{ID: sid, Title: title})
		}
	})
	if kebab.Hovered() {
		kebab.Background(p.Hover)
		tint = p.Muted
	}
	kebab.Children(func() { ui.Icon(c, icEllipsis).FontSize(13).TextColor(tint) })
}

func Run(backend agent.Backend, factories ...feature.Factory) {
	if len(os.Args) == 2 && os.Args[1] == "--reset-metadata" {
		path, err := metadataPath(backend.ID())
		if err == nil {
			err = resetMetadata(path)
		}
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Reset application metadata at %s", path)
		return
	}
	a := newApp(backend, factories...)
	mygo.App.OnQuit(a.closeWork)
	// A banner's click can be what launched this run, so the listening
	// starts before Run and the window catches up when it is ready.
	clickedNotices := make(chan string, 4)
	mygo.App.OnNotificationClick(func(id string) {
		select {
		case clickedNotices <- id:
		default:
		}
	})
	mygo.App.OnDidBecomeActive(func() {
		if a.runtime.clearNotices != nil {
			a.runtime.clearNotices()
		}
	})
	mygo.App.WhenReady(func() {
		// A fixed app directory is shared by plain, dev and packaged builds.
		var db *metadataStore
		path, err := metadataPath(backend.ID())
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
		a.runtime.post = win.Update
		a.runtime.menu = func(fn func()) { win.Update(fn) }
		a.runtime.setTitle = win.SetTitle
		mygo.App.SetMenu(appMenu(a))
		a.applyAppearance()
		if mygo.NotificationsSupported() {
			a.runtime.notify = showDesktopNotice
			a.runtime.clearNotices = mygo.ClearNotifications
		}
		go func() {
			for {
				select {
				case id := <-clickedNotices:
					win.Show()
					win.Update(func() { a.openNoticeChat(id) })
				case <-a.runtime.lifetime.Done():
					return
				}
			}
		}()
		a.runtime.background = func(work func() func()) {
			go func() { win.Update(work()) }()
		}
		win.OnClosed(a.closeWork)
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
		a.startModules()
		go searchChats(a.runtime.lifetime, a.search.searches, func(ctx context.Context, query searchRequest) ([]agent.Session, error) {
			client, err := backend.Client(query.config)
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
		// WADA_APPEARANCE=(system|light|dark) fixes the palette, and
		// WADA_TERMINAL=N opens the bottom panel with N shells before the
		// capture, N defaulting to one; WADA_TERMINAL_MAX=1 maximizes it.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				wait := 3 * time.Second
				if appearance, ok := os.LookupEnv("WADA_APPEARANCE"); ok && appearance != "" {
					win.Update(func() { a.setAppearance(appearance) })
					wait = 5 * time.Second // the new palette reaches the chrome
				}
				if tab, ok := os.LookupEnv("WADA_SETTINGS"); ok {
					if tab == "" || tab == "1" {
						tab = tabServers
					}
					win.Update(func() { a.openSettings(tab) })
					wait = 6 * time.Second // the catalog fills in behind the page
				}
				if os.Getenv("WADA_TERMINAL") != "" || os.Getenv("WADA_TERMINAL_MAX") != "" {
					wait = 8 * time.Second
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

// syncWindowTitle names the open chat in the window title, or the page when
// the settings are up. The window starts as plain Codex.
func (a *app) syncWindowTitle() {
	if a.runtime.setTitle == nil {
		return
	}
	title := "Codex"
	switch {
	case a.layout.settingsOpen:
		title = "Settings — Codex"
	case a.conversation.selected != "":
		title = a.conversation.selected + " — Codex"
	}
	a.runtime.setTitle(title)
}

// resetPaneWidths puts the sidebar, tools pane and terminal back to their
// default sizes, leaving full view first so the panes are there to size.
func (a *app) resetPaneWidths() {
	a.leaveFullView(false)
	a.layout.sidebarW = sidebarDefaultWidth
	a.layout.browserW = browserDefaultWidth
	if bottom := a.bottom(); bottom != nil {
		bottom.ResetSize()
	}
	a.saveState()
}

// notify queues one Onevra toast for the next frame. Callbacks from
// background work land here; MyGo's queue, timing and announcements own
// the rest, and a window that closed before the frame drops it.
func (a *app) notify(t ui.Toast) {
	a.layout.toasts = append(a.layout.toasts, t)
	if a.runtime.win != nil {
		a.runtime.win.Invalidate()
	}
}

// send appends the draft to the transcript as the user's own words and
// brings the latest block in view.
func (a *app) send() bool {
	text := strings.TrimSpace(a.conversation.draft)
	if text == "" {
		return false
	}
	// While the @-file menu answers the words, Enter takes its choice —
	// or waits for the search — instead of sending a half-typed word.
	if a.beforeFeatureSend() {
		return true
	}
	if a.runSlash(text) {
		return true
	}
	if command, ok := a.slashChoice(text); ok {
		a.executeSlash(command)
		return true
	}
	if !a.agentSend(text) {
		return false
	}
	key := a.draftKey()
	if a.conversation.target.isDraft() {
		a.freshTranscript()
	}
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindUser, Text: a.conversation.draft})
	a.conversation.draft = ""
	a.forgetDraft(key)
	a.resetComposerFeatures()
	a.closeAttach()
	a.layout.feed.ScrollToEnd()
	a.saveState()
	return true
}

// agentPill names the persona the next conversation is born under and
// picks among the chat-pickable ones the server offers ("plan" keeps its
// answers read-only). The panel hides while the server names none.
func (a *app) agentPill(c *ui.Context, p palette) {
	agents := a.scope().agents
	if len(agents) == 0 {
		return
	}
	chosen := a.conversation.agent
	name := agentTitle(agents, chosen)
	b := ui.ButtonBase(c).Label("Mode " + name).Height(28).Radius(999).PaddingX(10).
		Tooltip("How this chat approaches the work").
		Menu(a.agentMenu())
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Icon(c, icGitBranch).FontSize(14).TextColor(p.Muted)
		ui.Text(c, name).MarginStart(6).FontSize(13).FontWeight(500).SingleLine()
	})
}

// agentTitle words the persona a pill shows: the picked persona's name,
// or the server's opening one when the pick has not happened.
func agentTitle(agents []agent.Persona, picked string) string {
	for _, agent := range agents {
		if picked != "" && agent.ID == picked {
			return agent.Name
		}
	}
	return agents[0].Name
}

// agentSend queues one user turn for the runner, addressed to the open
// conversation or to a fresh one when none is open. The turn carries the
// app's default model, so a conversation born here starts on what the
// settings enabled.
func (a *app) agentSend(text string) bool {
	def := a.draftModelRef()
	queued := !a.conversation.target.isDraft() && a.conversation.transcript.Busy() != ""
	accepted := a.queueCommand(runner.Prompt{Session: a.conversation.target.session, Text: text, Directory: a.chatDir(), Agent: a.conversation.agent, Draft: a.conversation.target.revision, Model: agent.ModelRef{ProviderID: def.ProviderID, ID: def.ID, Variant: def.Variant}}) == runner.Accepted
	if accepted && queued {
		a.queueTurn(string(a.conversation.target.session), text)
	}
	return accepted
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
	// A folder linked into a project is already represented; adding it as
	// its own project row would split its chats out of the group.
	if owner := a.projectPrimaryFor(clean); owner != "" && !sameDir(owner, clean) {
		return clean, nil
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
	backend := a.backend
	runRequest(a, &st.request, func(ctx context.Context) ([]byte, error) {
		client, err := backend.Client(cfg.transport())
		ag, _ := client.(workspace.Client)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if ag == nil {
			return nil, fmt.Errorf("workspace files unavailable")
		}
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
	a.conversation.draft += quoteMarkup(abs, data)
	a.closeAttach()
}

// setAsk moves the shell policy of the chat's folder in the workspace
// config; the note lands in the transcript when the file refuses. A
// remote chat carries the rule over the server's own API, into the file
// it watches on its machine.
// requestRename opens the rename dialog for one conversation.
func (a *app) requestRename(s agent.Session) {
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
func (a *app) acceptUsage(m agent.Message) {
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
func (a *app) sessionModelOf(sid string) agent.ModelRef {
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
func (a *app) modelLabel(ref agent.ModelRef) (name string, variants []string) {
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
func (a *app) chooseModel(m agent.Model, variant string) {
	if a.conversation.target.isDraft() {
		a.layout.pickerOpen = false
		a.conversation.draftModel = agent.ModelRef{ProviderID: m.ProviderID, ID: m.ID, Variant: variant}
		return
	}
	if a.queueCommand(runner.SelectModel{Session: a.conversation.target.session, Model: agent.ModelRef{ProviderID: m.ProviderID, ID: m.ID, Variant: variant}}) == runner.Accepted {
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
func (a *app) confirmDelete(s agent.Session) {
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
func (a *app) adoptSession(s agent.Session) {
	label := s.Title
	if label == "" {
		label = "New chat"
	}
	a.conversation.selected = label
	a.layout.queueOpen = false
	a.stashDraft()
	a.conversation.target.selectSession(chat.SessionID(s.ID))
	a.restoreDraft(s.ID)
	a.clearUnread(s.ID)
	a.freshTranscript()
	// The pane's tools follow the folder on show, and another chat means
	// another folder.
	a.workspaceChanged()
	a.modulesReady()
	a.saveState()
	a.syncWindowTitle()
}

// settleBoot finishes the boot: the conversation list lands and the view
// settles onto the newest conversation, as its click would have; with
// none, the transcript starts clean instead of showing the mock.
func (a *app) settleBoot(ls []agent.Session, next string, lerr error) {
	// The new-chat composer is restored before the first conversation, so
	// adopting that conversation stashes these words instead of deleting
	// a draft the user has not seen yet.
	a.restoreDraft("")
	if lerr == nil {
		a.scope().sessions = ls
		a.scope().nextCursor = next
	}
	settled := false
	if lerr == nil && len(ls) > 0 {
		if listed := a.listedSessions(); len(listed) > 0 {
			a.openConversation(listed[0])
			settled = true
		}
	}
	if !settled {
		// No conversation to show, or only put-away ones: a clean draft
		// is less surprising than opening a chat the user archived.
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
	if dir != "" && a.projectPrimaryFor(dir) != "" {
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
	a.stashDraft()
	a.conversation.target.selectSession("")
	a.conversation.selected = ""
	a.restoreDraft("")
	a.conversation.draftProject = dir
	a.conversation.draftModel = agent.ModelRef{}
	a.conversation.commandError = ""
	a.layout.projectPickerOpen = false
	a.layout.queueOpen = false
	a.freshTranscript()
	a.workspaceChanged()
	a.saveState()
	a.syncWindowTitle()
}

// freshTranscript empties the transcript for another conversation,
// forgetting what was streaming, what tools ran and what find had found.
func (a *app) freshTranscript() {
	a.conversation.transcript.Reset()
	a.layout.feed = newFeed()
	a.layout.findOpen = false
	a.layout.findShownMatch = -1
	a.layout.findShownQuery = ""
}
