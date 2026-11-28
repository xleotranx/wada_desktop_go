package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/protocol"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "wada@example.com")
	runGit(t, dir, "config", "user.name", "Wada Test")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "init")
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestWorktreeIsolationIncludeDiffListAndRemove(t *testing.T) {
	repo := initTestRepo(t)
	outside := t.TempDir()
	secretOutside := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretOutside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Gitignored local env + .worktreeinclude with an invalid escape rule that must be ignored.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env.local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env.local"), []byte("LOCAL=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".worktreeinclude"), []byte("# comment\n.env.local\n../escape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore", ".worktreeinclude")
	runGit(t, repo, "commit", "-m", "add include")

	mgr := New(t.TempDir())
	wtA, err := mgr.Create(t.Context(), repo, "feature-a", "")
	if err != nil {
		t.Fatal(err)
	}
	wtB, err := mgr.Create(t.Context(), repo, "feature-b", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create(t.Context(), repo, "feature-c", "--detach"); err == nil {
		t.Fatal("a base ref that reads as an option was accepted")
	}

	if envBytes, err := os.ReadFile(filepath.Join(wtA.Path, ".env.local")); err != nil || string(envBytes) != "LOCAL=1\n" {
		t.Fatalf("expected .env.local copied into worktree A, got %q err=%v", string(envBytes), err)
	}

	if err := os.WriteFile(filepath.Join(wtA.Path, "main.go"), []byte("package main\n\nfunc main() { println(\"A\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtB.Path, "b_only.txt"), []byte("hello B\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	diffA, err := mgr.Diff(t.Context(), wtA.Path)
	if err != nil || len(diffA) != 1 || diffA[0].Path != "main.go" || diffA[0].Status != protocol.FileModified {
		t.Fatalf("diffA = %+v, err=%v", diffA, err)
	}
	diffB, err := mgr.Diff(t.Context(), wtB.Path)
	if err != nil || len(diffB) != 1 || diffB[0].Path != "b_only.txt" || diffB[0].Status != protocol.FileAdded || !strings.Contains(diffB[0].Patch, "+hello B") {
		t.Fatalf("diffB = %+v, err=%v", diffB, err)
	}

	list, err := mgr.List(t.Context(), repo)
	if err != nil || len(list) != 3 {
		t.Fatalf("expected 3 worktrees, got %+v, err=%v", list, err)
	}

	if err := mgr.Remove(t.Context(), repo, repo, true); err == nil {
		t.Fatal("expected main checkout removal to be refused")
	}
	if err := mgr.Remove(t.Context(), repo, wtA.Path, true); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Remove(t.Context(), repo, wtB.Path, true); err != nil {
		t.Fatal(err)
	}
	listAfter, err := mgr.List(t.Context(), repo)
	if err != nil || len(listAfter) != 1 {
		t.Fatalf("expected only main worktree after removals, got %+v, err=%v", listAfter, err)
	}
}

func TestDiffListsFilesInsideNewDirectories(t *testing.T) {
	repo := initTestRepo(t)
	pkg := filepath.Join(repo, "internal", "newpkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"a.go": "package newpkg\n", "b.go": "package newpkg\n\nvar B = 1\n"} {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	changes, err := New(t.TempDir()).Diff(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]protocol.FileChange{}
	for _, ch := range changes {
		got[ch.Path] = ch
	}
	if len(got) != 2 {
		t.Fatalf("changes = %+v, want the two new files", changes)
	}
	for _, path := range []string{"internal/newpkg/a.go", "internal/newpkg/b.go"} {
		ch, ok := got[path]
		if !ok || ch.Status != protocol.FileAdded || ch.Added == 0 || !strings.Contains(ch.Patch, "+package newpkg") {
			t.Fatalf("%s = %+v (present %v)", path, ch, ok)
		}
	}
}
