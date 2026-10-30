package pig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugins/flows"
	"wada-desktop-go/internal/plugins/workflow"
	"wada-desktop-go/internal/plugintest"
)

func TestStructuredTurnThroughPinnedSDKAndCleanup(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("The plan."), ai.FauxToolCall(resultToolName, map[string]any{"status": "ready", "summary": "planned"}, nil)}, StopReason: "toolUse"}})
	var terminal *agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.TurnSucceeded {
				copy := ev
				terminal = &copy
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{ID: "plan-request", Session: s.ID, Text: "Do the task", Instructions: "Plan only.", Policy: &agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}, ResultSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"},"summary":{"type":"string"}},"required":["status","summary"]}`)})
	})
	waitFor(t, h, "structured turn", func() bool { return terminal != nil })
	if terminal.RequestID != "plan-request" || !strings.Contains(string(terminal.Result), "planned") {
		t.Fatalf("terminal %+v", terminal)
	}
	e, _ := p.current()
	c, _ := e.get(s.ID)
	c.turnMu.Lock()
	cleared := c.turn == nil
	c.turnMu.Unlock()
	if !cleared {
		t.Fatal("turn restrictions leaked")
	}
	for _, name := range c.session.ActiveToolNames() {
		if name == resultToolName {
			t.Fatal("result tool leaked into ordinary chat")
		}
	}
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Ordinary answer")}}})
	terminal = nil
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{ID: "ordinary-request", Session: s.ID, Text: "Why?"})
	})
	waitFor(t, h, "ordinary turn", func() bool { return terminal != nil })
	if len(terminal.Result) > 0 {
		t.Fatal("old structured result leaked")
	}
}

func TestTaskSnapshotIncludesExistingAndUntrackedFiles(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("preexisting edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, dir)
	base := call(t, h, func(ctx context.Context) (agent.Snapshot, error) { return p.Snapshot(ctx, s.ID) })
	if string(base["existing.go"]) != "preexisting edit\n" {
		t.Fatalf("baseline %v", base)
	}
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("task edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("new file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Diff(ctx, s.ID, base) })
	if len(changes) != 2 || !strings.Contains(changes[0].Patch, "preexisting edit") || changes[1].Status != agent.FileAdded {
		t.Fatalf("changes %+v", changes)
	}
}

func TestTurnApprovalCarriesRequestIdentity(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{"command": "pwd"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Checked.")}}})
	var approval *agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.ApprovalRequested {
				copy := ev
				approval = &copy
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{ID: "review-request", Session: s.ID, Mode: modeFull, Text: "Check", Policy: &agent.ToolPolicy{Allow: []agent.ToolKind{agent.ToolRead}, Deny: []agent.ToolKind{agent.ToolEdit}}})
	})
	waitFor(t, h, "review approval", func() bool { return approval != nil })
	if approval.RequestID != "review-request" {
		t.Fatalf("uncorrelated approval %+v", approval)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Abort(ctx, s.ID) })
}

func TestConversationalWorkflowThroughPiGWithReviewerApproval(t *testing.T) {
	// Resolve the native dependency before startPig isolates HOME and its cache.
	library, err := sqlite.LibraryPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYGO_SQLITE_LIBRARY", library)
	for _, proxy := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		t.Setenv(proxy, "http://127.0.0.1:1")
	}
	h, p, faux := startPig(t, (&plugintest.Chat{}).New, workflow.New, flows.New)
	s := createChat(t, h, p, t.TempDir())
	delivery, ok := kernel.Use(h, chat.DeliveryKey)
	if !ok {
		t.Fatal("delivery unavailable")
	}
	waitFor(t, h, "workflow history", func() bool { return delivery.Validate(chat.Draft{}) == nil })
	result := func(status, summary string, review bool) ai.FauxResponse {
		args := map[string]any{"status": status, "summary": summary}
		if review {
			args["findings"] = []any{}
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(summary), ai.FauxToolCall(resultToolName, args, nil)}, StopReason: "toolUse"}
	}
	faux.SetResponses([]ai.FauxResponseStep{
		result("ready", "Plan with project context", false), result("ready", "Implemented and checked", false),
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{"command": "pwd"}, nil)}, StopReason: "toolUse"},
		result("pass", "Independent review complete", true), result("ready", "Finished", false),
	})
	var approval *agent.Approval
	kernel.On(h, chat.Incoming, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Approval != nil {
				approval = ev.Approval
			}
		}
	})
	if err := delivery.Admit(chat.Draft{Session: s.ID, Directory: s.Directory, Model: s.Model, Mode: modeFull, Text: "Implement the fixture", Workflow: "flows.plan-code-review", Anchor: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, "reviewer approval in parent chat", func() bool { return approval != nil })
	if approval.Session == "" || approval.Session == s.ID {
		t.Fatalf("wrong permission owner %+v", approval)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.Reply(ctx, agent.Reply{Session: approval.Session, Approval: approval.ID, Decision: agent.Once})
	})
	waitFor(t, h, "workflow completion", func() bool { return !delivery.Busy(s.ID) && faux.State().CallCount() >= 5 })
	messages := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	users := 0
	for _, m := range messages {
		if m.Role == agent.RoleUser {
			users++
			if m.Text != "Implement the fixture" {
				t.Fatalf("internal prompt displayed: %q", m.Text)
			}
		}
	}
	if users != 1 {
		t.Fatalf("main user messages=%d", users)
	}
	sessions := call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != s.ID {
		t.Fatalf("top-level sessions %+v", sessions)
	}
}
