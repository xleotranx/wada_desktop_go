package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/protocol"
)

// fetchTimeout bounds the fetch before a worktree is made from remote refs.
const fetchTimeout = 30 * time.Second

// AddOptions says how Add makes a worktree.
type AddOptions struct {
	Mode protocol.StartMode
	From string // branch-off: the ref, default DefaultRef; checkout-branch: the branch
	PR   int    // checkout-pr
	Slug string // names the directory and a new branch
}

// Added is a worktree Add made.
type Added struct {
	Path    string // the worktree's root, symlinks resolved
	Branch  string
	BaseRef string
	PR      int
}

// Add makes a worktree of dir's repository under the worktree directory, fetching
// origin first when it has one, and copies the .worktreeinclude files into it.
func (m *Manager) Add(ctx context.Context, dir string, opts AddOptions) (Added, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return Added{}, err
	}
	// Refs can come from a model's tool call: never let one read as an option.
	if strings.HasPrefix(opts.From, "-") {
		return Added{}, fmt.Errorf("invalid ref %q", opts.From)
	}
	storage, err := m.storageDirectory(root)
	if err != nil {
		return Added{}, err
	}
	fetch(ctx, root)
	slug := sanitizeSlug(cmpOr(opts.Slug, strings.TrimPrefix(opts.From, "origin/"), fmt.Sprint("pr-", opts.PR)))
	wtSlug := slug + "-" + randomHex(2)
	sum := sha256.Sum256([]byte(root))
	dest := filepath.Join(storage, hex.EncodeToString(sum[:6]), wtSlug)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Added{}, err
	}
	added := Added{PR: opts.PR}
	switch opts.Mode {
	case protocol.StartCheckoutBranch:
		if opts.From == "" {
			return Added{}, errors.New("worktree: a branch to check out is required")
		}
		if err := checkoutBranch(ctx, root, dest, opts.From); err != nil {
			return Added{}, err
		}
		added.BaseRef = defaultRef(ctx, root)
	case protocol.StartCheckoutPR:
		if opts.PR <= 0 {
			return Added{}, errors.New("worktree: a pull request number is required")
		}
		info := m.forge.prInfo(ctx, root, opts.PR)
		switch {
		case info.Head != "" && !info.Fork:
			// The project's own branch: work on it, so a push updates the
			// pull request.
			_, _ = gitOut(ctx, root, "fetch", "--quiet", "origin", info.Head)
			err = checkoutBranch(ctx, root, dest, "origin/"+info.Head)
		default:
			// Another repository's branch: a local copy without an upstream,
			// so a push never goes to someone else's branch.
			local := m.branchPrefix + fmt.Sprintf("pr-%d", opts.PR)
			if m.BranchExists(ctx, root, local) {
				local += "-" + randomHex(2)
			}
			ref := fmt.Sprintf("refs/pull/%d/head", opts.PR)
			if _, err = gitOut(ctx, root, "fetch", "--quiet", "origin", ref); err == nil {
				_, err = gitOut(ctx, root, "worktree", "add", "-b", local, "--", dest, "FETCH_HEAD")
			}
		}
		if err != nil {
			return Added{}, fmt.Errorf("worktree: pull request #%d: %w", opts.PR, err)
		}
		added.BaseRef = defaultRef(ctx, root)
		if info.Base != "" {
			added.BaseRef = "origin/" + info.Base
		}
	case protocol.StartBranchOff, "":
		from := cmpOr(opts.From, defaultRef(ctx, root))
		branch := m.branchPrefix + wtSlug
		if _, err := gitOut(ctx, root, "worktree", "add", "-b", branch, "--", dest, from); err != nil {
			return Added{}, fmt.Errorf("worktree: %w", err)
		}
		added.BaseRef = from
	default:
		return Added{}, fmt.Errorf("worktree: unknown mode %q", opts.Mode)
	}
	if resolved, rerr := filepath.EvalSymlinks(dest); rerr == nil {
		dest = resolved
	}
	added.Path = dest
	added.Branch = currentBranch(ctx, dest)
	if err := applyWorktreeInclude(root, dest); err != nil {
		return added, fmt.Errorf("worktree created at %s, include copy failed: %w", dest, err)
	}
	return added, nil
}

