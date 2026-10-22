package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/egoist/mygo/ui"
)

type fixture struct {
	h    *kerneltest.Harness
	p    *Plugin
	a    *plugintest.Agent
	chat *plugintest.Chat
	host *plugintest.Host
}

// start runs the plugin over the database in dir and waits for it to load.
func start(t *testing.T, dir string) fixture {
	t.Helper()
	f := fixture{a: plugintest.NewAgent(), chat: &plugintest.Chat{}, host: &plugintest.Host{Data: dir}}
	clock := int64(1000)
	f.p = &Plugin{now: func() int64 { clock++; return clock }, choose: func() (string, error) { return "", nil }}
	f.h = kerneltest.New(t, f.a, f.chat, f.host, f.p)
	// The record is read on a worker, which posts it back.
	for deadline := time.Now().Add(2 * time.Second); dir != "" && !f.p.Loaded() && time.Now().Before(deadline); {
		f.h.RunPosts()
		time.Sleep(time.Millisecond)
	}
	f.h.Flush()
	if !f.p.Loaded() {
		t.Fatal("the record did not load")
	}
	return f
}

func ids(list []workspace.Conversation) []agent.SessionID {
	var out []agent.SessionID
	for _, c := range list {
		out = append(out, c.ID)
	}
	return out
}

func paths(list []workspace.Folder) []string {
	var out []string
	for _, f := range list {
		out = append(out, f.Path)
	}
	return out
}

func TestStartedConversationsAreRecordedAndKept(t *testing.T) {
	dir := t.TempDir()
	f := start(t, dir)
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s1", Directory: "/w/app/", Title: "", Model: agent.ModelRef{Provider: "p", ID: "m"}})
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s2"})
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s3", Directory: "relative"})
	if err := f.p.AddFolder("/w/Bin"); err != nil {
		t.Fatal(err)
	}
	if err := f.p.AddFolder("nope"); err == nil {
		t.Fatal("a relative folder was added")
	}
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.SessionRenamed, Session: "s1", Title: "Parser"}})
	if got := ids(f.p.Conversations()); !slices.Equal(got, []agent.SessionID{"s3", "s2", "s1"}) {
		t.Fatalf("conversations %v", got) // newest first; a rename is not activity
	}
	f.h.Close() // the worker writes what is queued before it stops

	g := start(t, dir)
	if got := paths(g.p.Folders()); !slices.Equal(got, []string{"/w/app", "/w/Bin"}) {
		t.Fatalf("folders %v", got)
	}
	c, ok := g.p.Lookup("s1")
	if !ok || c.Folder != "/w/app" || c.Title != "Parser" || c.Model.ID != "m" || c.Created == 0 {
		t.Fatalf("s1 %+v %v", c, ok)
	}
	if c, _ := g.p.Lookup("s3"); c.Folder != "" { // the backend resolves a relative folder
		t.Fatalf("s3 %+v", c)
	}
}

func TestOnlyTheCurrentOriginIsListed(t *testing.T) {
	f := start(t, "")
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s1"})
	changes := 0
	kernel.On(f.h, workspace.Changed, func(struct{}) { changes++ })
	f.a.State.Origin = "elsewhere"
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	if len(f.p.Conversations()) != 0 || changes != 1 {
		t.Fatalf("other origin lists %v, %d changes", ids(f.p.Conversations()), changes)
	}
	if _, ok := f.p.Lookup("s1"); ok {
		t.Fatal("another origin's conversation was found")
	}
	f.a.State.Origin = "fake"
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	if got := ids(f.p.Conversations()); !slices.Equal(got, []agent.SessionID{"s1"}) {
		t.Fatalf("back home %v", got)
	}
}

func TestActivityOrdersAndSyncTakesTitles(t *testing.T) {
	f := start(t, "")
	for _, id := range []agent.SessionID{"s1", "s2", "s3"} {
		kernel.Emit(f.h, chat.Started, chat.Conversation{Session: id})
	}
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "cli", Title: "Not ours"}, {ID: "s1", Title: "Named by the agent", Updated: 5}}}
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "s1"}, {Kind: agent.TurnSucceeded, Session: "s1"}})
	f.h.Flush()
	if got := ids(f.p.Conversations()); !slices.Equal(got, []agent.SessionID{"s1", "s3", "s2"}) {
		t.Fatalf("order %v", got)
	}
	if c, _ := f.p.Lookup("s1"); c.Title != "Named by the agent" || c.Updated <= 5 {
		t.Fatalf("s1 %+v", c)
	}
	if _, ok := f.p.Lookup("cli"); ok {
		t.Fatal("a conversation started elsewhere was recorded")
	}
	// A turn of a conversation started elsewhere reads nothing.
	before := f.a.Called("sessions")
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "cli"}})
	f.h.Flush()
	if f.a.Called("sessions") != before {
		t.Fatal("a stranger's turn synced")
	}
}

