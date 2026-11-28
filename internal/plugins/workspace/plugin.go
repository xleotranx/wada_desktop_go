// Package workspace keeps the app's copy of the backend's record of where the
// user works: the projects, their workspaces, and the conversations in them.
// The tabs, All chats and the notifications list only these.
package workspace

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// syncPages bounds how far a full sync reads back through the backend's
// conversations.
const syncPages = 20

// Plugin is the copy of the record.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	host  shell.Host

	loaded     bool
	origin     string // the backend's, as last seen
	synced     uint64 // the connection attempt last read in full
	projects   []agent.Project
	workspaces []agent.Workspace // active, in the order of their projects
	sessions   map[agent.SessionID]agent.Session
	missed     map[agent.SessionID]bool // seen in events, not in a listing since
	// since holds the updates applied while a full sync runs, to apply again
	// over the record it read.
	since     []agent.WorkspaceUpdate
	list      []workspace.Conversation
	full      kernel.Task
	recent    kernel.Task
	picking   kernel.Task
	archiving kernel.Task

	remoteProject  projectForm
	projectFolders kernel.Task
	// choose asks the user for a folder, off the UI thread; tests replace it.
	choose func() (string, error)
	// now is the clock, in Unix milliseconds; tests replace it.
	now func() int64
}

var _ workspace.Service = (*Plugin)(nil)

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "workspace", Title: "Projects", Locked: true,
		Description: "The projects you work in, their workspaces and their conversations.",
		Requires:    []kernel.Named{agent.ServiceKey, shell.HostKey},
		Provides:    []kernel.Named{workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.sessions = map[agent.SessionID]agent.Session{}
	p.missed = map[agent.SessionID]bool{}
	p.origin = p.agent.Status().Origin
	if p.choose == nil {
		p.choose = func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Add project", ButtonLabel: "Add",
				Message: "Choose a folder for the agent to work in.", Directory: true, CreateDirectories: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		}
	}
	if p.now == nil {
		p.now = func() int64 { return time.Now().UnixMilli() }
	}
	kernel.On(c, agent.StatusChanged, p.statusChanged)
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, agent.Deleted, p.Forget)
	kernel.On(c, agent.WorkspaceUpdated, p.updated)
	kernel.On(c, chat.Started, p.started)
	kernel.Provide[workspace.Service](c, workspace.ServiceKey, p)
	p.maybeSync()
	return kernel.Contribute(c, shell.Overlays, "workspace.add-project", 0, shell.View{Render: p.projectDialog})
}

// ---- workspace.Service

func (p *Plugin) Loaded() bool                            { return p.loaded }
func (p *Plugin) Projects() []agent.Project               { return p.projects }
func (p *Plugin) Workspaces() []agent.Workspace           { return p.workspaces }
func (p *Plugin) Conversations() []workspace.Conversation { return p.list }

func (p *Plugin) Workspace(id agent.WorkspaceID) (agent.Workspace, bool) {
	i := slices.IndexFunc(p.workspaces, func(w agent.Workspace) bool { return w.ID == id })
	if i < 0 {
		return agent.Workspace{}, false
	}
	return p.workspaces[i], true
}

func (p *Plugin) At(dir string) (agent.Workspace, bool) {
	if dir == "" {
		return p.inbox()
	}
	dir = filepath.Clean(dir)
	i := slices.IndexFunc(p.workspaces, func(w agent.Workspace) bool { return w.Directory == dir })
	if i < 0 {
		return agent.Workspace{}, false
	}
	return p.workspaces[i], true
}

func (p *Plugin) inbox() (agent.Workspace, bool) {
	i := slices.IndexFunc(p.projects, func(pr agent.Project) bool { return pr.Inbox })
	if i < 0 {
		return agent.Workspace{}, false
	}
	j := slices.IndexFunc(p.workspaces, func(w agent.Workspace) bool { return w.Project == p.projects[i].ID })
	if j < 0 {
		return agent.Workspace{}, false
	}
	return p.workspaces[j], true
}

