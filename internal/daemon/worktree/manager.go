// Package worktree manages isolated Git worktrees, .worktreeinclude file copies,
// per-worktree diffs, and repo setup trust records for wadad.
package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"wada-desktop-go/internal/protocol"
)

const maxPatchBytes = 256 << 10

// Manager creates, lists, diffs, and removes Git worktrees under dataDir/worktrees.
type Manager struct {
	dataDir string
	mu      sync.Mutex
	forge   *Forge
}

// New creates a Git worktree manager storing worktrees inside dataDir.
func New(dataDir string) *Manager {
	return &Manager{dataDir: filepath.Clean(dataDir)}
}

// RepoRoot resolves the top-level Git repository root containing dir.
func (m *Manager) RepoRoot(ctx context.Context, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("worktree: directory is required")
	}
	out, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("worktree: not a git repository (%s): %w", dir, err)
	}
	root := strings.TrimSpace(out)
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		return resolved, nil
	}
	return filepath.Clean(root), nil
}

// List returns all worktrees belonging to dir's repository.
func (m *Manager) List(ctx context.Context, dir string) ([]protocol.WorktreeInfo, error) {
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	raw, err := gitOut(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []protocol.WorktreeInfo
	blocks := strings.Split(strings.TrimSpace(raw), "\n\n")
	for i, blk := range blocks {
		if strings.TrimSpace(blk) == "" {
			continue
		}
		var info protocol.WorktreeInfo
		info.RepoRoot = root
		info.IsMain = i == 0
		for _, line := range strings.Split(blk, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				p := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
				if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
					p = resolved
				}
				info.Path = p
			case strings.HasPrefix(line, "HEAD "):
				info.HeadCommit = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
			case strings.HasPrefix(line, "branch "):
				br := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
				info.Branch = strings.TrimPrefix(br, "refs/heads/")
			}
		}
		if info.Path != "" {
			if st, serr := gitOut(ctx, info.Path, "status", "--porcelain"); serr == nil && strings.TrimSpace(st) != "" {
				info.Dirty = true
			}
			result = append(result, info)
		}
	}
	return result, nil
}

// Create creates a new isolated Git worktree and branch for dir's repository,
// applying any valid .worktreeinclude rules from repoRoot.
func (m *Manager) Create(ctx context.Context, dir, name, baseRef string) (protocol.WorktreeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return protocol.WorktreeInfo{}, err
	}
	if strings.TrimSpace(baseRef) == "" {
		baseRef = "HEAD"
	}
	slug := sanitizeSlug(name)
	suffix := randomHex(2)
	wtSlug := slug + "-" + suffix

	sum := sha256.Sum256([]byte(root))
	repoKey := hex.EncodeToString(sum[:6])
	dest := filepath.Join(m.dataDir, "worktrees", repoKey, wtSlug)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return protocol.WorktreeInfo{}, err
	}

	branch := "wada/" + wtSlug
	if _, err := gitOut(ctx, root, "worktree", "add", "-b", branch, dest, baseRef); err != nil {
		return protocol.WorktreeInfo{}, fmt.Errorf("worktree create failed: %w", err)
	}

	if resolved, rerr := filepath.EvalSymlinks(dest); rerr == nil {
		dest = resolved
	}
	if err := applyWorktreeInclude(root, dest); err != nil {
		return protocol.WorktreeInfo{}, fmt.Errorf("worktree created at %s, include copy failed: %w", dest, err)
	}

	head, _ := gitOut(ctx, dest, "rev-parse", "HEAD")
	return protocol.WorktreeInfo{
		RepoRoot:   root,
		Path:       dest,
		Branch:     branch,
		HeadCommit: strings.TrimSpace(head),
	}, nil
}

// Remove deletes a secondary worktree created for dir's repository.
// It refuses to delete the main checkout.
func (m *Manager) Remove(ctx context.Context, dir, wtPath string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(wtPath) == "" {
		return errors.New("worktree: path is required")
	}
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return err
	}
	target := filepath.Clean(wtPath)
	if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
		target = resolved
	}
	if target == root {
		return errors.New("worktree: refusing to remove main checkout")
	}

	list, err := m.List(ctx, root)
	if err != nil {
		return err
	}
	allowed := false
	for _, wt := range list {
		if wt.Path == target {
			if wt.IsMain {
				return errors.New("worktree: refusing to remove main checkout")
			}
			allowed = true
			break
		}
	}
	if !allowed {
		wtRoot := filepath.Join(m.dataDir, "worktrees")
		rel, rerr := filepath.Rel(wtRoot, target)
		if rerr != nil || !filepath.IsLocal(rel) {
			return errors.New("worktree: path is not a managed secondary worktree")
		}
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, target)
	if _, err := gitOut(ctx, root, args...); err != nil {
		return fmt.Errorf("worktree remove failed: %w", err)
	}
	return nil
}

