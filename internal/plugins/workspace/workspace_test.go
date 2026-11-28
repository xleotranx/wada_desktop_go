package workspace

import (
	"errors"
	"slices"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

type fixture struct {
	h      *kerneltest.Harness
	p      *Plugin
	a      *plugintest.Agent
	record *plugintest.Projects
	host   *kittest.Host
	chosen string
}

// start runs the plugin over a backend whose record has /w/app and the inbox
// /data/inbox, and lists sessions, then waits for it to load.
func start(t *testing.T, sessions ...agent.Session) *fixture {
	t.Helper()
	f := &fixture{a: plugintest.NewAgent(), host: &kittest.Host{},
		record: &plugintest.Projects{Dirs: []string{"/w/app"}, Inbox: "/data/inbox"}}
	f.a.Pages["|"] = agent.SessionPage{Sessions: sessions}
	clock := int64(1000)
	f.h = kerneltest.New(t, f.a.New, f.record.New, f.host.New, kerneltest.Keep(&f.p, func() *Plugin {
		return &Plugin{now: func() int64 { clock++; return clock }, choose: func() (string, error) { return f.chosen, nil }}
	}))
	f.until(t, "the record loads", f.p.Loaded)
	return f
}

// until lands what workers post until ok holds.
func (f *fixture) until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		f.h.Flush()
		if ok() {
			return
		}
		f.h.RunPosts()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s did not happen", what)
}

func ids(list []workspace.Conversation) []agent.SessionID {
	var out []agent.SessionID
	for _, c := range list {
		out = append(out, c.ID)
	}
	return out
}

func TestListsTheConversationsOfActiveWorkspaces(t *testing.T) {
	f := start(t,
		agent.Session{ID: "s1", Title: "Parser", Directory: "/w/app", Updated: 20},
		agent.Session{ID: "s2", Title: "Elsewhere", Directory: "/w/archived", Updated: 30},
		agent.Session{ID: "s3", Title: "Question", Directory: "/data/inbox", Updated: 10})
	if got := ids(f.p.Conversations()); !slices.Equal(got, []agent.SessionID{"s1", "s3"}) {
		t.Fatalf("conversations %q", got)
	}
	ps := f.p.Projects()
	if len(ps) != 2 || ps[0].Root != "/w/app" || !ps[1].Inbox {
		t.Fatalf("projects %+v, want app then the inbox", ps)
	}
	if w, ok := f.p.At(""); !ok || w.Directory != "/data/inbox" {
		t.Fatalf("At(\"\") = %+v %v, want the inbox", w, ok)
	}
	if c, _ := f.p.Lookup("s1"); c.Workspace != "wks:/w/app" || c.Directory != "/w/app" {
		t.Fatalf("s1 %+v", c)
	}
	for dir, want := range map[string]string{"/w/app": "app", "": "Inbox", "/data/inbox": "Inbox", "/w/gone": "gone"} {
		if got := f.p.Name(dir); got != want {
			t.Fatalf("Name(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestStartedConversationsJoinTheirWorkspace(t *testing.T) {
	f := start(t)
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "n1", Title: "New", Directory: "/w/app"})
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "n2"})
	if got := ids(f.p.Conversations()); !slices.Equal(got, []agent.SessionID{"n2", "n1"}) {
		t.Fatalf("conversations %q", got)
	}
	if c, _ := f.p.Lookup("n2"); c.Directory != "/data/inbox" {
		t.Fatalf("a chat without a folder is in %q, want the inbox", c.Directory)
	}
}

func TestEventsKeepTitlesAndOrder(t *testing.T) {
	f := start(t,
		agent.Session{ID: "s1", Title: "One", Directory: "/w/app", Updated: 20},
		agent.Session{ID: "s2", Title: "Two", Directory: "/w/app", Updated: 10})
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "s2"},
		{Kind: agent.SessionRenamed, Session: "s2", Title: "Renamed"}})
	if got := f.p.Conversations(); got[0].ID != "s2" || got[0].Title != "Renamed" {
		t.Fatalf("conversations %+v", got)
	}
	kernel.Emit(f.h, agent.Deleted, agent.SessionID("s1"))
	if _, ok := f.p.Lookup("s1"); ok {
		t.Fatal("a deleted conversation is listed")
	}
}