func (p *Plugin) Project(id agent.ProjectID) (agent.Project, bool) {
	i := slices.IndexFunc(p.projects, func(pr agent.Project) bool { return pr.ID == id })
	if i < 0 {
		return agent.Project{}, false
	}
	return p.projects[i], true
}

func (p *Plugin) Name(dir string) string {
	if w, ok := p.At(dir); ok {
		if w.Title != "" {
			return w.Title
		}
		if w.Isolation != agent.IsolateProject && w.Branch != "" {
			return w.Branch
		}
		if i := slices.IndexFunc(p.projects, func(pr agent.Project) bool { return pr.ID == w.Project }); i >= 0 {
			return p.projects[i].Name
		}
	}
	if dir == "" {
		return "Inbox"
	}
	return filepath.Base(dir)
}

func (p *Plugin) Lookup(id agent.SessionID) (workspace.Conversation, bool) {
	i := slices.IndexFunc(p.list, func(c workspace.Conversation) bool { return c.ID == id })
	if i < 0 {
		return workspace.Conversation{}, false
	}
	return p.list[i], true
}

func (p *Plugin) ChooseProject(done func(agent.Workspace)) {
	if p.picking.Running() || p.remoteProject.open {
		return
	}
	st := p.agent.Status()
	if st.State != agent.Ready {
		p.host.Toast(ui.Toast{Title: "Connect to Wada before adding a project", Type: components.ToastError})
		return
	}
	if st.RemoteHost != "" {
		p.projectFolders.Cancel()
		p.remoteProject = projectForm{open: true, host: st.RemoteHost, done: done}
		p.ctx.Invalidate()
		return
	}
	choose := p.choose
	p.addProject(func(context.Context) (string, error) { return choose() }, done,
		func(err error) {
			p.host.Toast(ui.Toast{Title: "The project was not added", Description: err.Error(), Type: components.ToastError})
		})
}

func (p *Plugin) addProject(choose func(context.Context) (string, error), done func(agent.Workspace), failed func(error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		failed(errNoRecord)
		return
	}
	kernel.Go(p.ctx, &p.picking, func(ctx context.Context) (agent.WorkspaceUpdate, error) {
		path, err := choose(ctx)
		if err != nil || path == "" { // failed or cancelled
			return agent.WorkspaceUpdate{}, err
		}
		if err := ctx.Err(); err != nil {
			return agent.WorkspaceUpdate{}, err
		}
		pr, w, err := ws.AddProject(ctx, path)
		return agent.WorkspaceUpdate{Project: &pr, Workspace: &w}, err
	}, func(up agent.WorkspaceUpdate, err error) {
		if err != nil {
			failed(err)
			return
		}
		if up.Workspace == nil {
			return
		}
		p.updated(up)
		done(*up.Workspace)
	})
}

func (p *Plugin) ArchiveProject(id agent.ProjectID, done func()) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		return
	}
	origin := p.origin
	kernel.Go(p.ctx, &p.archiving, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, ws.ArchiveProject(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The project was not removed", Description: err.Error(), Type: components.ToastError})
			return
		}
		if origin != p.origin {
			return
		}
		// The backend announces it too; this needn't wait for that.
		now := p.now()
		if i := slices.IndexFunc(p.projects, func(pr agent.Project) bool { return pr.ID == id }); i >= 0 {
			pr := p.projects[i]
			pr.Archived = now
			p.updated(agent.WorkspaceUpdate{Project: &pr})
		}
		done()
	})
}

func (p *Plugin) CreateWorkspace(req agent.CreateWorkspace, done func(agent.Workspace, error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		done(agent.Workspace{}, errNoRecord)
		return
	}
	origin := p.origin
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.Workspace, error) {
		return ws.CreateWorkspace(ctx, req)
	}, func(w agent.Workspace, err error) {
		if err == nil && origin == p.origin {
			p.updated(agent.WorkspaceUpdate{Workspace: &w})
		}
		done(w, err)
	})
}