// Diff returns uncommitted file changes (modified, added, deleted, untracked) inside dir.
func (m *Manager) Diff(ctx context.Context, dir string) ([]protocol.FileChange, error) {
	// --untracked-files=all lists the files inside a new directory, not just the directory.
	statusOut, err := gitOut(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if statusOut == "" {
		return nil, nil
	}

	hasHEAD := true
	if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "HEAD"); err != nil {
		hasHEAD = false
	}

	entries := strings.Split(strings.TrimSuffix(statusOut, "\x00"), "\x00")
	var changes []protocol.FileChange
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		xy := entry[:2]
		rel := filepath.ToSlash(entry[3:])
		// Rename/copy entries have an additional NUL-terminated original path right after.
		if xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C' {
			if i+1 < len(entries) {
				i++
			}
		}

		if xy == "??" {
			patch, added := untrackedPatch(dir, rel)
			changes = append(changes, protocol.FileChange{
				Path:   rel,
				Status: protocol.FileAdded,
				Added:  added,
				Patch:  patch,
			})
			continue
		}

		var status protocol.FileStatus
		switch {
		case xy[0] == 'D' || xy[1] == 'D':
			status = protocol.FileDeleted
		case xy[0] == 'A' || xy[1] == 'A':
			status = protocol.FileAdded
		default:
			status = protocol.FileModified
		}

		var patch string
		if hasHEAD {
			patch, _ = gitOut(ctx, dir, "diff", "HEAD", "--", rel)
		} else {
			patch, _ = gitOut(ctx, dir, "diff", "--cached", "--", rel)
		}
		if len(patch) > maxPatchBytes {
			patch = patch[:maxPatchBytes] + "\n... [truncated]"
		}
		added, removed := countPatchLines(patch)
		changes = append(changes, protocol.FileChange{
			Path:    rel,
			Status:  status,
			Added:   added,
			Removed: removed,
			Patch:   patch,
		})
	}
	return changes, nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

func sanitizeSlug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "task"
	}
	return s
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func applyWorktreeInclude(repoRoot, dest string) error {
	incPath := filepath.Join(repoRoot, ".worktreeinclude")
	raw, err := os.ReadFile(incPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var patterns []string
	for _, line := range strings.Split(string(raw), "\n") {
		p := strings.TrimSpace(line)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		p = strings.TrimPrefix(filepath.ToSlash(p), "./")
		if strings.HasPrefix(p, "/") || !filepath.IsLocal(filepath.FromSlash(strings.TrimSuffix(p, "/"))) {
			continue
		}
		patterns = append(patterns, p)
	}
	if len(patterns) == 0 {
		return nil
	}

	return filepath.WalkDir(repoRoot, func(full string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if full == repoRoot {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, full)
		if err != nil || !filepath.IsLocal(rel) {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if !matchesAnyInclude(slashRel, patterns) {
			return nil
		}
		return copyFile(full, filepath.Join(dest, rel))
	})
}

func matchesAnyInclude(rel string, patterns []string) bool {
	base := path.Base(rel)
	for _, pat := range patterns {
		dirPat := strings.HasSuffix(pat, "/")
		trimmed := strings.TrimSuffix(pat, "/")
		if dirPat && (rel == trimmed || strings.HasPrefix(rel, trimmed+"/")) {
			return true
		}
		if matched, _ := path.Match(trimmed, rel); matched {
			return true
		}
		if !strings.Contains(trimmed, "/") {
			if matched, _ := path.Match(trimmed, base); matched {
				return true
			}
		}
		if rel == trimmed || strings.HasPrefix(rel, trimmed+"/") {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode()&0o777)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if werr := out.Close(); cerr == nil {
		cerr = werr
	}
	return cerr
}

func untrackedPatch(dir, rel string) (string, int) {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", 0
	}
	truncated := false
	if len(data) > maxPatchBytes {
		data = data[:maxPatchBytes]
		truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf("Binary file %s added", rel), 0
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, len(lines))
	for _, line := range lines {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if truncated {
		b.WriteString("... [truncated]\n")
	}
	return b.String(), len(lines)
}

func countPatchLines(patch string) (added, removed int) {
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}
