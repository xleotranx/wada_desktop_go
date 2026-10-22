package tabs

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

type fixture struct {
	h    *kerneltest.Harness
	p    *Plugin
	a    *plugintest.Agent
	ws   *plugintest.Workspace
	chat *plugintest.Chat
	host *kittest.Host
}

func record() *plugintest.Workspace {
	return &plugintest.Workspace{
		FolderSet: []workspace.Folder{{Path: "/w/app"}, {Path: "/w/web"}},
		Convs: []workspace.Conversation{
			{ID: "s1", Title: "First", Folder: "/w/app"},
			{ID: "s2", Title: "Second"},
			{ID: "s3", Title: "Third", Folder: "/w/web"},
			{ID: "s4", Title: "Fourth", Folder: "/w/app"},
		}}
}

func start(t *testing.T, store *kerneltest.Store, ws *plugintest.Workspace) fixture {
	t.Helper()
	f := fixture{p: &Plugin{}, a: plugintest.NewAgent(), ws: ws, chat: &plugintest.Chat{}, host: &kittest.Host{}}
	pages := &pagesPlugin{}
	f.h = kerneltest.Start(t, kerneltest.Config{Store: store}, f.a, f.ws, f.chat, f.host, pages, f.p)
	f.h.Flush()
	return f
}

// pagesPlugin contributes a page other than the chat, as Settings does.
type pagesPlugin struct{}

func (*pagesPlugin) Manifest() kernel.Manifest { return kernel.Manifest{ID: "pages"} }
func (*pagesPlugin) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, shell.Pages, "pages.settings", 100, shell.Page{Title: func() string { return "Settings" }})
}

func (f fixture) open(id agent.SessionID) {
	conv, _ := f.ws.Lookup(id)
	f.chat.Open(conv.Session())
	f.host.ShowPage(chat.PageID)
}

// summary is the strip as "title" per tab, the active one in brackets.
func (f fixture) summary() []string {
	var out []string
	for _, t := range f.p.Tabs() {
		s := t.Title
		if t.ID == f.p.active {
			s = "[" + s + "]"
		}
		out = append(out, s)
	}
	return out
}

func (f fixture) want(t *testing.T, want ...string) {
	t.Helper()
	if got := f.summary(); !slices.Equal(got, want) {
		t.Fatalf("tabs %q, want %q", got, want)
	}
}

func (f fixture) tab(title string) tabs.Tab {
	for _, t := range f.p.Tabs() {
		if t.Title == title {
			return t
		}
	}
	return tabs.Tab{}
}

func TestTabsFollowNavigation(t *testing.T) {
	f := start(t, nil, record())
	// The chat starts on a new chat, which is the first tab.
	f.want(t, "[New chat]")

	// Opening a conversation from the empty tab takes the tab over.
	f.open("s1")
	f.want(t, "[First]")

	// A new chat in the same folder opens beside it, and sending it turns
	// the tab into the conversation.
	f.chat.NewChat("/w/app")
	f.want(t, "First", "[New chat]")
	f.ws.Convs = append(f.ws.Convs, workspace.Conversation{ID: "s5", Title: "Fifth", Folder: "/w/app"})
	f.chat.Conv = chat.Conversation{Session: "s5", Directory: "/w/app"}
	kernel.Emit(f.h, chat.Changed, f.chat.Conv)
	f.want(t, "First", "[Fifth]")

	// Another folder's conversation starts its own group after this one.
	f.open("s3")
	f.want(t, "First", "Fifth", "[Third]")
	// A new chat moved to the first folder joins that group.
	f.chat.NewChat("/w/web")
	f.chat.SetDirectory("/w/app")
	f.want(t, "First", "Fifth", "[New chat]", "Third")

	// A page is a tab too, and selecting a conversation tab shows it again.
	f.host.ShowPage("pages.settings")
	f.want(t, "First", "Fifth", "New chat", "Third", "[Settings]")
	f.p.Select(f.tab("First").ID)
	f.want(t, "[First]", "Fifth", "New chat", "Third", "Settings")
	if f.chat.Conv.Session != "s1" || f.host.PageID != chat.PageID {
		t.Fatalf("selecting did not show the conversation: %+v on %q", f.chat.Conv, f.host.PageID)
	}
	if g := f.tab("First"); g.Folder != "/w/app" || g.Kind != tabs.Conversation {
		t.Fatalf("tab %+v", g)
	}
}