func (p *Plugin) ArchiveWorkspace(id agent.WorkspaceID, force bool, done func(error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		done(errNoRecord)
		return
	}
	origin := p.origin
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, ws.ArchiveWorkspace(ctx, id, force)
	}, func(_ struct{}, err error) {
		if err == nil && origin == p.origin {
			if w, ok := p.Workspace(id); ok {
				w.Archived = p.now()
				p.updated(agent.WorkspaceUpdate{Workspace: &w})
			}
		}
		done(err)
	})
}

func (p *Plugin) Archived(done func([]agent.Workspace, error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		done(nil, errNoRecord)
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) ([]agent.Workspace, error) {
		list, err := ws.Workspaces(ctx, true)
		if err != nil {
			return nil, err
		}
		projects, err := ws.Projects(ctx, false)
		if err != nil {
			return nil, err
		}
		active := map[agent.ProjectID]bool{}
		for _, pr := range projects {
			active[pr.ID] = true
		}
		var archived []agent.Workspace
		for _, w := range list {
			if w.Archived != 0 && active[w.Project] {
				archived = append(archived, w)
			}
		}
		slices.SortStableFunc(archived, func(a, b agent.Workspace) int { return cmp.Compare(b.Archived, a.Archived) })
		return archived, nil
	}, done)
}

func (p *Plugin) RestoreWorkspace(id agent.WorkspaceID, done func(agent.Workspace, error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		done(agent.Workspace{}, errNoRecord)
		return
	}
	origin := p.origin
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.Workspace, error) {
		return ws.RestoreWorkspace(ctx, id)
	}, func(w agent.Workspace, err error) {
		if err == nil && origin == p.origin {
			p.updated(agent.WorkspaceUpdate{Workspace: &w})
		}
		done(w, err)
	})
}

func (p *Plugin) Refs(project agent.ProjectID, done func(agent.Refs, error)) {
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		done(agent.Refs{}, errNoRecord)
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.Refs, error) {
		return ws.Refs(ctx, project)
	}, done)
}

// errNoRecord reports a backend that keeps no projects.
var errNoRecord = errors.New("the agent keeps no projects")

func (p *Plugin) Retitle(id agent.SessionID, title string) {
	if s, ok := p.sessions[id]; ok && s.Title != title {
		s.Title = title
		p.sessions[id] = s
		p.changed()
	}
}

func (p *Plugin) Forget(id agent.SessionID) {
	if _, ok := p.sessions[id]; ok {
		delete(p.sessions, id)
		p.changed()
	}
}

// ---- Keeping the copy

// changed rebuilds the list and tells the consumers.
func (p *Plugin) changed() {
	p.list = nil // a fresh slice: consumers may still hold the last one
	for _, s := range p.sessions {
		w, ok := p.At(s.Directory)
		if !ok || s.Directory == "" {
			continue
		}
		p.list = append(p.list, workspace.Conversation{ID: s.ID, Workspace: w.ID, Directory: w.Directory,
			Title: s.Title, Model: s.Model, Updated: s.Updated})
	}
	slices.SortFunc(p.list, func(a, b workspace.Conversation) int {
		return cmp.Or(cmp.Compare(b.Updated, a.Updated), strings.Compare(string(a.ID), string(b.ID)))
	})
	kernel.Emit(p.ctx, workspace.Changed, struct{}{})
}

// updated applies a project or workspace the backend changed.
func (p *Plugin) updated(up agent.WorkspaceUpdate) {
	p.apply(up)
	if p.full.Running() {
		p.since = append(p.since, up)
	}
	p.order()
	p.changed()
	p.ctx.Invalidate()
}

