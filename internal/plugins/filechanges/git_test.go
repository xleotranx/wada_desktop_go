package filechanges

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v: %s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func repository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "--quiet")
	return dir
}

func TestGitChangesIncludeStagedUnstagedAndUntrackedFiles(t *testing.T) {
	dir := repository(t)
	odd := ":(glob)*\tline\nname.txt"
	for _, name := range []string{"mixed.txt", "deleted.txt", odd} {
		writeFile(t, dir, name, "before\n")
	}
	writeFile(t, dir, ".gitignore", "ignored.txt\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")
	// The working tree matches HEAD, but both staged and unstaged changes exist.
	writeFile(t, dir, "mixed.txt", "staged\n")
	testGit(t, dir, "add", "mixed.txt")
	writeFile(t, dir, "mixed.txt", "before\n")
	writeFile(t, dir, odd, "after\n")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "new.txt", "new\nsecond\n")
	writeFile(t, dir, "image.bin", "\x00\x01\x02")
	writeFile(t, dir, "ignored.txt", "ignored\n")
	before := testGit(t, dir, "status", "--porcelain=v1", "-z")
	changes, err := gitChanges(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]agent.FileChange{}
	for _, change := range changes {
		byPath[change.Path] = change
		if strings.ContainsRune(change.Patch, '\x00') {
			t.Fatal("diff contains a NUL field separator")
		}
	}
	if len(byPath) != 5 {
		t.Fatalf("files: %+v", changes)
	}
	mixed := byPath["mixed.txt"]
	if mixed.Added != 2 || mixed.Removed != 2 || !strings.Contains(mixed.Patch, "Staged changes") || !strings.Contains(mixed.Patch, "Unstaged changes") {
		t.Fatalf("mixed: %+v", mixed)
	}
	if change := byPath[odd]; change.Added != 1 || change.Removed != 1 || !strings.Contains(change.Patch, "+after") {
		t.Fatalf("literal path: %+v", change)
	}
	if change := byPath["deleted.txt"]; change.Status != agent.FileDeleted || change.Removed != 1 {
		t.Fatalf("deleted: %+v", change)
	}
	if change := byPath["new.txt"]; change.Status != agent.FileAdded || change.Added != 2 || !strings.Contains(change.Patch, "+second") {
		t.Fatalf("untracked: %+v", change)
	}
	if change := byPath["image.bin"]; change.Status != agent.FileAdded || change.Added != 0 || !strings.Contains(change.Patch, "Binary files") {
		t.Fatalf("binary: %+v", change)
	}
	if after := testGit(t, dir, "status", "--porcelain=v1", "-z"); after != before {
		t.Fatal("reading changes modified Git status")
	}
}

func TestGitChangesWorkBeforeTheFirstCommitAndFromASubfolder(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "staged.txt", "staged\n")
	testGit(t, dir, "add", "staged.txt")
	writeFile(t, dir, "untracked.txt", "untracked\n")
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	changes, err := gitChanges(context.Background(), subdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes: %+v", changes)
	}
	for _, change := range changes {
		if change.Status != agent.FileAdded || change.Added != 1 {
			t.Fatalf("change: %+v", change)
		}
	}
}

func TestGitChangesReportMissingFoldersAndRepositories(t *testing.T) {
	for _, dir := range []string{"", "relative/path", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := gitChanges(context.Background(), dir); err == nil {
			t.Fatalf("%q should not read the process's repository", dir)
		}
	}
	dir := repository(t)
	changes, err := gitChanges(context.Background(), dir)
	if err != nil || len(changes) != 0 {
		t.Fatalf("clean repo: %+v, %v", changes, err)
	}
}

func TestGitChangesHandleRenamesAndSymlinksWithoutRunningDrivers(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "old.txt", "before\n")
	writeFile(t, dir, ".gitattributes", "*.txt diff=custom\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")
	testGit(t, dir, "mv", "old.txt", "new.txt")
	outside := t.TempDir()
	writeFile(t, outside, "private.txt", "do not read the target\n")
	if err := os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(outside, "driver")
	marker := filepath.Join(outside, "ran")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"core.fsmonitor", "diff.external", "diff.custom.textconv"} {
		testGit(t, dir, "config", key, script)
	}
	index, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	// An ambient Git override must not redirect the app to another repository.
	t.Setenv("GIT_DIR", filepath.Join(repository(t), ".git"))
	changes, err := gitChanges(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes: %+v", changes)
	}
	for _, change := range changes {
		if strings.Contains(change.Patch, "do not read the target") {
			t.Fatal("followed an untracked symlink")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a configured Git driver ran")
	}
	after, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil || string(after) != string(index) {
		t.Fatal("read modified the Git index")
	}
}

func TestGitDiffPreviewIsBoundedButCountsAreComplete(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "large.txt", strings.Repeat("new content\n", 60000))
	changes, err := gitChanges(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Added != 60000 || len(changes[0].Patch) > gitOutputLimit+100 || !strings.Contains(changes[0].Patch, "Diff preview truncated") {
		t.Fatalf("large diff: files=%d, added=%d, preview bytes=%d", len(changes), changes[0].Added, len(changes[0].Patch))
	}
}

func TestGitChangesShowAnUnmergedFile(t *testing.T) {
	dir := repository(t)
	testGit(t, dir, "config", "user.name", "Test")
	testGit(t, dir, "config", "user.email", "test@example.com")
	writeFile(t, dir, "conflict.txt", "base\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "--quiet", "-m", "base")
	base := strings.TrimSpace(testGit(t, dir, "rev-parse", "HEAD"))
	testGit(t, dir, "checkout", "--quiet", "-b", "feature")
	writeFile(t, dir, "conflict.txt", "feature\n")
	testGit(t, dir, "commit", "--quiet", "-am", "feature")
	testGit(t, dir, "checkout", "--quiet", "--detach", base)
	writeFile(t, dir, "conflict.txt", "current\n")
	testGit(t, dir, "commit", "--quiet", "-am", "current")
	cmd := exec.Command("git", "-C", dir, "merge", "feature")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected a merge conflict")
	}

	changes, err := gitChanges(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "conflict.txt" || !strings.Contains(changes[0].Patch, "<<<<<<<") || strings.ContainsRune(changes[0].Patch, '\x00') {
		t.Fatalf("conflict diff: %+v", changes)
	}
}

func TestGitChangesCombineAStagedDeletionAndUntrackedReplacement(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "replaced.txt", "before\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")
	testGit(t, dir, "rm", "--quiet", "replaced.txt")
	writeFile(t, dir, "replaced.txt", "replacement\n")
	changes, err := gitChanges(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Status != agent.FileModified || changes[0].Added != 1 || changes[0].Removed != 1 || !strings.Contains(changes[0].Patch, "-before") || !strings.Contains(changes[0].Patch, "+replacement") {
		t.Fatalf("replacement: %+v", changes)
	}
}