func TestAnotherClientsConversationIsReadOnce(t *testing.T) {
	f := start(t)
	before := f.a.Called("sessions")
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "cli", Title: "From the CLI", Directory: "/w/app"}}}
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "cli"}})
	f.until(t, "the CLI's conversation is listed", func() bool { _, ok := f.p.Lookup("cli"); return ok })
	// A side chat the backend never lists is not read for again and again.
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "side"}})
	f.until(t, "the side chat's read", func() bool { return f.a.Called("sessions") == before+2 })
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "side"}})
	f.h.Flush()
	if got := f.a.Called("sessions"); got != before+2 {
		t.Fatalf("sessions read %d more times, want 2: %q", got-before, f.a.Calls())
	}
}

func TestWorkspaceUpdatesShowAndHideConversations(t *testing.T) {
	f := start(t,
		agent.Session{ID: "s1", Directory: "/w/app"},
		agent.Session{ID: "s2", Directory: "/w/web"})
	web := agent.Project{ID: "prj:/w/web", Root: "/w/web", Name: "web"}
	kernel.Emit(f.h, agent.WorkspaceUpdated, agent.WorkspaceUpdate{Project: &web})
	kernel.Emit(f.h, agent.WorkspaceUpdated, agent.WorkspaceUpdate{
		Workspace: &agent.Workspace{ID: "wks:/w/web", Project: web.ID, Directory: "/w/web"}})
	if _, ok := f.p.Lookup("s2"); !ok {
		t.Fatal("a new workspace's conversation is not listed")
	}
	app := f.p.Projects()[0]
	app.Archived = 5
	kernel.Emit(f.h, agent.WorkspaceUpdated, agent.WorkspaceUpdate{Project: &app})
	if _, ok := f.p.Lookup("s1"); ok {
		t.Fatal("an archived project's conversation is listed")
	}
	if _, ok := f.p.At("/w/app"); ok {
		t.Fatal("an archived project's workspace is active")
	}
}

func TestACompleteListingDropsWhatIsGone(t *testing.T) {
	f := start(t, agent.Session{ID: "s1", Directory: "/w/app"}, agent.Session{ID: "s2", Directory: "/w/app"})
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s2", Directory: "/w/app"}}}
	f.a.State.Attempt = 2
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.until(t, "s1 is dropped", func() bool { _, ok := f.p.Lookup("s1"); return !ok })
	// A listing that stops early proves nothing.
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s2", Directory: "/w/app"}}, Next: "more"}
	f.a.Fail["Sessions"] = nil
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "s3", Directory: "/w/app"})
	f.a.Pages["more|"] = agent.SessionPage{Sessions: nil, Next: "more"}
	f.a.State.Attempt = 3
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.until(t, "the third sync", func() bool { return f.p.synced == 3 && !f.p.full.Running() })
	if _, ok := f.p.Lookup("s3"); !ok {
		t.Fatal("an incomplete listing dropped a conversation")
	}
}

func TestAFailedSyncIsTriedAgain(t *testing.T) {
	f := start(t)
	f.a.Fail["Sessions"] = errors.New("offline")
	f.a.State.Attempt = 2
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	f.until(t, "the failed sync", func() bool { return f.p.synced == 0 })
	f.a.Fail["Sessions"] = nil
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s1", Directory: "/w/app"}}}
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s1"}})
	f.until(t, "the retry", func() bool { _, ok := f.p.Lookup("s1"); return ok })
}

func TestAnotherOriginStartsOver(t *testing.T) {
	f := start(t, agent.Session{ID: "s1", Directory: "/w/app"})
	f.a.State = agent.Status{State: agent.Connecting, Attempt: 2, Origin: "other"}
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	if f.p.Loaded() || len(f.p.Conversations()) != 0 || len(f.p.Projects()) != 0 {
		t.Fatal("another origin shows the last one's record")
	}
}

