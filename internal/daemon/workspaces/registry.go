// Package workspaces keeps wadad's record of the projects the user works in
// and their workspaces, the working directories conversations and terminals
// run in. The record is read into memory at startup and every change is
// written to wadad.db before it applies.
package workspaces

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

// ErrNotFound reports a project or workspace the record doesn't have, or no
// longer has active.
var ErrNotFound = errors.New("workspaces: not found")

// Store is where the record is saved.
type Store interface {
	Projects(ctx context.Context) ([]store.ProjectRow, error)
	Workspaces(ctx context.Context) ([]store.WorkspaceRow, error)
	PutProject(ctx context.Context, projects []store.ProjectRow, workspaces []store.WorkspaceRow) error
}

// Registry is the record. Its methods are safe for concurrent use; changes are
// made one at a time.
type Registry struct {
	st    Store
	inbox string
	emit  func(protocol.Event)
	now   func() int64

	mu         sync.Mutex
	projects   map[string]store.ProjectRow
	workspaces map[string]store.WorkspaceRow
}

// Options configures Open.
type Options struct {
	// Inbox is the directory of conversations started without a project. Its
	// project is created when missing and can't be archived.
	Inbox string
	// Emit, when set, receives a workspace_updated event for each project and
	// workspace a change touched, after the change is saved.
	Emit func(protocol.Event)
	// Now is the clock in Unix milliseconds; tests replace it.
	Now func() int64
}

// Open reads the record from st and makes sure the inbox project exists.
func Open(ctx context.Context, st Store, opts Options) (*Registry, error) {
	if !filepath.IsAbs(opts.Inbox) {
		return nil, errors.New("workspaces: the inbox needs an absolute directory")
	}
	r := &Registry{
		st:         st,
		inbox:      filepath.Clean(opts.Inbox),
		emit:       opts.Emit,
		now:        opts.Now,
		projects:   map[string]store.ProjectRow{},
		workspaces: map[string]store.WorkspaceRow{},
	}
	if r.now == nil {
		r.now = func() int64 { return time.Now().UnixMilli() }
	}
	if r.emit == nil {
		r.emit = func(protocol.Event) {}
	}
	projects, err := st.Projects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		r.projects[p.ID] = p
	}
	workspaces, err := st.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range workspaces {
		r.workspaces[w.ID] = w
	}
	if _, _, err := r.AddProject(ctx, r.inbox); err != nil {
		return nil, fmt.Errorf("workspaces: inbox: %w", err)
	}
	return r, nil
}

// ---- Reading

// Projects lists the active projects by name, the inbox last, and with
// archived, the archived ones after them.
func (r *Registry) Projects(archived bool) []protocol.ProjectInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []protocol.ProjectInfo
	for _, p := range r.projects {
		if p.Archived == 0 || archived {
			out = append(out, r.projectInfo(p))
		}
	}
	slices.SortFunc(out, func(a, b protocol.ProjectInfo) int {
		return cmp.Or(compareBool(a.Archived != 0, b.Archived != 0), compareBool(a.Inbox, b.Inbox),
			strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Root, b.Root),
			strings.Compare(a.ID, b.ID))
	})
	return out
}

// Workspaces lists the active workspaces of project, or of every project when
// project is empty, oldest first; with archived, the archived ones after them.
func (r *Registry) Workspaces(project string, archived bool) []protocol.WorkspaceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []protocol.WorkspaceInfo
	for _, w := range r.workspaces {
		if (project == "" || w.Project == project) && (w.Archived == 0 || archived) {
			out = append(out, workspaceInfo(w))
		}
	}
	slices.SortFunc(out, func(a, b protocol.WorkspaceInfo) int {
		return cmp.Or(compareBool(a.Archived != 0, b.Archived != 0), cmp.Compare(a.Created, b.Created), strings.Compare(a.ID, b.ID))
	})
	return out
}

// Workspace returns a workspace, archived or not.
func (r *Registry) Workspace(id string) (protocol.WorkspaceInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workspaces[id]
	return workspaceInfo(w), ok
}

// Active returns the active workspace at dir.
func (r *Registry) Active(dir string) (protocol.WorkspaceInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.activeAt(clean(dir))
	return workspaceInfo(w), ok
}

// Inbox returns the inbox's workspace.
func (r *Registry) Inbox() protocol.WorkspaceInfo {
	w, _ := r.Active(r.inbox)
	return w
}

// At returns the workspace a conversation in dir belongs to: the active one
// there, else the one there archived last.
func (r *Registry) At(dir string) (protocol.WorkspaceInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.at(clean(dir))
	return workspaceInfo(w), ok
}

func (r *Registry) at(dir string) (store.WorkspaceRow, bool) {
	if w, ok := r.activeAt(dir); ok {
		return w, true
	}
	var last store.WorkspaceRow
	for _, w := range r.workspaces {
		if w.Dir == dir && w.Archived > last.Archived {
			last = w
		}
	}
	return last, last.ID != ""
}

func (r *Registry) activeAt(dir string) (store.WorkspaceRow, bool) {
	for _, w := range r.workspaces {
		if w.Archived == 0 && w.Dir == dir {
			return w, true
		}
	}
	return store.WorkspaceRow{}, false
}

func (r *Registry) activeProjectAt(root string) (store.ProjectRow, bool) {
	for _, p := range r.projects {
		if p.Archived == 0 && p.Root == root {
			return p, true
		}
	}
	return store.ProjectRow{}, false
}

func (r *Registry) projectInfo(p store.ProjectRow) protocol.ProjectInfo {
	inbox := p.Root == r.inbox
	name := p.Name
	if name == "" && inbox {
		name = "Inbox"
	}
	if name == "" {
		name = filepath.Base(p.Root)
	}
	return protocol.ProjectInfo{ID: p.ID, Root: p.Root, Name: name, Inbox: inbox, Created: p.Created, Archived: p.Archived}
}