func (p *Plugin) apply(up agent.WorkspaceUpdate) {
	if up.Project != nil {
		pr := *up.Project
		p.projects = slices.DeleteFunc(p.projects, func(x agent.Project) bool { return x.ID == pr.ID })
		if pr.Archived == 0 {
			p.projects = append(p.projects, pr)
		} else {
			p.workspaces = slices.DeleteFunc(p.workspaces, func(w agent.Workspace) bool { return w.Project == pr.ID })
		}
	}
	if up.Workspace != nil {
		w := *up.Workspace
		p.workspaces = slices.DeleteFunc(p.workspaces, func(x agent.Workspace) bool { return x.ID == w.ID })
		if w.Archived == 0 {
			p.workspaces = append(p.workspaces, w)
		}
	}
}

// order sorts projects by name with the inbox last, and workspaces by their
// projects, then oldest first. A workspace whose project isn't active is
// dropped.
func (p *Plugin) order() {
	slices.SortFunc(p.projects, func(a, b agent.Project) int {
		return cmp.Or(compareBool(a.Inbox, b.Inbox), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			strings.Compare(a.Root, b.Root), strings.Compare(string(a.ID), string(b.ID)))
	})
	rank := map[agent.ProjectID]int{}
	for i, pr := range p.projects {
		rank[pr.ID] = i
	}
	p.workspaces = slices.DeleteFunc(p.workspaces, func(w agent.Workspace) bool { _, ok := rank[w.Project]; return !ok })
	slices.SortFunc(p.workspaces, func(a, b agent.Workspace) int {
		return cmp.Or(cmp.Compare(rank[a.Project], rank[b.Project]), cmp.Compare(a.Created, b.Created),
			strings.Compare(string(a.ID), string(b.ID)))
	})
}

// started takes in a conversation the chat page just created.
func (p *Plugin) started(conv chat.Conversation) {
	if p.origin == "" || conv.Session == "" {
		return
	}
	dir := conv.Directory
	if w, ok := p.At(dir); ok {
		dir = w.Directory
	}
	s := p.sessions[conv.Session]
	s.ID, s.Directory, s.Model = conv.Session, cmp.Or(dir, s.Directory), conv.Model
	s.Title = cmp.Or(conv.Title, s.Title)
	s.Updated = max(s.Updated, p.now())
	p.sessions[conv.Session] = s
	delete(p.missed, conv.Session)
	p.changed()
}

func (p *Plugin) events(batch []agent.Event) {
	changed, finished, unknown := false, false, false
	for _, ev := range batch {
		var fn func(*agent.Session)
		switch ev.Kind {
		case agent.TurnStarted, agent.TurnInterrupted:
			fn = p.touch
		case agent.TurnSucceeded, agent.TurnFailed:
			fn = p.touch
			_, known := p.sessions[ev.Session]
			finished = finished || known
		case agent.SessionRenamed:
			fn = func(s *agent.Session) { s.Title = ev.Title }
		case agent.ModelChanged:
			fn = func(s *agent.Session) { s.Model = ev.Model }
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested:
		}
		if fn == nil {
			continue
		}
		s, ok := p.sessions[ev.Session]
		if !ok {
			// Another client's, or a side chat the backend doesn't list.
			if ev.Session != "" && !p.missed[ev.Session] {
				p.missed[ev.Session] = true
				unknown = true
			}
			continue
		}
		before := s
		fn(&s)
		if s != before {
			p.sessions[ev.Session] = s
			changed = true
		}
	}
	if changed {
		p.changed()
		p.ctx.Invalidate() // titles, models and order show in lists of chats
	}
	if finished || unknown {
		p.maybeSync()  // after a full sync that failed
		p.syncRecent() // a first turn is when the backend names a conversation
	}
}

func (p *Plugin) touch(s *agent.Session) { s.Updated = max(s.Updated, p.now()) }

