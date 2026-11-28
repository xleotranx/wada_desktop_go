package worktree

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SanitizeSlug makes a name fit a branch or a folder: lower case letters,
// digits and single dashes, at most 32 bytes.
func SanitizeSlug(name string) string { return sanitizeSlug(name) }

// RenameBranch renames a branch no remote has yet; one pushed keeps its name.
func (m *Manager) RenameBranch(ctx context.Context, dir, from, to string) error {
	if from == "" || to == "" || strings.HasPrefix(to, "-") {
		return errors.New("worktree: a branch to rename and its new name are required")
	}
	if _, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", from+"@{upstream}"); err == nil {
		return fmt.Errorf("worktree: %s is pushed; it keeps its name", from)
	}
	_, err := gitOut(ctx, dir, "branch", "-m", from, to)
	return err
}

// BranchExists reports a local branch of dir's repository.
func (m *Manager) BranchExists(ctx context.Context, dir, name string) bool {
	_, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// WorkingChanges is what a commit of everything in dir would hold: the
// status, then the diff of tracked files against HEAD.
func (m *Manager) WorkingChanges(ctx context.Context, dir string) (string, error) {
	status, err := gitOut(ctx, dir, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) == "" {
		return "", nil
	}
	diff, _ := gitOut(ctx, dir, "diff", "HEAD")
	return status + "\n" + diff, nil
}

// Commit commits everything in dir, untracked files included, and returns
// the commit's hash.
func (m *Manager) Commit(ctx context.Context, dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("worktree: a commit message is required")
	}
	if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, dir, "commit", "--quiet", "-m", message); err != nil {
		return "", err
	}
	sha, err := gitOut(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(sha), err
}

// History is what a pull request from dir's branch would hold: the commits
// since base and the files they change.
func (m *Manager) History(ctx context.Context, dir, base string) (string, error) {
	if strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("invalid ref %q", base)
	}
	log, err := gitOut(ctx, dir, "log", "--format=%s%n%n%b%n---", base+"..HEAD")
	if err != nil {
		return "", err
	}
	stat, _ := gitOut(ctx, dir, "diff", "--stat", base+"...HEAD")
	return log + "\n" + stat, nil
}
