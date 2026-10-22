package tabs

import (
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

type fixture struct {
	h    *kerneltest.Harness
	p    *Plugin
	a    *plugintest.Agent
	ws   *plugintest.Workspace
	chat *plugintest.Chat
	host *plugintest.Host
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
	f := fixture{p: &Plugin{}, a: plugintest.NewAgent(), ws: ws, chat: &plugintest.Chat{}, host: &plugintest.Host{}}
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
	f.open("s2")
	f.want(t, "First", "Fourth", "[Second]")

	// Closing a background tab keeps the active one.
	f.p.Close(f.tab("First").ID)
	f.want(t, "Fourth", "[Second]")
	// Closing the active tab shows its left neighbor when it has no right one.
	f.p.Close(f.tab("Second").ID)
	f.want(t, "[Fourth]")
	if f.chat.Conv.Session != "s4" {
		t.Fatalf("chat shows %q", f.chat.Conv.Session)
	}
	// Closing the last tab leaves a new chat in its folder.
	f.p.Close(f.tab("Fourth").ID)
	f.want(t, "[New chat]")
	if f.chat.Conv != (chat.Conversation{Directory: "/w/app"}) {
		t.Fatalf("chat shows %+v", f.chat.Conv)
	}
	// Close Tab runs on the active tab.
	f.open("s3")
	f.chat.NewChat("")
	f.want(t, "Third", "[New chat]")
	i := slices.IndexFunc(kernel.Items(f.h, kernel.Commands), func(e kernel.Entry[kernel.Command]) bool { return e.ID == "tabs.close" })
	kernel.Invoke(f.h, kernel.Items(f.h, kernel.Commands)[i], func(cmd kernel.Command) { cmd.Run() })
	f.want(t, "[Third]")
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
	f.chat.NewChat("")
	f.open("s2")
	f.ws.Forget("s2")
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

func TestStripDrawsGroupsAndSelects(t *testing.T) {
	f := start(t, nil, record())
	f.open("s1")
	f.chat.NewChat("")
	f.open("s3")
	f.chat.NewChat("")
	f.open("s2")
	tt := ui.NewTester(f.p.strip, 1100, 50)
	tt.Frame()
	for _, want := range []string{"app", "web", "First", "Third", "Second"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Third"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "First", "[Third]", "Second")
	if err := tt.Click("Close First"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "[Third]", "Second")
	if err := tt.Click("New chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.want(t, "Third", "[New chat]", "Second")
	if f.tab("New chat").Folder != "/w/web" {
		t.Fatalf("new chat in %q", f.tab("New chat").Folder)
	}
}