// storageDirectory keeps new checkouts outside the source tree, where they
// would be treated as untracked files and scanned by .worktreeinclude.
func (m *Manager) storageDirectory(repo string) (string, error) {
	dir, err := filepath.Abs(m.directory)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(repo, dir); err == nil && filepath.IsLocal(rel) {
		return "", fmt.Errorf("worktree: storage directory %s is inside source repository %s", dir, repo)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("worktree: create storage directory: %w", err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(repo, dir); err == nil && filepath.IsLocal(rel) {
		return "", fmt.Errorf("worktree: storage directory %s is inside source repository %s", dir, repo)
	}
	return dir, nil
}

// checkoutBranch adds a worktree at dest on a branch; "origin/x" with no
// local x makes x track it.
func checkoutBranch(ctx context.Context, root, dest, branch string) error {
	local := strings.TrimPrefix(branch, "origin/")
	var err error
	if _, verr := gitOut(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+local); verr == nil {
		_, err = gitOut(ctx, root, "worktree", "add", "--", dest, local)
	} else {
		_, err = gitOut(ctx, root, "worktree", "add", "--track", "-b", local, "--", dest, "origin/"+local)
	}
	if err != nil && strings.Contains(err.Error(), "already") {
		return fmt.Errorf("worktree: %s is already checked out in another worktree; open that one instead", local)
	}
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	return nil
}

// Recreate puts back a removed worktree at path on its branch.
func (m *Manager) Recreate(ctx context.Context, dir, path, branch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return err
	}
	if strings.HasPrefix(branch, "-") || branch == "" {
		return fmt.Errorf("worktree: invalid branch %q", branch)
	}
	_, _ = gitOut(ctx, root, "worktree", "prune")
	if _, err := gitOut(ctx, root, "worktree", "add", "--", path, branch); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	return applyWorktreeInclude(root, path)
}

// Pending counts what removing the worktree at dir would lose: uncommitted
// files, and commits on branch that neither base nor any remote has.
func (m *Manager) Pending(ctx context.Context, dir, branch, base string) (files, commits int, err error) {
	st, err := gitOut(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(st, "\n") {
		if strings.TrimSpace(line) != "" {
			files++
		}
	}
	if branch == "" || strings.HasPrefix(branch, "-") {
		return files, 0, nil
	}
	args := []string{"rev-list", "--count", branch, "--not", "--remotes"}
	if base != "" && !strings.HasPrefix(base, "-") {
		args = append(args, base)
	}
	if out, rerr := gitOut(ctx, dir, args...); rerr == nil {
		commits, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	return files, commits, nil
}

// Refs lists what dir's repository offers a new worktree.
func (m *Manager) Refs(ctx context.Context, dir string) (protocol.WorkspaceRefs, error) {
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return protocol.WorkspaceRefs{}, err
	}
	refs := protocol.WorkspaceRefs{Default: defaultRef(ctx, root)}
	if out, err := gitOut(ctx, root, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads"); err == nil {
		refs.Branches = lines(out)
	}
	if out, err := gitOut(ctx, root, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/remotes"); err == nil {
		for _, r := range lines(out) {
			if !strings.HasSuffix(r, "/HEAD") && strings.Contains(r, "/") {
				refs.Remotes = append(refs.Remotes, r)
			}
		}
	}
	refs.PRs = m.forge.openPRs(ctx, root)
	if wts, err := m.List(ctx, root); err == nil {
		refs.Worktrees = wts
	}
	return refs, nil
}

// DiffAgainst returns the changes in dir since it left base: commits and
// uncommitted work, against the merge base of base and HEAD, with paths
// relative to dir.
func (m *Manager) DiffAgainst(ctx context.Context, dir, base string) ([]protocol.FileChange, error) {
	if base == "" || strings.HasPrefix(base, "-") {
		return nil, fmt.Errorf("worktree: invalid base %q", base)
	}
	mbOut, err := gitOut(ctx, dir, "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("worktree: no common history with %s: %w", base, err)
	}
	mb := strings.TrimSpace(mbOut)
	raw, err := gitOut(ctx, dir, "diff", "--relative", "--no-renames", "--name-status", "-z", mb)
	if err != nil {
		return nil, err
	}
	var changes []protocol.FileChange
	fields := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		code, rel := fields[i], filepath.ToSlash(fields[i+1])
		status := protocol.FileModified
		switch {
		case strings.HasPrefix(code, "A"):
			status = protocol.FileAdded
		case strings.HasPrefix(code, "D"):
			status = protocol.FileDeleted
		}
		patch, _ := gitOut(ctx, dir, "diff", "--relative", mb, "--", rel)
		if len(patch) > maxPatchBytes {
			patch = patch[:maxPatchBytes] + "\n... [truncated]"
		}
		added, removed := countPatchLines(patch)
		changes = append(changes, protocol.FileChange{Path: rel, Status: status, Added: added, Removed: removed, Patch: patch})
	}
	if out, err := gitOut(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for _, rel := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
			if rel == "" {
				continue
			}
			rel = filepath.ToSlash(rel)
			patch, added := untrackedPatch(dir, rel)
			changes = append(changes, protocol.FileChange{Path: rel, Status: protocol.FileAdded, Added: added, Patch: patch})
		}
	}
	return changes, nil
}

