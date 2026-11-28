package workspaces

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

// PendingError refuses to archive a worktree wadad made while removing it
// would lose work.
type PendingError struct {
	Files   int // uncommitted
	Commits int // on its branch and on no remote or its base
}

func (e *PendingError) Error() string {
	var parts []string
	if e.Files > 0 {
		parts = append(parts, count(e.Files, "uncommitted file"))
	}
	if e.Commits > 0 {
		parts = append(parts, count(e.Commits, "unpushed commit"))
	}
	return "the worktree has " + strings.Join(parts, " and ")
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Service is the record together with the Git work that makes, removes and
// restores worktree workspaces. Its changes are made one at a time.
type Service struct {
	*Registry
	git *worktree.Manager
	mu  sync.Mutex
}

// NewService serves r, making worktrees with git.
func NewService(r *Registry, git *worktree.Manager) *Service {
	return &Service{Registry: r, git: git}
}

// Create makes a workspace in a new or existing worktree of an active
// project's repository.
func (s *Service) Create(ctx context.Context, req protocol.WorkspaceCreateRequest) (protocol.WorkspaceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Project(req.Project)
	if !ok || p.Archived != 0 {
		return protocol.WorkspaceInfo{}, ErrNotFound
	}
	if p.Inbox {
		return protocol.WorkspaceInfo{}, errors.New("workspaces: the inbox has no repository")
	}
	rel, err := s.subdir(ctx, p.Root)
	if err != nil {
		return protocol.WorkspaceInfo{}, err
	}
	row := store.WorkspaceRow{Project: p.ID, Isolation: req.Isolation, Title: req.Title}
	switch req.Isolation {
	case protocol.IsolationWorktree:
		added, err := s.git.Add(ctx, p.Root, worktree.AddOptions{Mode: req.Mode, From: req.From, PR: req.PR, Slug: req.Slug})
		if err != nil {
			if added.Path != "" {
				_ = s.git.Remove(ctx, p.Root, added.Path, true)
			}
			return protocol.WorkspaceInfo{}, err
		}
		row.Dir, row.WorktreeRoot, row.Owned = filepath.Join(added.Path, rel), added.Path, true
		row.Branch, row.BaseRef, row.PR = added.Branch, added.BaseRef, added.PR
		// A new branch nobody named is named from the first message sent.
		row.AutoName = req.Slug == "" && req.Title == "" && cmp.Or(req.Mode, protocol.StartBranchOff) == protocol.StartBranchOff
		w, err := s.addWorkspace(ctx, row)
		if err != nil {
			// The worktree is this call's own, with nothing in it yet.
			_ = s.git.Remove(ctx, p.Root, added.Path, true)
		}
		return w, err
	case protocol.IsolationExisting:
		want := filepath.Clean(req.Path)
		if real, err := filepath.EvalSymlinks(want); err == nil {
			want = real
		}
		list, err := s.git.List(ctx, p.Root)
		if err != nil {
			return protocol.WorkspaceInfo{}, err
		}
		for _, wt := range list {
			if wt.Path == want {
				row.Dir, row.WorktreeRoot, row.Branch = filepath.Join(wt.Path, rel), wt.Path, wt.Branch
				row.BaseRef = s.git.DefaultRef(ctx, wt.Path)
				return s.addWorkspace(ctx, row)
			}
		}
		return protocol.WorkspaceInfo{}, fmt.Errorf("workspaces: %s is not a worktree of %s", req.Path, p.Name)
	case protocol.IsolationProject:
		return protocol.WorkspaceInfo{}, errors.New("workspaces: a project has its own directory's workspace already")
	}
	return protocol.WorkspaceInfo{}, fmt.Errorf("workspaces: unknown isolation %q", req.Isolation)
}

// subdir is where a project's root is inside its repository, "." at the top.
func (s *Service) subdir(ctx context.Context, root string) (string, error) {
	repo, err := s.git.RepoRoot(ctx, root)
	if err != nil {
		return "", err
	}
	real := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		real = r
	}
	rel, err := filepath.Rel(repo, real)
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return "", fmt.Errorf("workspaces: %s is outside its repository", root)
	}
	return rel, nil
}

// Archive archives a workspace other than a project's own directory's. A
// worktree wadad made is removed once no active workspace is in it; one with
// work removing it would lose is refused with a *PendingError unless force.
// Its branch stays, so Restore can put it back.
func (s *Service) Archive(ctx context.Context, id string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return ErrNotFound
	}
	if w.Isolation == protocol.IsolationProject {
		return errors.New("workspaces: a project's own directory goes with the project; remove the project instead")
	}
	p, _ := s.Project(w.Project)
	if w.Owned && s.activeUsers(w.WorktreeRoot) <= 1 {
		if _, err := os.Stat(w.WorktreeRoot); err == nil {
			files, commits, err := s.git.Pending(ctx, w.WorktreeRoot, w.Branch, w.BaseRef)
			if err != nil {
				return err
			}
			if files+commits > 0 && !force {
				return &PendingError{Files: files, Commits: commits}
			}
			if out, err := s.teardown(ctx, id); err != nil && !force {
				return &TeardownError{Output: out, Err: err}
			}
			if err := s.git.Remove(ctx, p.Root, w.WorktreeRoot, files > 0); err != nil {
				return err
			}
		}
	}
	_, err := s.setArchived(ctx, id, true)
	return err
}

// Restore makes an archived workspace active again, putting back a worktree
// wadad made from its branch.
func (s *Service) Restore(ctx context.Context, id string) (protocol.WorkspaceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspace(id)
	if !ok || w.Archived == 0 {
		return protocol.WorkspaceInfo{}, ErrNotFound
	}
	p, _ := s.Project(w.Project)
	if p.Archived != 0 {
		return protocol.WorkspaceInfo{}, errors.New("workspaces: its project is archived; add the project again first")
	}
	if _, err := os.Stat(w.WorktreeRoot); w.WorktreeRoot != "" && err != nil {
		if !w.Owned {
			return protocol.WorkspaceInfo{}, fmt.Errorf("workspaces: the worktree %s is gone", w.WorktreeRoot)
		}
		if err := s.git.Recreate(ctx, p.Root, w.WorktreeRoot, w.Branch); err != nil {
			return protocol.WorkspaceInfo{}, err
		}
	}
	return s.setArchived(ctx, id, false)
}

// Refs lists what an active project's repository offers a new workspace.
func (s *Service) Refs(ctx context.Context, project string) (protocol.WorkspaceRefs, error) {
	p, ok := s.Project(project)
	if !ok || p.Archived != 0 {
		return protocol.WorkspaceRefs{}, ErrNotFound
	}
	return s.git.Refs(ctx, p.Root)
}

// Changes lists a workspace's changes since it left its base.
func (s *Service) Changes(ctx context.Context, id string) ([]protocol.FileChange, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return nil, ErrNotFound
	}
	if w.BaseRef == "" {
		return nil, errors.New("workspaces: the workspace has no base to compare with")
	}
	return s.git.DiffAgainst(ctx, w.Directory, w.BaseRef)
}
