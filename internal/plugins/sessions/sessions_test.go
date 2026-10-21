package sessions

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Workspace, *plugintest.Chat, *plugintest.Host) {
	t.Helper()
	a := plugintest.NewAgent()
	ws := &plugintest.Workspace{
		FolderSet: []workspace.Folder{{Path: "/w/app"}, {Path: "/w/web", Collapsed: true}, {Path: "/w/zed"}},
		Convs: []workspace.Conversation{
			{ID: "s1", Title: "First", Folder: "/w/app"},
			{ID: "s2", Title: "Second"},
			{ID: "s3", Title: "Third", Folder: "/w/web"},
			{ID: "s4", Title: "Fourth", Folder: "/w/app"},
		}}
	chat, host := &plugintest.Chat{}, &plugintest.Host{}
	p := &Plugin{}
	h := kerneltest.New(t, a, ws, chat, host, p)
	h.Flush()
	return h, p, a, ws, chat, host
}

func keys(items []item) []any {
	var out []any
	for _, it := range items {
		out = append(out, it.key())
	}
	return out
}

func TestConversationsAreGroupedByFolder(t *testing.T) {
	_, p, _, _, chat, _ := start(t)
	want := []any{"folder:/w/app", agent.SessionID("s1"), agent.SessionID("s4"), "folder:/w/web", "folder:/w/zed",
		"empty:/w/zed", "other", agent.SessionID("s2")}
	if got := keys(p.items()); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if !tt.HasText("app") || !tt.HasText("First") || tt.HasText("Third") || !tt.HasText("No conversations") {
		t.Fatalf("rows %q", tt.Texts())
	}
	if err := tt.Click("Second"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if chat.Conv.Session != "s2" {
		t.Fatalf("opened %+v", chat.Conv)
	}
}

func TestFolderHeaderCollapsesAndStartsChats(t *testing.T) {
	_, p, _, ws, chat, _ := start(t)
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if err := tt.Click("web"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ws.FolderSet[1].Collapsed || !tt.HasText("Third") {
		t.Fatalf("expanding: %+v rows %q", ws.FolderSet[1], tt.Texts())
	}
	if err := tt.Click("New chat in zed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if chat.Conv.Session != "" || chat.Conv.Directory != "/w/zed" {
		t.Fatalf("new chat %+v", chat.Conv)
	}
}

func TestWithoutFoldersTheListIsFlat(t *testing.T) {
	_, p, _, ws, _, _ := start(t)
	ws.FolderSet = nil
	items := p.items()
	if len(items) != 4 || slices.ContainsFunc(items, func(it item) bool { return it.kind != itemChat || it.nested }) {
		t.Fatalf("rows %v", keys(items))
	}
}

func TestSearchFiltersTitlesAcrossFolders(t *testing.T) {
	_, p, _, _, _, _ := start(t)
	p.query = "  THI "
	want := []any{"folder:/w/web", agent.SessionID("s3")} // collapsed, yet shown
	if got := keys(p.items()); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	p.query = "nothing like it"
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if !tt.HasText("No matches") {
		t.Fatalf("rows %q", tt.Texts())
	}
}

func TestOpenFolderStartsAChatThere(t *testing.T) {
	_, p, _, ws, chat, _ := start(t)
	ws.Chosen = "/w/new"
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if err := tt.Click("Open folder…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if chat.Conv.Directory != "/w/new" || !slices.ContainsFunc(ws.FolderSet, func(f workspace.Folder) bool { return f.Path == "/w/new" }) {
		t.Fatalf("conv %+v folders %+v", chat.Conv, ws.FolderSet)
	}
}

func TestFinishedTurnElsewhereMarksUnread(t *testing.T) {
	h, p, _, ws, chat, _ := start(t)
	chat.Open(agent.Session{ID: "s1"})
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.TurnSucceeded, Session: "s1"},
		{Kind: agent.TurnSucceeded, Session: "s2"},
		{Kind: agent.TurnFailed, Session: "cli"}, // not started in this app
	})
	h.Flush()
	if p.unread["s1"] || !p.unread["s2"] || p.unread["cli"] {
		t.Fatalf("unread %v", p.unread)
	}
	p.open(ws.Convs[1])
	if p.unread["s2"] {
		t.Fatal("opening did not clear the mark")
	}
}

func TestRenameAndDelete(t *testing.T) {
	h, p, a, ws, chat, host := start(t)
	chat.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	p.startRename(ws.Convs[0])
	p.title = "  Better  "
	p.saveRename()
	h.Flush()
	if ws.Convs[0].Title != "Better" || a.Called("rename s1 Better") != 1 {
		t.Fatalf("rename: %q calls %q", ws.Convs[0].Title, a.Calls())
	}
	p.startDelete(ws.Convs[0])
	p.confirmDelete()
	h.Flush()
	if _, ok := ws.Lookup("s1"); ok || chat.Conv.Session != "" || chat.Conv.Directory != "/w/app" {
		t.Fatalf("delete: %+v conv %+v", ws.Convs, chat.Conv)
	}
	a.Fail["Delete"] = errors.New("gone")
	p.startDelete(ws.Convs[0])
	p.confirmDelete()
	h.Flush()
	if len(host.Toasts) != 1 || !slices.ContainsFunc(ws.Convs, func(c workspace.Conversation) bool { return c.ID == "s2" }) {
		t.Fatalf("failed delete: toasts %q list %+v", host.Toasts, ws.Convs)
	}
	p.forget(ws.Convs[0]) // "Remove from List"
	if _, ok := ws.Lookup("s2"); ok || a.Called("delete s2") != 1 {
		t.Fatalf("remove from list: %+v calls %q", ws.Convs, a.Calls())
	}
}

func TestRemovingAFolderLeavesItsChat(t *testing.T) {
	_, p, _, ws, chat, _ := start(t)
	chat.NewChat("/w/app")
	p.startRemove("/w/app")
	tt := ui.NewTester(p.dialogs, 500, 400)
	tt.Frame()
	if !tt.HasText("Its 2 conversations stay with the agent but are no longer listed here. Nothing on disk changes.") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	p.confirmRemove()
	if len(ws.FolderSet) != 2 || len(ws.Convs) != 2 || chat.Conv.Directory != "" {
		t.Fatalf("folders %+v convs %+v conv %+v", ws.FolderSet, ws.Convs, chat.Conv)
	}
}

func TestConversationActionsRemainKeyboardAccessible(t *testing.T) {
	_, p, _, _, _, _ := start(t)
	tt := ui.NewTester(p.view, 300, 500)
	for range 16 {
		if tt.Focused("Options for First") && !tt.Focused("First") {
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Options for First") || tt.Focused("First") {
		t.Fatal("conversation actions are not keyboard accessible")
	}
	tt.Key(0, ui.KeySpace)
	if err := tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.renaming == nil || p.renaming.ID != "s1" {
		t.Fatal("rename action did not open")
	}
}

func TestTheListFollowsTheRecord(t *testing.T) {
	_, p, _, ws, _, _ := start(t)
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	ws.Retitle("s1", "Renamed elsewhere")
	tt.Frame()
	if !tt.HasText("Renamed elsewhere") || tt.HasText("First") {
		t.Fatalf("rows %q", tt.Texts())
	}
}
