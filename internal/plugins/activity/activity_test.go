package activity

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Stored["s1"] = []agent.Message{
		{ID: "u1", Role: agent.RoleUser, Text: "test it"},
		{ID: "a1", Role: agent.RoleAssistant, Parts: []agent.Part{
			{Kind: agent.PartTool, Tool: &agent.Tool{ID: "t1", Kind: agent.ToolShell, Command: "go build ./...", Output: []string{"ok\n"}}},
			{Kind: agent.PartTool, Tool: &agent.Tool{ID: "t2", Kind: agent.ToolShell, Command: "go test ./...", Exit: 1,
				Output: []string{"--- FAIL: TestX\n", "FAIL\n"}}},
			{Kind: agent.PartTool, Tool: &agent.Tool{ID: "t3", Kind: agent.ToolRead, Name: "read"}},
			{Kind: agent.PartText, Text: "One test fails."},
		}},
	}
	a.Changed["s1"] = []agent.FileChange{
		{Path: "cmd/main.go", Added: 3, Removed: 1, Patch: "--- a/cmd/main.go\n+++ b/cmd/main.go\n@@ -1,2 +1,4 @@\n package main\n-var x = 1\n+var x = 2\n"},
		{Path: "README.md", Status: agent.FileAdded, Added: 12},
	}
	chat := &plugintest.Chat{}
	p := &Plugin{}
	h := kerneltest.New(t, a, chat, p)
	h.Flush()
	return h, p, a, chat
}

func panel(p *Plugin) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(300).Children(func() {
			if len(p.changes) > 0 {
				p.changesView(c)
			}
			if p.last != nil {
				p.commandView(c)
			}
		})
	}, 300, 900)
}

func TestOpeningAConversationReadsItsChangesAndLastCommand(t *testing.T) {
	h, p, _, chat := start(t)
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	if p.last == nil || p.last.Command != "go test ./..." || len(p.changes) != 2 {
		t.Fatalf("last %+v changes %+v", p.last, p.changes)
	}
	tt := panel(p)
	tt.Frame()
	for _, want := range []string{"2 files", "+15", "−1", "main.go", "cmd", "README.md", "go test ./...", "--- FAIL: TestX", "exit 1"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("cmd/main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+var x = 2") || !tt.HasText("@@ -1,2 +1,4 @@") || tt.HasText("+++ b/cmd/main.go") {
		t.Fatalf("diff %q", tt.Texts())
	}
	chat.NewChat("")
	h.Flush()
	if p.last != nil || p.changes != nil {
		t.Fatal("the activity outlived its conversation")
	}
}

func TestEventsBringTheLatestCommandAndTurnsReadChanges(t *testing.T) {
	h, p, a, chat := start(t)
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	reads := a.Called("changes s1")
	// ToolFinished need not say its kind; ToolStarted did.
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.ToolStarted, Session: "s1", Tool: &agent.Tool{ID: "t9", Kind: agent.ToolShell}},
		{Kind: agent.ToolFinished, Session: "s1", Tool: &agent.Tool{ID: "t9", Command: "make", Output: []string{"done"}}},
		{Kind: agent.ToolFinished, Session: "other", Tool: &agent.Tool{ID: "t8", Kind: agent.ToolShell, Command: "ls"}},
	})
	if p.last == nil || p.last.Command != "make" {
		t.Fatalf("last %+v", p.last)
	}
	a.Changed["s1"] = a.Changed["s1"][:1]
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s1"}})
	h.Flush()
	if a.Called("changes s1") != reads+1 || !slices.EqualFunc(p.changes, a.Changed["s1"], func(x, y agent.FileChange) bool { return x.Path == y.Path }) {
		t.Fatalf("changes %+v after %d reads", p.changes, a.Called("changes s1"))
	}
}

func TestActivityIsReadAgainOnceTheBackendConnects(t *testing.T) {
	h, p, a, chat := start(t)
	a.Fail["Messages"] = errors.New("the agent is not connected")
	a.Fail["Changes"] = errors.New("the agent is not connected")
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	if p.last != nil || p.changes != nil {
		t.Fatal("activity from failed reads")
	}
	delete(a.Fail, "Messages")
	delete(a.Fail, "Changes")
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	if p.last == nil || len(p.changes) != 2 {
		t.Fatalf("last %+v changes %+v", p.last, p.changes)
	}
}