func workspaceInfo(w store.WorkspaceRow) protocol.WorkspaceInfo {
	return protocol.WorkspaceInfo{ID: w.ID, Project: w.Project, Isolation: w.Isolation, Directory: w.Dir,
		Title: w.Title, Created: w.Created, Archived: w.Archived}
}

// ---- Changing

// Adopt returns the workspace a conversation in dir belongs to, adding a
// project at dir when no workspace was ever there.
func (r *Registry) Adopt(ctx context.Context, dir string) (protocol.WorkspaceInfo, error) {
	if w, ok := r.At(dir); ok {
		return w, nil
	}
	_, w, err := r.AddProject(ctx, dir)
	return w, err
}

// AddProject returns the active project at root and its project-directory
// workspace. It restores the one archived there last, or else creates one.
func (r *Registry) AddProject(ctx context.Context, root string) (protocol.ProjectInfo, protocol.WorkspaceInfo, error) {
	if !filepath.IsAbs(root) {
		return protocol.ProjectInfo{}, protocol.WorkspaceInfo{}, fmt.Errorf("workspaces: %q is not an absolute path", root)
	}
	root = filepath.Clean(root)
	r.mu.Lock()
	if p, ok := r.activeProjectAt(root); ok {
		w, _ := r.activeAt(root)
		r.mu.Unlock()
		return r.projectInfo(p), workspaceInfo(w), nil
	}
	if w, ok := r.activeAt(root); ok {
		r.mu.Unlock()
		return protocol.ProjectInfo{}, protocol.WorkspaceInfo{}, fmt.Errorf("workspaces: %s is already a workspace of another project", w.Dir)
	}
	now := r.now()
	var p store.ProjectRow
	for _, q := range r.projects {
		if q.Root == root && q.Archived > p.Archived {
			p = q
		}
	}
	var w store.WorkspaceRow
	if p.ID != "" {
		p.Archived = 0
		for _, x := range r.workspaces {
			if x.Project == p.ID && x.Isolation == protocol.IsolationProject && x.Archived > w.Archived {
				w = x
			}
		}
	} else {
		p = store.ProjectRow{ID: newID("prj_"), Root: root, Created: now}
	}
	if w.ID != "" {
		w.Archived = 0
	} else {
		w = store.WorkspaceRow{ID: newID("wks_"), Project: p.ID, Isolation: protocol.IsolationProject, Dir: root, Created: now}
	}
	err := r.put(ctx, []store.ProjectRow{p}, []store.WorkspaceRow{w})
	pi := r.projectInfo(p)
	r.mu.Unlock()
	if err != nil {
		return protocol.ProjectInfo{}, protocol.WorkspaceInfo{}, err
	}
	wi := workspaceInfo(w)
	r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Project: &pi})
	r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Workspace: &wi})
	return pi, wi, nil
}

// RenameProject sets an active project's name; empty goes back to the root's
// base name.
func (r *Registry) RenameProject(ctx context.Context, id, name string) error {
	r.mu.Lock()
	p, ok := r.projects[id]
	if !ok || p.Archived != 0 {
		r.mu.Unlock()
		return ErrNotFound
	}
	p.Name = strings.TrimSpace(name)
	err := r.put(ctx, []store.ProjectRow{p}, nil)
	pi := r.projectInfo(p)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Project: &pi})
	return nil
}

// ArchiveProject archives an active project and its active workspaces. Their
// conversations stay with the backend; adding the project again restores it.
func (r *Registry) ArchiveProject(ctx context.Context, id string) error {
	r.mu.Lock()
	p, ok := r.projects[id]
	if !ok || p.Archived != 0 {
		r.mu.Unlock()
		return ErrNotFound
	}
	if p.Root == r.inbox {
		r.mu.Unlock()
		return errors.New("workspaces: the inbox can't be archived")
	}
	now := r.now()
	p.Archived = now
	var ws []store.WorkspaceRow
	for _, w := range r.workspaces {
		if w.Project == id && w.Archived == 0 {
			w.Archived = now
			ws = append(ws, w)
		}
	}
	err := r.put(ctx, []store.ProjectRow{p}, ws)
	pi := r.projectInfo(p)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Project: &pi})
	for _, w := range ws {
		wi := workspaceInfo(w)
		r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Workspace: &wi})
	}
	return nil
}

// RenameWorkspace sets an active workspace's title; empty shows its
// project's name.
func (r *Registry) RenameWorkspace(ctx context.Context, id, title string) error {
	r.mu.Lock()
	w, ok := r.workspaces[id]
	if !ok || w.Archived != 0 {
		r.mu.Unlock()
		return ErrNotFound
	}
	w.Title = strings.TrimSpace(title)
	err := r.put(ctx, nil, []store.WorkspaceRow{w})
	r.mu.Unlock()
	if err != nil {
		return err
	}
	wi := workspaceInfo(w)
	r.emit(protocol.Event{Kind: protocol.EventWorkspaceUpdated, Workspace: &wi})
	return nil
}

// put saves rows, then applies them in memory. Called with mu held.
func (r *Registry) put(ctx context.Context, ps []store.ProjectRow, ws []store.WorkspaceRow) error {
	if err := r.st.PutProject(ctx, ps, ws); err != nil {
		return err
	}
	for _, p := range ps {
		r.projects[p.ID] = p
	}
	for _, w := range ws {
		r.workspaces[w.ID] = w
	}
	return nil
}

func clean(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

func newID(prefix string) string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return prefix + hex.EncodeToString(raw[:])
}
