package pig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

func TestProjectTurnBlocksGeneralToolsEvenInFullAccess(t *testing.T) {
	c := &conversation{rec: record{Mode: modeFull}, allowed: map[string]bool{}}
	c.turn = &turnState{project: &agent.ProjectScope{Kind: "video", Directory: t.TempDir()}}
	e := &engine{ctx: context.Background()}
	for _, name := range []string{"bash", "read", "grep", "find", "ls", "write", "edit", "fetch", "codemode", "unknown"} {
		if got := e.toolHook(c)(t.Context(), name, name, json.RawMessage(`{}`)); !got.Block {
			t.Fatalf("project turn allowed %s", name)
		}
	}
	if got := e.toolHook(c)(t.Context(), "inspect", projectToolName, json.RawMessage(`{"action":"inspect"}`)); got.Block {
		t.Fatal("project inspection was blocked")
	}
	c.turn.policy = &agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}
	if got := e.toolHook(c)(t.Context(), "update", projectToolName, json.RawMessage(`{"action":"update"}`)); !got.Block {
		t.Fatal("project tool bypassed read-only review policy")
	}
}

func TestProjectToolThroughPinnedSDKAndOrdinaryToolRestoration(t *testing.T) {
	h, p, faux := startPig(t)
	root := t.TempDir()
	dir := filepath.Join(root, "videos", "one")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, root)
	calls := 0
	if err := kernel.Contribute(h, agent.ProjectTools, "app.video", 0, agent.ProjectTool{Kind: "video", Execute: func(_ context.Context, bound, action string, input json.RawMessage) (string, error) {
		if bound != dir || action != "inspect" {
			t.Errorf("wrong bound scope %s/%s", bound, action)
		}
		calls++
		return `{"scenes":2}`, nil
	}}); err != nil {
		t.Fatal(err)
	}
	e, _ := p.current()
	c, _ := e.get(s.ID)
	ordinary := slices.Clone(c.session.ActiveToolNames())
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(projectToolName, map[string]any{"action": "inspect"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Created the video.")}},
	})
	terminal := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.TurnSucceeded {
				terminal = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Mode: modeFull, Text: "Create two tips", Project: &agent.ProjectScope{Kind: "video", Directory: dir}})
	})
	waitFor(t, h, "project completion", func() bool { return terminal })
	if calls != 1 {
		t.Fatalf("project handler called %d times", calls)
	}
	if !slices.Equal(c.session.ActiveToolNames(), ordinary) {
		t.Fatalf("ordinary tools were not restored: %v", c.session.ActiveToolNames())
	}
	if _, err := (&projectTool{c: c, e: e}).Execute(t.Context(), "late", json.RawMessage(`{"action":"inspect"}`), nil); err == nil {
		t.Fatal("project tool available outside scoped turn")
	}
}

func TestProjectScopeCannotLeaveConversationFolder(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	call(t, h, func(ctx context.Context) (struct{}, error) {
		if _, err := p.Send(ctx, agent.Send{Session: s.ID, Text: "Create video", Project: &agent.ProjectScope{Kind: "video", Directory: t.TempDir()}}); err == nil {
			t.Error("outside project scope accepted")
		}
		return struct{}{}, nil
	})
}

func TestProjectMutationTrackingStaysInsideItsScope(t *testing.T) {
	h, p, _ := startPig(t)
	root := t.TempDir()
	dir := filepath.Join(root, "videos", "one")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "index.html")
	if err := os.WriteFile(file, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This sibling would exceed the tracker file limit if the project mutation
	// scanned the full conversation folder instead of the bound video.
	sibling, err := os.Create(filepath.Join(root, "unrelated.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sibling.Truncate(9 << 20); err != nil {
		t.Fatal(err)
	}
	if err := sibling.Close(); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, root)
	e, _ := p.current()
	c, _ := e.get(s.ID)
	c.turn = &turnState{project: &agent.ProjectScope{Kind: "video", Directory: dir}}
	if err := e.beginMutation(t.Context(), c, "update", projectToolName, json.RawMessage(`{"action":"update"}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.finishMutation(c, "update")
	if c.rec.ChangesError != "" {
		t.Fatal(c.rec.ChangesError)
	}
	if len(c.rec.Baselines) != 1 || string(c.rec.Baselines["videos/one/index.html"].Data) != "before" {
		t.Fatalf("incorrect baseline %v", c.rec.Baselines)
	}
	c.turn = nil
}