// defaultRef is the ref a new branch starts from: origin's default branch,
// else the checkout's own branch, else HEAD.
func defaultRef(ctx context.Context, root string) string {
	if out, err := gitOut(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	if br := currentBranch(ctx, root); br != "" {
		return br
	}
	return "HEAD"
}

func currentBranch(ctx context.Context, dir string) string {
	out, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if br := strings.TrimSpace(out); err == nil && br != "HEAD" {
		return br
	}
	return ""
}

// fetch updates origin's refs, when there is an origin, giving up quietly.
func fetch(ctx context.Context, root string) {
	if _, err := gitOut(ctx, root, "remote", "get-url", "origin"); err != nil {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	_, _ = gitOut(fctx, root, "-c", "credential.interactive=never", "fetch", "--quiet", "--prune", "origin")
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" && v != "pr-0" {
			return v
		}
	}
	return ""
}

// ---- Pull requests through gh

type prDetails struct {
	Head, Base string
	Fork       bool
}

// prInfo reads a pull request's branches, or nothing when gh can't.
func (f *Forge) prInfo(ctx context.Context, dir string, n int) prDetails {
	host, err := f.originHost(ctx, dir)
	if err != nil || host == "" || !f.isHostAuthenticated(ctx, host) {
		return prDetails{}
	}
	raw, err := f.run(ctx, dir, "gh", "pr", "view", strconv.Itoa(n), "--json", "headRefName,baseRefName,isCrossRepository")
	if err != nil {
		return prDetails{}
	}
	var v struct {
		Head  string `json:"headRefName"`
		Base  string `json:"baseRefName"`
		Cross bool   `json:"isCrossRepository"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return prDetails{}
	}
	return prDetails{Head: v.Head, Base: v.Base, Fork: v.Cross}
}

// openPRs lists the repository's open pull requests, or none when gh can't.
func (f *Forge) openPRs(ctx context.Context, dir string) []protocol.PullRequestRef {
	host, err := f.originHost(ctx, dir)
	if err != nil || host == "" || !f.isHostAuthenticated(ctx, host) {
		return nil
	}
	raw, err := f.run(ctx, dir, "gh", "pr", "list", "--state", "open", "--limit", "50", "--json", "number,title,headRefName,isCrossRepository")
	if err != nil {
		return nil
	}
	var list []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Head   string `json:"headRefName"`
		Cross  bool   `json:"isCrossRepository"`
	}
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	out := make([]protocol.PullRequestRef, 0, len(list))
	for _, pr := range list {
		out = append(out, protocol.PullRequestRef{Number: pr.Number, Title: pr.Title, Branch: pr.Head, Fork: pr.Cross})
	}
	return out
}

// DefaultRef is the ref a new branch of dir's repository starts from.
func (m *Manager) DefaultRef(ctx context.Context, dir string) string {
	root, err := m.RepoRoot(ctx, dir)
	if err != nil {
		return ""
	}
	return defaultRef(ctx, root)
}
