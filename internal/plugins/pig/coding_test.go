package pig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

func TestCodingIdentityAndBuiltinToolsEnabled(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	c, _ := e.get(s.ID)
	prompt := c.session.SystemPrompt()
	if strings.Contains(prompt, "Wada Cowork") || !strings.Contains(prompt, "Wada") || !strings.Contains(prompt, "software engineering") {
		t.Fatalf("unexpected coding system prompt: %q", prompt)
	}
	names := c.session.ActiveToolNames()
	for _, name := range []string{"read", "bash", "edit", "write"} {
		if !slices.Contains(names, name) {
			t.Fatalf("missing built-in coding tool %s: %v", name, names)
		}
	}
	c.turn = &turnState{}
	if result := e.toolHook(c)(t.Context(), "read", "read", json.RawMessage(`{"path":"main.go"}`)); result.Block {
		t.Fatalf("read was blocked in ask mode: %s", result.Reason)
	}
	if got := permissionFor(modeAsk, nil, toolKind("bash")); got != ask {
		t.Fatalf("bash permission in ask mode = %v, want ask", got)
	}
	if got := permissionFor(modeAsk, nil, toolKind("write")); got != ask {
		t.Fatalf("write permission in ask mode = %v, want ask", got)
	}
	c.turn = nil
}

func TestPerDirectoryMutationLockAllowsConcurrentWorkspaces(t *testing.T) {
	h, p, _ := startPig(t)
	dirA := t.TempDir()
	dirB := t.TempDir()
	sA := createChat(t, h, p, dirA)
	sB := createChat(t, h, p, dirB)
	e, _ := p.current()
	cA, _ := e.get(sA.ID)
	cB, _ := e.get(sB.ID)

	if e.mutationLock(dirA) != e.mutationLock(filepath.Join(dirA, ".")) {
		t.Fatal("normalized paths did not share a mutation lock")
	}
	if err := e.beginMutation(t.Context(), cA, "mA", "write", json.RawMessage(`{"path":"a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.beginMutation(t.Context(), cB, "mB", "write", json.RawMessage(`{"path":"b.go"}`)); err != nil {
		e.finishMutation(cA, "mA")
		t.Fatalf("distinct workspace blocked on unrelated mutation lock: %v", err)
	}
	e.finishMutation(cB, "mB")
	e.finishMutation(cA, "mA")
}

func TestFolderlessConversationUsesAnInboxInsteadOfTheUserHome(t *testing.T) {
	h, p, _ := startPig(t)
	s := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Create(ctx, agent.CreateSession{Title: "Daily work"})
	})
	if s.Directory == os.Getenv("HOME") || !strings.HasSuffix(s.Directory, filepath.Join("workspaces", "inbox")) {
		t.Fatalf("unsafe default folder: %s", s.Directory)
	}
	e, _ := p.current()
	c, _ := e.get(s.ID)
	if !strings.Contains(c.session.SystemPrompt(), "Wada") {
		t.Fatal("Inbox did not get Wada instructions")
	}
}

func TestFolderlessMentionsUseTheInboxRatherThanHome(t *testing.T) {
	h, p, _ := startPig(t)
	e, _ := p.current()
	inbox, err := e.workDirectory("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "note.md"), []byte("Inbox note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "private.md"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := call(t, h, func(ctx context.Context) ([]byte, error) { return p.Read(ctx, "", "note.md") })
	if string(data) != "Inbox note" {
		t.Fatal("read did not use Inbox")
	}
	files := call(t, h, func(ctx context.Context) ([]agent.FileEntry, error) { return p.Find(ctx, "", "", 20) })
	for _, file := range files {
		if file.Path == "private.md" {
			t.Fatal("folderless completion listed the home folder")
		}
	}
}

func TestBaselinesAreLazyOnDiskAndSurviveRestartDeleteRewind(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "main.go", "content": "package main\n// edited\n"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Updated")}},
	})
	done := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.TurnSucceeded {
				done = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Mode: modeFull, Text: "Edit main.go"})
	})
	waitFor(t, h, "edit turn", func() bool { return done })

	e, _ := p.current()
	c, _ := e.get(s.ID)
	if len(c.rec.Baselines) != 0 {
		t.Fatalf("baselines retained in RAM: %d", len(c.rec.Baselines))
	}
	if _, err := os.Stat(e.baselinePath(s.ID)); err != nil {
		t.Fatalf("baseline file missing: %v", err)
	}

	// Simulate legacy inline Baselines in metadata/<id>.json to verify migration on restart.
	c.mu.Lock()
	c.rec.Baselines = map[string]fileSnapshot{"legacy.txt": {Exists: true, Data: []byte("old\n")}}
	if err := e.saveLocked(c); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.mu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "legacy.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.Disable("pig"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("pig"); err != nil {
		t.Fatal(err)
	}
	service, _ := kernel.Use(h, agent.ServiceKey)
	p = service.(*Plugin)
	waitFor(t, h, "restart", func() bool { return p.Status().State == agent.Ready })

	e, _ = p.current()
	c, _ = e.get(s.ID)
	if len(c.rec.Baselines) != 0 {
		t.Fatalf("migrated baselines still kept in RAM: %d", len(c.rec.Baselines))
	}
	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes after restart & legacy migration, got %+v", changes)
	}

	// Rewind copies baselines independently to fork; deleting original preserves fork baselines.
	msgs := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	forkID := call(t, h, func(ctx context.Context) (agent.SessionID, error) { return p.Rewind(ctx, s.ID, msgs[0].ID) })
	if _, err := os.Stat(e.baselinePath(forkID)); err != nil {
		t.Fatalf("fork baseline missing: %v", err)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Delete(ctx, s.ID) })
	if _, err := os.Stat(e.baselinePath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted session baseline still exists: %v", err)
	}
	forkChanges := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, forkID) })
	if len(forkChanges) != 2 {
		t.Fatalf("fork lost baselines after original delete: %+v", forkChanges)
	}
}