func TestTabsShowWhatConversationsAreDoing(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s2")
	f.want(t, "First", "[Second]")

	state := func(title string) tabs.State { return f.tab(title).State }
	emit := func(events ...agent.Event) {
		f.a.Emit(events...)
		f.h.Flush()
	}
	emit(agent.Event{Kind: agent.TurnStarted, Session: "s1"})
	if state("First") != tabs.Working {
		t.Fatalf("started: %v", state("First"))
	}
	emit(agent.Event{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{ID: "p1"}})
	if state("First") != tabs.Waiting {
		t.Fatalf("approval: %v", state("First"))
	}
	emit(agent.Event{Kind: agent.ToolStarted, Session: "s1", Tool: &agent.Tool{ID: "t1"}},
		agent.Event{Kind: agent.TurnSucceeded, Session: "s1"},
		agent.Event{Kind: agent.TurnSucceeded, Session: "s2"},
		agent.Event{Kind: agent.SessionRenamed, Session: "s2", Title: "Second, renamed"})
	if state("First") != tabs.Unread || state("Second, renamed") != tabs.Idle {
		t.Fatalf("finished in the background %v, on show %v", state("First"), state("Second, renamed"))
	}
	f.p.Select(f.tab("First").ID)
	if state("First") != tabs.Idle {
		t.Fatalf("seen: %v", state("First"))
	}
}

func TestClosingTabs(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s4")
	f.p.showSpace("/w/app")
	f.want(t, "First", "[Fourth]")

	// Closing a background tab keeps the active one.
	f.p.Close(f.tab("First").ID)
	f.want(t, "[Fourth]")
	// Closing a space's last tab leaves a new chat in its folder.
	f.p.Close(f.tab("Fourth").ID)
	f.want(t, "[New chat]")
	if f.chat.Conv != (chat.Conversation{Directory: "/w/app"}) {
		t.Fatalf("chat shows %+v", f.chat.Conv)
	}
	// Closing the active tab shows its left neighbor when it has no right one.
	f.open("s3")
	f.chat.NewChat("/w/web")
	f.want(t, "Third", "[New chat]")
	// Close Tab runs on the active tab.
	i := slices.IndexFunc(kernel.Items(f.h, kernel.Commands), func(e kernel.Entry[kernel.Command]) bool { return e.ID == "tabs.close" })
	kernel.Invoke(f.h, kernel.Items(f.h, kernel.Commands)[i], func(cmd kernel.Command) { cmd.Run() })
	f.want(t, "[Third]")
	if f.chat.Conv.Session != "s3" {
		t.Fatalf("chat shows %q", f.chat.Conv.Session)
	}
}

func TestMovingATabKeepsItInItsGroup(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s4")
	f.chat.NewChat("")
	f.open("s3")
	f.want(t, "First", "Fourth", "[Third]")
	f.p.Move(f.tab("Fourth").ID, 0)
	f.want(t, "Fourth", "First", "[Third]")
	// Past the group's end is its last place, not another group's.
	f.p.Move(f.tab("Fourth").ID, 9)
	f.want(t, "First", "Fourth", "[Third]")
}