func (p *Plugin) statusChanged(st agent.Status) {
	if st.State != agent.Ready || st.Origin != p.origin {
		p.picking.Cancel()
		p.projectFolders.Cancel()
		p.remoteProject = projectForm{}
	}
	if st.Origin != p.origin {
		p.origin = st.Origin
		p.full.Cancel()
		p.recent.Cancel()
		p.synced = 0
		p.loaded = false
		p.projects, p.workspaces = nil, nil
		p.sessions = map[agent.SessionID]agent.Session{}
		p.missed = map[agent.SessionID]bool{}
		p.since = nil
		p.changed()
	}
	p.maybeSync()
}

// ---- Reading the backend

// snapshot is what a sync read.
type snapshot struct {
	projects   []agent.Project
	workspaces []agent.Workspace
	sessions   []agent.Session
	complete   bool // the listing reached its end
	record     bool // projects and workspaces were read
}

// maybeSync reads the record and the conversations once per connection.
func (p *Plugin) maybeSync() {
	st := p.agent.Status()
	if st.State != agent.Ready || st.Attempt == p.synced || p.origin == "" {
		return
	}
	p.synced = st.Attempt
	p.sync(&p.full, syncPages)
}

func (p *Plugin) syncRecent() { p.sync(&p.recent, 1) }

// sync reads up to pages of the backend's conversations, newest first; a full
// sync then reads the projects and workspaces, after the listing has added a
// project for any conversation's new folder. Updates that land while it runs
// are applied again over what it read. A full sync that fails is tried again
// after the next finished turn.
func (p *Plugin) sync(task *kernel.Task, pages int) {
	origin, backend, full := p.origin, p.agent, task == &p.full
	ws, hasRecord := kernel.Use(p.ctx, agent.WorkspacesKey)
	known := map[agent.SessionID]bool{}
	for id := range p.sessions {
		known[id] = true
	}
	kernel.Go(p.ctx, task, func(ctx context.Context) (snapshot, error) {
		var snap snapshot
		cursor := ""
		for range pages {
			page, err := backend.Sessions(ctx, agent.SessionQuery{Cursor: cursor})
			if err != nil {
				return snap, err
			}
			snap.sessions = append(snap.sessions, page.Sessions...)
			if page.Next == "" {
				snap.complete = true
				break
			}
			cursor = page.Next
		}
		if full && hasRecord {
			var err error
			if snap.projects, err = ws.Projects(ctx, false); err != nil {
				return snap, err
			}
			if snap.workspaces, err = ws.Workspaces(ctx, false); err != nil {
				return snap, err
			}
			snap.record = true
		}
		return snap, nil
	}, func(snap snapshot, err error) {
		if origin != p.origin {
			return
		}
		if err != nil && full {
			p.synced = 0
			if !snap.record {
				p.since = nil
				return
			}
		}
		if full {
			if snap.record {
				p.projects, p.workspaces = snap.projects, snap.workspaces
				for _, up := range p.since {
					p.apply(up)
				}
				p.order()
			}
			p.since = nil
		}
		seen := map[agent.SessionID]bool{}
		for _, s := range snap.sessions {
			seen[s.ID] = true
			delete(p.missed, s.ID)
			if old, ok := p.sessions[s.ID]; ok {
				s.Title = cmp.Or(s.Title, old.Title)
				s.Updated = max(s.Updated, old.Updated)
				if s.Model.IsZero() {
					s.Model = old.Model
				}
			}
			if s.Directory != "" {
				s.Directory = filepath.Clean(s.Directory)
			}
			p.sessions[s.ID] = s
		}
		// A complete listing proves the ones known before it are gone; one
		// started meanwhile may not be listed yet.
		if err == nil && snap.complete {
			for id := range known {
				if !seen[id] {
					delete(p.sessions, id)
				}
			}
		}
		if full && (err == nil || snap.record) {
			p.loaded = true
		}
		p.changed()
		p.ctx.Invalidate()
	})
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