func TestANewConnectionSyncsThroughPages(t *testing.T) {
	f := start(t, "")
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "old"})
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "gone"})
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "x"}}, Next: "c2"}
	f.a.Pages["c2|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "old", Title: "Found"}}}
	f.a.State.Attempt = 2
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.h.Flush()
	if c, _ := f.p.Lookup("old"); c.Title != "Found" {
		t.Fatalf("old %+v, calls %q", c, f.a.Calls())
	}
	// The backend no longer lists it, but that doesn't prove it is gone.
	if _, ok := f.p.Lookup("gone"); !ok {
		t.Fatal("an unlisted conversation was dropped")
	}
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.h.Flush()
	if got := f.a.Called("sessions"); got != 2 {
		t.Fatalf("the same connection synced again: %q", f.a.Calls())
	}
}

func TestRemovingAFolderForgetsItsConversations(t *testing.T) {
	dir := t.TempDir()
	f := start(t, dir)
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s1", Directory: "/w/app"})
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s2", Directory: "/w/web"})
	f.p.RemoveFolder("/w/app")
	f.p.Forget("s2")
	f.h.Close()
	g := start(t, dir)
	if got := paths(g.p.Folders()); !slices.Equal(got, []string{"/w/web"}) || len(g.p.Conversations()) != 0 {
		t.Fatalf("folders %v conversations %v", got, ids(g.p.Conversations()))
	}
}

func TestADatabaseOfAnotherSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "workspace.db"), sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(ctx, "PRAGMA user_version = 9"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	f := start(t, dir)
	if len(f.host.Toasts) != 1 || f.host.Toasts[0] != "Folders and conversations won't be saved" {
		t.Fatalf("toasts %q", f.host.Toasts)
	}
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s1"})
	if len(f.p.Conversations()) != 1 {
		t.Fatal("the record does not work in memory")
	}
}

func TestChooseFolderAddsItAndReportsFailures(t *testing.T) {
	f := start(t, "")
	f.p.choose = func() (string, error) { return "/w/picked/", nil }
	var got []string
	f.p.ChooseFolder(func(path string) { got = append(got, path) })
	f.h.Flush()
	if !slices.Equal(got, []string{"/w/picked"}) || !slices.Equal(paths(f.p.Folders()), []string{"/w/picked"}) {
		t.Fatalf("chosen %v folders %v", got, paths(f.p.Folders()))
	}
	f.p.choose = func() (string, error) { return "", errors.New("no window") }
	f.p.ChooseFolder(func(path string) { got = append(got, path) })
	f.h.Flush()
	if len(got) != 1 || len(f.host.Toasts) != 1 {
		t.Fatalf("chosen %v toasts %q", got, f.host.Toasts)
	}
}

func TestComposerChipSetsTheNewChatsFolder(t *testing.T) {
	f := start(t, "")
	_ = f.p.AddFolder("/w/app")
	f.chat.NewChat("")
	tt := ui.NewTester(f.p.chip, 400, 300)
	tt.Frame()
	if !tt.HasText("No folder") {
		t.Fatalf("chip %q", tt.Texts())
	}
	f.p.pick("/w/app")
	tt.Frame()
	if f.chat.Conv.Directory != "/w/app" || !tt.HasText("app") {
		t.Fatalf("conv %+v chip %q", f.chat.Conv, tt.Texts())
	}
	f.p.choose = func() (string, error) { return "/w/other", nil }
	f.p.pick(chooseFolder)
	f.h.Flush()
	if f.chat.Conv.Directory != "/w/other" {
		t.Fatalf("conv %+v", f.chat.Conv)
	}
	f.p.pick(noFolder)
	f.chat.Open(agent.Session{ID: "s1"})
	tt.Frame()
	if f.chat.Conv.Directory != "" || tt.HasText("No folder") {
		t.Fatalf("a stored conversation shows the chip: %q", tt.Texts())
	}
}

func TestSyncStopsBeforeTheRecordAndRetriesAfterAFailure(t *testing.T) {
	f := start(t, "")
	clock := int64(100_000_000)
	f.p.now = func() int64 { clock++; return clock }
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "gone"})
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "x", Updated: clock}, {ID: "y", Updated: 1}}, Next: "c2"}
	f.a.Pages["c2|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "z", Updated: 1}}}
	f.a.State.Attempt = 2
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.h.Flush()
	if got := f.a.Called("sessions"); got != 1 {
		t.Fatalf("read past activity older than the record: %q", f.a.Calls())
	}

	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s1"})
	f.a.Fail["Sessions"] = errors.New("timeout")
	f.a.State.Attempt = 3
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.h.Flush()
	delete(f.a.Fail, "Sessions")
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s1", Title: "Named", Updated: clock}}}
	before := f.a.Called("sessions")
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s1"}})
	f.h.Flush()
	if c, _ := f.p.Lookup("s1"); c.Title != "Named" || f.a.Called("sessions") != before+2 {
		t.Fatalf("s1 %+v calls %q", c, f.a.Calls())
	}
}

func TestTheRecordCannotBeTurnedOff(t *testing.T) {
	f := start(t, t.TempDir())
	if err := f.h.Disable("workspace"); err == nil {
		t.Fatal("the workspace record was disabled")
	}
}
