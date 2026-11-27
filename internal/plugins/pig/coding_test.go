package pig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