func TestChooseProjectAddsItOrReportsTheFailure(t *testing.T) {
	f := start(t)
	f.chosen = "/w/new"
	var got []agent.Workspace
	f.p.ChooseProject(func(w agent.Workspace) { got = append(got, w) })
	f.until(t, "the project is added", func() bool { return len(got) == 1 })
	if got[0].Directory != "/w/new" {
		t.Fatalf("chosen %+v", got)
	}
	if _, ok := f.p.At("/w/new"); !ok {
		t.Fatal("the added project's workspace is not active")
	}
	f.record.Fail = map[string]error{"AddProject": errors.New("no")}
	f.p.ChooseProject(func(w agent.Workspace) { got = append(got, w) })
	f.until(t, "the failure is reported", func() bool { return len(f.host.Toasts) == 1 })
	if len(got) != 1 {
		t.Fatal("a failed add ran its callback")
	}
	f.chosen = "" // cancelled
	f.record.Fail = nil
	f.p.ChooseProject(func(w agent.Workspace) { got = append(got, w) })
	f.until(t, "the dialog closes", func() bool { return !f.p.picking.Running() })
	if len(got) != 1 || len(f.host.Toasts) != 1 {
		t.Fatalf("a cancelled dialog did something: %v %q", got, f.host.Toasts)
	}
}

func TestArchiveProjectHidesItOnceTheBackendHas(t *testing.T) {
	f := start(t, agent.Session{ID: "s1", Directory: "/w/app"})
	done := false
	f.p.ArchiveProject("prj:/w/app", func() { done = true })
	f.until(t, "the archive", func() bool { return done })
	if _, ok := f.p.Lookup("s1"); ok {
		t.Fatal("an archived project's conversation is listed")
	}
	f.record.Fail = map[string]error{"ArchiveProject": errors.New("no")}
	inbox := f.p.Projects()[0].ID
	done = false
	f.p.ArchiveProject(inbox, func() { done = true })
	f.until(t, "the failure is reported", func() bool { return len(f.host.Toasts) == 1 })
	if done || len(f.p.Projects()) != 1 {
		t.Fatal("a failed archive changed the record")
	}
}

func TestWithoutABackendRecordNothingIsListed(t *testing.T) {
	a := plugintest.NewAgent()
	a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s1", Directory: "/w/app"}}}
	var p *Plugin
	h := kerneltest.New(t, a.New, (&kittest.Host{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	for deadline := time.Now().Add(2 * time.Second); !p.Loaded() && time.Now().Before(deadline); {
		h.RunPosts()
		h.Flush()
		time.Sleep(time.Millisecond)
	}
	if !p.Loaded() || len(p.Conversations()) != 0 {
		t.Fatalf("loaded %v, conversations %+v", p.Loaded(), p.Conversations())
	}
}

func TestTheRecordCannotBeTurnedOff(t *testing.T) {
	f := start(t)
	if err := f.h.Disable("workspace"); err == nil {
		t.Fatal("the workspace record was disabled")
	}
}

func TestUpdatesDuringASyncOutliveIt(t *testing.T) {
	f := start(t, agent.Session{ID: "s1", Directory: "/w/app"})
	f.a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s1", Directory: "/w/app"}, {ID: "s2", Directory: "/w/new"}}}
	f.a.State.Attempt = 2
	kernel.Emit(f.h, agent.StatusChanged, f.a.State)
	if !f.p.full.Running() {
		t.Fatal("no sync runs")
	}
	// The listing added a project for /w/new; its announcement lands before the
	// sync's result, which read the record without it.
	added := agent.Project{ID: "prj:/w/new", Root: "/w/new", Name: "new"}
	kernel.Emit(f.h, agent.WorkspaceUpdated, agent.WorkspaceUpdate{Project: &added,
		Workspace: &agent.Workspace{ID: "wks:/w/new", Project: added.ID, Directory: "/w/new"}})
	f.until(t, "the sync", func() bool { return !f.p.full.Running() && f.p.synced == 2 })
	if _, ok := f.p.At("/w/new"); !ok {
		t.Fatal("the sync's result undid an update that landed while it ran")
	}
	if _, ok := f.p.Lookup("s2"); !ok {
		t.Fatal("the new project's conversation is not listed")
	}
}