func TestTabsComeBackAfterARestart(t *testing.T) {
	store := kerneltest.NewStore()
	f := start(t, store, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s3")
	f.chat.NewChat("")
	f.open("s2")
	f.p.Select(f.tab("Third").ID)
	f.want(t, "First", "[Third]", "Second")
	f.h.Close()

	// The record no longer lists Second; until it is read nothing opens.
	ws := record()
	ws.Convs = slices.DeleteFunc(ws.Convs, func(c workspace.Conversation) bool { return c.ID == "s2" })
	ws.Pending = true
	g := start(t, store, ws)
	if len(g.p.Tabs()) != 0 {
		t.Fatalf("restored before the record was read: %q", g.summary())
	}
	ws.Pending = false
	kernel.Emit(g.h, workspace.Changed, struct{}{})
	g.want(t, "First", "[Third]")
	if g.chat.Conv.Session != "s3" {
		t.Fatalf("chat shows %q", g.chat.Conv.Session)
	}
}

func TestForgottenConversationLosesItsTab(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s4")
	f.ws.Forget("s4")
	f.want(t, "[First]")
	if f.chat.Conv.Session != "s1" {
		t.Fatalf("chat shows %q", f.chat.Conv.Session)
	}
}

func TestConversationStartedElsewhereOpensInTheBackground(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s4", Directory: "/w/app"})
	f.want(t, "[First]", "Fourth")
}

func TestStripDrawsTheSpaceAndSelects(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s4")
	tt := ui.NewTester(f.p.strip, 1100, 50)
	tt.Frame()
	for _, want := range []string{"app", "First", "Fourth"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("First"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "[First]", "Fourth")
	if err := tt.Click("Close Fourth"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "[First]")
	if err := tt.Click("New chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "First", "[New chat]")
	if f.tab("New chat").Folder != "/w/app" {
		t.Fatalf("new chat in %q", f.tab("New chat").Folder)
	}
}

func TestUnsentTextStaysWithItsTab(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s2")
	f.chat.Draft = "for Second"
	f.p.Select(f.tab("First").ID)
	if f.chat.Draft != "" {
		t.Fatalf("Second's text shows in First: %q", f.chat.Draft)
	}
	f.chat.Draft = "for First"
	f.p.Select(f.tab("Second").ID)
	if f.chat.Draft != "for Second" {
		t.Fatalf("Second's text %q", f.chat.Draft)
	}

	// A new chat's text survives a conversation opened from elsewhere and a
	// page on the way back.
	f.chat.NewChat("/w/web")
	f.chat.Draft = "a new idea"
	f.open("s3")
	f.host.ShowPage("pages.settings")
	f.p.Select(f.tab("New chat").ID)
	if f.chat.Draft != "a new idea" {
		t.Fatalf("new chat's text %q", f.chat.Draft)
	}

	// A closed tab's text goes with it.
	f.p.Select(f.tab("First").ID)
	if f.chat.Draft != "for First" {
		t.Fatalf("First's text %q", f.chat.Draft)
	}
	f.p.Select(f.tab("Third").ID)
	f.p.Close(f.tab("First").ID)
	if _, kept := f.p.unsent[conversationID("s1")]; kept {
		t.Fatal("closed tab's text kept")
	}
}

func TestTabMenuRenamesForgetsAndDeletes(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("/w/app")
	f.open("s4")
	tt := ui.NewTester(f.p.strip, 1100, 50)
	tt.Frame()
	if err := tt.RightClick("First"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); !slices.Equal(got, []string{"Rename…", "Close Tab", "Remove from List", "-", "Delete…"}) {
		t.Fatalf("menu %q", got)
	}
	if err := tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.renaming.Session != "s1" || !f.p.renameOn {
		t.Fatalf("renaming %+v", f.p.renaming)
	}
	f.p.title = "  Better  "
	f.p.saveRename()
	f.h.Flush()
	if c, _ := f.ws.Lookup("s1"); c.Title != "Better" || f.a.Called("rename s1 Better") != 1 {
		t.Fatalf("rename: %q calls %q", c.Title, f.a.Calls())
	}
	f.want(t, "Better", "[Fourth]")

	f.p.deleting, f.p.deleteOn = f.tab("Fourth"), true
	f.p.confirmDelete()
	f.h.Flush()
	if _, ok := f.ws.Lookup("s4"); ok || f.a.Called("delete s4") != 1 {
		t.Fatalf("delete: %+v calls %q", f.ws.Convs, f.a.Calls())
	}
	f.want(t, "[Better]")

	f.a.Fail["Delete"] = errors.New("gone")
	f.p.deleting, f.p.deleteOn = f.tab("Better"), true
	f.p.confirmDelete()
	f.h.Flush()
	if len(f.host.Toasts) != 1 || f.tab("Better").ID == "" {
		t.Fatalf("failed delete: toasts %q", f.host.Toasts)
	}
	f.p.forget(f.tab("Better")) // "Remove from List"
	if _, ok := f.ws.Lookup("s1"); ok || f.a.Called("delete s1") != 1 {
		t.Fatalf("remove from list: %+v calls %q", f.ws.Convs, f.a.Calls())
	}
	f.want(t, "[New chat]")
}

func TestTheStripShowsOneSpaceAtATime(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s3")
	f.chat.NewChat("")
	f.open("s4")
	tt := ui.NewTester(f.p.strip, 1100, 400)
	tt.Frame()
	if !tt.HasText("First") || !tt.HasText("Fourth") || tt.HasText("Third") {
		t.Fatalf("the app space: %q", tt.Texts())
	}
	// A turn ending in another space marks the switcher.
	f.a.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "s3"})
	f.h.Flush()
	tt.Frame()
	if _, ok := tt.Find("Activity in another space"); !ok {
		t.Fatalf("no mark for the other space: %q", tt.Texts())
	}
	if err := tt.Click("Space app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Spaces", "web", "New Chat in app", "Open Folder…", "Remove “app”…"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from the switcher: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Switch to web"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a, _ := f.p.Active(); a.Title != "Third" || f.p.space != "/w/web" || !tt.HasText("Third") || tt.HasText("First") {
		t.Fatalf("after switching: active %q, strip %q", a.Title, tt.Texts())
	}
	// Coming back shows the tab shown there last.
	f.p.showSpace("/w/app")
	if a, _ := f.p.Active(); a.Title != "Fourth" {
		t.Fatalf("back in app: %q", a.Title)
	}
	// A new chat starts in the space on show; a space without tabs opens one.
	f.p.newChat()
	if f.chat.Conv.Session != "" || f.chat.Conv.Directory != "/w/app" {
		t.Fatalf("new chat %+v", f.chat.Conv)
	}
	f.p.showSpace("")
	if a, _ := f.p.Active(); a.Title != "Second" && a.Kind != tabs.Draft {
		t.Fatalf("the default folder's space: %+v", a)
	}
}

func TestClosingTheLastTabOfASpaceStaysInIt(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s3")
	f.p.Close(f.tab("Third").ID)
	if a, _ := f.p.Active(); a.Kind != tabs.Draft || a.Folder != "/w/web" || f.p.space != "/w/web" {
		t.Fatalf("after closing web's last tab: %+v in %q", a, f.p.space)
	}
}

func TestRemovingAFolderClosesItsTabs(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s3")
	f.p.removing, f.p.removeOn = "/w/app", true
	dt := ui.NewTester(f.p.dialogs, 500, 400)
	dt.Frame()
	if !dt.HasText("Its 2 conversations stay with the agent but are no longer listed here. Its tabs close. Nothing on disk changes.") {
		t.Fatalf("dialog %q", dt.Texts())
	}
	f.p.confirmRemove()
	f.h.Flush()
	if len(f.ws.FolderSet) != 1 || slices.ContainsFunc(f.p.Tabs(), func(t tabs.Tab) bool { return t.Folder == "/w/app" }) {
		t.Fatalf("folders %+v tabs %q", f.ws.FolderSet, f.summary())
	}
}

// runPages contributes a page while its scope lives, as a workflow run does.
type runPages struct{ ctx *kernel.Context }

func (*runPages) Manifest() kernel.Manifest       { return kernel.Manifest{ID: "runs"} }
func (r *runPages) Setup(c *kernel.Context) error { r.ctx = c; return nil }

func (r *runPages) add(id, title string) *kernel.Scope {
	s := r.ctx.NewScope()
	_ = kernel.Contribute(s, shell.Pages, id, 50, shell.Page{Title: func() string { return title }})
	return s
}

func TestAPageThatGoesAwayLosesItsTab(t *testing.T) {
	runs := &runPages{}
	f := fixture{p: &Plugin{}, a: plugintest.NewAgent(), ws: record(), chat: &plugintest.Chat{}, host: &kittest.Host{}}
	f.h = kerneltest.Start(t, kerneltest.Config{}, f.a, f.ws, f.chat, f.host, runs, f.p)
	f.h.Flush()
	f.open("s1")
	run := runs.add("runs.run/1", "add a flag")
	f.h.Flush()
	f.host.ShowPage("runs.run/1")
	f.want(t, "First", "[add a flag]")
	run.Dispose()
	f.h.Flush()
	f.want(t, "[First]")
	if f.host.PageID != chat.PageID {
		t.Fatalf("page %q", f.host.PageID)
	}
}

func TestTabsSettings(t *testing.T) {
	store := kerneltest.NewStore()
	f := start(t, store, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s3")
	tt := ui.NewTester(f.p.section, 700, 600)
	tt.Frame()
	for _, want := range []string{"Reopen tabs when Wada starts", "Project colors", "app", "web"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Color 4 for web"); err != nil {
		t.Fatal(err)
	}
	if f.p.colorIndex("/w/web") != 3 {
		t.Fatalf("web has color %d", f.p.colorIndex("/w/web"))
	}
	f.p.reopen.Set(false)
	f.h.Close()
	again := start(t, store, record())
	again.want(t, "[New chat]")
}
