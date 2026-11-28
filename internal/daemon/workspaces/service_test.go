package workspaces

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo makes a repository on main with app/main.go and a branch "feature".
func repo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "wada@example.com")
	git(t, dir, "config", "user.name", "Wada Test")
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "init")
	git(t, dir, "branch", "feature")
	return dir
}

func service(t *testing.T) *Service {
	t.Helper()
	data := t.TempDir()
	return NewService(open(t, data, nil), worktree.New(data))
}

func TestWorktreeWorkspacesOfASubproject(t *testing.T) {
	s, root := service(t), repo(t)
	p, _, err := s.AddProject(t.Context(), filepath.Join(root, "app"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree, Slug: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Directory != filepath.Join(w.WorktreeRoot, "app") || !w.Owned || w.BaseRef != "main" || w.Branch == "" {
		t.Fatalf("workspace %+v", w)
	}
	if _, err := os.Stat(filepath.Join(w.Directory, "main.go")); err != nil {
		t.Fatal(err)
	}
	// A commit no remote has is work archiving would lose.
	if err := os.WriteFile(filepath.Join(w.Directory, "new.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changes, err := s.Changes(t.Context(), w.ID)
	if err != nil || len(changes) != 1 || changes[0].Path != "new.go" {
		t.Fatalf("changes %+v %v", changes, err)
	}
	git(t, w.Directory, "add", ".")
	git(t, w.Directory, "commit", "-m", "work")
	var pending *PendingError
	if err := s.Archive(t.Context(), w.ID, false); !errors.As(err, &pending) || pending.Commits != 1 || pending.Files != 0 {
		t.Fatalf("archive = %v, want one unpushed commit", err)
	}
	if err := s.Archive(t.Context(), w.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.WorktreeRoot); !os.IsNotExist(err) {
		t.Fatal("the worktree was not removed")
	}
	back, err := s.Restore(t.Context(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(back.Directory, "new.go")); err != nil {
		t.Fatalf("the restored worktree lost its branch's commit: %v", err)
	}
}

func TestCheckoutBranchAndExistingWorktrees(t *testing.T) {
	s, root := service(t), repo(t)
	p, _, err := s.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree,
		Mode: protocol.StartCheckoutBranch, From: "feature"})
	if err != nil || w.Branch != "feature" {
		t.Fatalf("checkout = %+v %v", w, err)
	}
	if _, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree,
		Mode: protocol.StartCheckoutBranch, From: "feature"}); err == nil {
		t.Fatal("checked out a branch a worktree already has")
	}
	// A worktree made outside wadad becomes a workspace it doesn't own.
	outside := filepath.Join(t.TempDir(), "mine")
	git(t, root, "worktree", "add", "-b", "mine", outside)
	e, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationExisting, Path: outside})
	if err != nil || e.Owned || e.Branch != "mine" {
		t.Fatalf("existing = %+v %v", e, err)
	}
	if _, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationExisting, Path: outside}); err == nil {
		t.Fatal("two workspaces share a directory")
	}
	if err := s.Archive(t.Context(), e.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("archiving removed a worktree wadad didn't make")
	}
	if _, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationExisting, Path: t.TempDir()}); err == nil {
		t.Fatal("a folder that is no worktree became a workspace")
	}
	project, _ := s.Active(root)
	if err := s.Archive(t.Context(), project.ID, false); err == nil {
		t.Fatal("archived a project's own directory")
	}
	refs, err := s.Refs(t.Context(), p.ID)
	if err != nil || refs.Default != "main" || len(refs.Branches) < 3 || len(refs.Worktrees) != 3 {
		t.Fatalf("refs %+v %v", refs, err)
	}
}
