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

func TestCoworkIdentityAndToolsetReplaceCodingDefaults(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	c, _ := e.get(s.ID)
	prompt := c.session.SystemPrompt()
	for _, phrase := range []string{"Wada Cowork", "everyday work", "read_document", "calculate", "Do not assume a task is software development"} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("missing cowork behavior %q", phrase)
		}
	}
	names := c.session.ActiveToolNames()
	for _, name := range []string{"read_document", "write_document", "edit_document", "list_files", "search_documents", "read_url", "calculate"} {
		if !slices.Contains(names, name) {
			t.Fatalf("missing %s: %v", name, names)
		}
	}
	for _, name := range []string{"bash", "read", "write", "edit", "grep", "find", "ls", "codemode"} {
		if slices.Contains(names, name) {
			t.Fatalf("coding tool exposed: %s", name)
		}
		c.turn = &turnState{}
		if result := e.toolHook(c)(t.Context(), name, name, json.RawMessage(`{}`)); !result.Block {
			t.Fatalf("blocked tool can bypass the hook: %s", name)
		}
	}
	c.turn = nil
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
	if !strings.Contains(c.session.SystemPrompt(), "Wada Cowork") {
		t.Fatal("Inbox did not get cowork instructions")
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
