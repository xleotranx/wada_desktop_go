package notifications

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s2", Title: "Fix the build", Directory: "/w/app"}}}
	c, host := &plugintest.Chat{}, &kittest.Host{}
	p := &Plugin{}
	h := kerneltest.New(t, a, c, host, p)
	c.Open(agent.Session{ID: "s1", Title: "On show"})
	return h, p, c, host
}

func emit(h *kerneltest.Harness, evs ...agent.Event) {
	kernel.Emit(h, agent.Events, evs)
	h.Flush()
}

func TestNotifiesOnlyWhatTheUserIsNotLookingAt(t *testing.T) {
	h, _, _, host := start(t)
	emit(h, agent.Event{Kind: agent.TurnSucceeded, Session: "s1"})
	if len(host.Notices) != 0 {
		t.Fatalf("notified the conversation on show: %+v", host.Notices)
	}
	emit(h, agent.Event{Kind: agent.TurnSucceeded, Session: "s2"})
	h.WaitPosts(1) // the title is looked up
	want := shell.Notice{ID: "chat:s2", Title: "Fix the build", Subtitle: "app", Body: "The turn finished."}
	if len(host.Notices) != 1 || host.Notices[0] != want {
		t.Fatalf("notices %+v", host.Notices)
	}
	host.Background = true
	emit(h, agent.Event{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{Kind: agent.ToolShell, Action: "run_terminal_cmd"}},
		agent.Event{Kind: agent.TurnInterrupted, Session: "s2"})
	if len(host.Notices) != 2 || host.Notices[1].Title != "On show" ||
		host.Notices[1].Body != "Waiting for your approval to run a command." {
		t.Fatalf("notices %+v", host.Notices)
	}
}

func TestClickOpensTheConversation(t *testing.T) {
	h, _, c, _ := start(t)
	emit(h, agent.Event{Kind: agent.TurnFailed, Session: "s2"})
	h.WaitPosts(1)
	kernel.Emit(h, shell.NoticeClicked, "chat:s2")
	if c.Conv != (chat.Conversation{Session: "s2", Title: "Fix the build", Directory: "/w/app"}) {
		t.Fatalf("opened %+v", c.Conv)
	}
	kernel.Emit(h, shell.NoticeClicked, "other:x")
	if c.Conv.Session != "s2" {
		t.Fatalf("a foreign notice opened %+v", c.Conv)
	}
}

func TestSwitchesTurnNoticesOff(t *testing.T) {
	h, p, _, host := start(t)
	tt := ui.NewTester(p.section, 600, 300)
	tt.Frame()
	if err := tt.Click("Notify: When a turn finishes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if p.finished.Get() || !p.approvals.Get() {
		t.Fatalf("finished %v approvals %v", p.finished.Get(), p.approvals.Get())
	}
	emit(h, agent.Event{Kind: agent.TurnSucceeded, Session: "s2"})
	if len(host.Notices) != 0 {
		t.Fatalf("notices %+v", host.Notices)
	}
}

func TestNoticesWaitForTheWorkspaceRecord(t *testing.T) {
	a := plugintest.NewAgent()
	c, host := &plugintest.Chat{}, &kittest.Host{}
	ws := &plugintest.Workspace{Pending: true}
	h := kerneltest.New(t, a, c, host, ws, &Plugin{})
	emit(h, agent.Event{Kind: agent.TurnSucceeded, Session: "ours"}, agent.Event{Kind: agent.TurnSucceeded, Session: "cli"})
	if len(host.Notices) != 0 {
		t.Fatalf("notified before the record was read: %+v", host.Notices)
	}
	ws.Pending = false
	ws.Convs = []workspace.Conversation{{ID: "ours", Title: "Parser", Folder: "/w/app"}}
	kernel.Emit(h, workspace.Changed, struct{}{})
	h.Flush()
	if len(host.Notices) != 1 || host.Notices[0].ID != "chat:ours" {
		t.Fatalf("notices %+v", host.Notices)
	}
}
