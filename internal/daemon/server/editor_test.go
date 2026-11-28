package server

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/protocol"
)

func TestWorkspaceEditorScopesReadsAndRejectsStaleSaves(t *testing.T) {
	dataDir := t.TempDir()
	startTestServer(t, dataDir)
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root := t.TempDir()
	name := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(name, []byte("first\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := c.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	id := added.Workspace.ID
	paths, err := c.FindWorkspaceFiles(t.Context(), id, "notes")
	if err != nil || !slices.Equal(paths, []string{"notes.txt"}) {
		t.Fatalf("paths = %v, %v", paths, err)
	}
	file, err := c.OpenWorkspaceFile(t.Context(), id, "notes.txt")
	if err != nil || file.Text != "first\n" || file.Revision == "" {
		t.Fatalf("open = %+v, %v", file, err)
	}
	for _, filename := range []string{"../notes.txt", ".git/config", name} {
		if _, err := c.OpenWorkspaceFile(t.Context(), id, filename); !isCode(err, protocol.ErrCodeInvalid) {
			t.Fatalf("open %q: %v", filename, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(outside, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err == nil {
		if _, err := c.OpenWorkspaceFile(t.Context(), id, "linked.txt"); !isCode(err, protocol.ErrCodeInvalid) {
			t.Fatalf("open link: %v", err)
		}
	}
	saved, err := c.SaveWorkspaceFile(t.Context(), id, "notes.txt", "second\n", file.Revision)
	if err != nil || saved.Text != "second\n" || saved.Revision == file.Revision {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	if _, err := c.SaveWorkspaceFile(t.Context(), id, "notes.txt", "stale", file.Revision); !isCode(err, protocol.ErrCodeConflict) {
		t.Fatalf("stale save = %v", err)
	}
	if disk, err := os.ReadFile(name); err != nil || string(disk) != "second\r\n" {
		t.Fatalf("disk = %q, %v", disk, err)
	}
	if err := c.ArchiveProject(t.Context(), added.Project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveWorkspaceFile(t.Context(), id, "notes.txt", "archived", saved.Revision); !isCode(err, protocol.ErrCodeNotFound) {
		t.Fatalf("saving into an archived workspace: %v", err)
	}
}
