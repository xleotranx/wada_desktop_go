// Package worktrees makes, archives and restores the workspaces that live in
// Git worktrees: the New Workspace dialog, Archive Workspace, Restore
// Workspace, and the work panel's Workspace section with the branch, its base
// and its pull request. It also runs a workspace's wada.json: its setup after
// New Workspace and its services from the section, once the user trusts it.
package worktrees

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	contractterm "wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Command IDs other plugins may invoke.
const (
	NewID     = "worktrees.new"
	ArchiveID = "worktrees.archive"
	RestoreID = "worktrees.restore"
)

// Plugin is the worktree workspaces' UI.
type Plugin struct {
	ctx  *kernel.Context
	chat chat.Controller
	ws   workspace.Service
	host shell.Host

	// The New Workspace dialog.
	newOn     bool
	project   agent.ProjectID
	existing  bool // an existing worktree rather than a new one
	refs      agent.Refs
	refsFor   agent.ProjectID
	refsState string // "", "loading" or a failure
	start     string // "ref:<name>" or "pr:<n>"
	checkout  bool   // check the branch out rather than branch off it
	path      string // the existing worktree
	name      string
	creating  bool
	problem   string
	pickers   [3]onechat.PickerState // project, start, existing worktree

	// Archive Workspace.
	archiving   agent.Workspace
	archiveOn   bool
	unsaved     string // why the backend refused; asking to discard
	archiveBusy bool

	// Restore Workspace.
	restoreOn  bool
	gone       []agent.Workspace // the archived workspaces
	goneState  string            // "", "loading" or a failure
	goneEpoch  int               // drops a stale read
	restoring  agent.WorkspaceID
	restoreErr string

	// The work panel's pull request.
	pr     *agent.PRStatus
	prFor  string
	prTask kernel.Task

	// The open workspace's wada.json.
	cfg     agent.WorkspaceConfig
	cfgFor  agent.WorkspaceID
	cfgTask kernel.Task

	// Asking to trust a wada.json before it runs.
	trustOn   bool
	trustWs   agent.Workspace
	trustCfg  agent.WorkspaceConfig
	trustFork bool   // the workspace checks out a pull request from a fork
	trustThen string // what runs once trusted: "setup", "service:<name>" or ""
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "worktrees", Title: "Worktree workspaces",
		Description: "New Workspace…, Archive Workspace…, Restore Workspace…, and the work panel's Workspace section.",
		Requires:    []kernel.Named{chat.ControllerKey, workspace.ServiceKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	kernel.On(c, chat.Changed, func(chat.Conversation) { p.readPR(); p.readConfig() })
	kernel.On(c, workspace.Changed, func(struct{}) { p.readPR(); p.readConfig() })
	kernel.On(c, contractterm.Updated, func(t contractterm.Info) {
		if w, ok := p.here(); ok && t.Directory == w.Directory {
			p.readConfig()
		}
	})
	kernel.Contribute(c, kernel.Commands, NewID, 2, kernel.Command{
		Title: "New Workspace…", Menu: "File", Keys: []string{"shift+cmd+n"}, Run: p.openNew})
	kernel.Contribute(c, kernel.Commands, ArchiveID, 3, kernel.Command{
		Title: "Archive Workspace…", Menu: "File",
		Enabled: func() bool { _, ok := p.current(); return ok },
		Run: func() {
			if w, ok := p.current(); ok {
				p.askArchive(w)
			}
		}})
	kernel.Contribute(c, kernel.Commands, RestoreID, 4, kernel.Command{
		Title: "Restore Workspace…", Menu: "File", Run: p.openRestore})
	kernel.Contribute(c, shell.Overlays, "worktrees.dialogs", 0, shell.View{Render: p.dialogs})
	return kernel.Contribute(c, chat.Work, "worktrees.workspace", 5, chat.WorkSection{Title: "Workspace",
		Shown: p.sectionShown, Render: p.section})
}

// sectionShown is a worktree workspace, or a workspace whose wada.json has
// setup or services.
func (p *Plugin) sectionShown() bool {
	if _, ok := p.current(); ok {
		return true
	}
	w, ok := p.here()
	return ok && w.ID == p.cfgFor && p.cfg.Found && (len(p.cfg.Setup) > 0 || len(p.cfg.Services) > 0 || p.cfg.Problem != "")
}

// here is the workspace the open conversation works in, other than the
// inbox.
func (p *Plugin) here() (agent.Workspace, bool) {
	dir := p.chat.Current().Directory
	if dir == "" {
		return agent.Workspace{}, false
	}
	w, ok := p.ws.At(dir)
	if !ok {
		return agent.Workspace{}, false
	}
	if pr, ok := p.ws.Project(w.Project); ok && pr.Inbox {
		return agent.Workspace{}, false
	}
	return w, true
}

// current is the worktree workspace the open conversation works in.
func (p *Plugin) current() (agent.Workspace, bool) {
	dir := p.chat.Current().Directory
	if dir == "" {
		return agent.Workspace{}, false
	}
	w, ok := p.ws.At(dir)
	return w, ok && w.Isolation != agent.IsolateProject
}

// ---- New Workspace

// openNew opens the dialog on the open conversation's project, else the
// first project that isn't the inbox.
func (p *Plugin) openNew() {
	p.project = ""
	if w, ok := p.ws.At(p.chat.Current().Directory); ok {
		if pr, ok := p.ws.Project(w.Project); ok && !pr.Inbox {
			p.project = pr.ID
		}
	}
	if p.project == "" {
		for _, pr := range p.ws.Projects() {
			if !pr.Inbox {
				p.project = pr.ID
				break
			}
		}
	}
	p.existing, p.checkout, p.start, p.path, p.name, p.problem = false, false, "", "", "", ""
	p.newOn = true
	p.loadRefs()
}

func (p *Plugin) loadRefs() {
	if p.project == "" || p.refsFor == p.project && p.refsState == "" {
		return
	}
	project := p.project
	p.refs, p.refsFor, p.refsState = agent.Refs{}, project, "loading"
	p.ws.Refs(project, func(refs agent.Refs, err error) {
		if project != p.refsFor {
			return
		}
		p.refs, p.refsState = refs, ""
		if err != nil {
			p.refsState = err.Error()
		}
		if p.start == "" && refs.Default != "" {
			p.start = "ref:" + refs.Default
		}
		p.ctx.Invalidate()
	})
}

// request is what the dialog asks for, or why it can't yet.
func (p *Plugin) request() (agent.CreateWorkspace, error) {
	req := agent.CreateWorkspace{Project: p.project, Title: strings.TrimSpace(p.name), Slug: strings.TrimSpace(p.name)}
	if p.project == "" {
		return req, errors.New("add a project first")
	}
	if p.existing {
		if p.path == "" {
			return req, errors.New("choose a worktree")
		}
		req.Isolation, req.Path = agent.IsolateExisting, p.path
		return req, nil
	}
	req.Isolation = agent.IsolateWorktree
	switch {
	case strings.HasPrefix(p.start, "pr:"):
		n, err := strconv.Atoi(strings.TrimPrefix(p.start, "pr:"))
		if err != nil {
			return req, err
		}
		req.Mode, req.PR = agent.CheckoutPR, n
	case p.checkout:
		req.Mode, req.From = agent.CheckoutBranch, strings.TrimPrefix(p.start, "ref:")
	default:
		req.Mode, req.From = agent.BranchOff, strings.TrimPrefix(p.start, "ref:")
	}
	return req, nil
}

func (p *Plugin) create() {
	req, err := p.request()
	if err != nil || p.creating {
		return
	}
	p.creating, p.problem = true, ""
	fork := p.fork()
	p.ws.CreateWorkspace(req, func(w agent.Workspace, err error) {
		p.creating = false
		if err != nil {
			p.problem = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.newOn = false
		p.chat.NewChat(w.Directory)
		p.afterCreate(w, fork)
	})
}

// fork reports that the dialog checks out a pull request from a fork.
func (p *Plugin) fork() bool {
	n, err := strconv.Atoi(strings.TrimPrefix(p.start, "pr:"))
	if p.existing || !strings.HasPrefix(p.start, "pr:") || err != nil {
		return false
	}
	for _, pr := range p.refs.PRs {
		if pr.Number == n {
			return pr.Fork
		}
	}
	return false
}

// ---- Archive Workspace

func (p *Plugin) askArchive(w agent.Workspace) {
	p.archiving, p.unsaved, p.archiveOn = w, "", true
}

func (p *Plugin) archive(force bool) {
	if p.archiveBusy {
		return
	}
	w := p.archiving
	p.archiveBusy = true
	p.ws.ArchiveWorkspace(w.ID, force, func(err error) {
		p.archiveBusy = false
		switch {
		case errors.Is(err, agent.ErrWorkUnsaved) && !force:
			p.unsaved = err.Error()
		case err != nil:
			p.archiveOn = false
			p.host.Toast(ui.Toast{Title: "The workspace was not archived", Description: err.Error(), Type: components.ToastError})
		default:
			p.archiveOn = false
		}
		p.ctx.Invalidate()
	})
}

// ---- Restore Workspace

// openRestore opens the list of archived workspaces, read afresh.
func (p *Plugin) openRestore() {
	p.restoreOn, p.gone, p.goneState, p.restoreErr, p.restoring = true, nil, "loading", "", ""
	p.goneEpoch++
	epoch := p.goneEpoch
	p.ws.Archived(func(list []agent.Workspace, err error) {
		if epoch != p.goneEpoch {
			return
		}
		p.gone, p.goneState = list, ""
		if err != nil {
			p.goneState = err.Error()
		}
		p.ctx.Invalidate()
	})
}

// restore makes an archived workspace active again and opens a new chat in
// it; a failure stays in the dialog.
func (p *Plugin) restore(id agent.WorkspaceID) {
	if p.restoring != "" {
		return
	}
	p.restoring, p.restoreErr = id, ""
	p.ws.RestoreWorkspace(id, func(w agent.Workspace, err error) {
		p.restoring = ""
		if err != nil {
			p.restoreErr = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.restoreOn = false
		p.goneEpoch++
		p.chat.NewChat(w.Directory)
	})
}

// goneName is what the list calls an archived workspace.
func (p *Plugin) goneName(w agent.Workspace) string {
	if w.Title != "" {
		return w.Title
	}
	if w.Branch != "" {
		return w.Branch
	}
	if pr, ok := p.ws.Project(w.Project); ok && w.Isolation == agent.IsolateProject {
		return pr.Name
	}
	return filepath.Base(w.Directory)
}

// ---- wada.json

// afterCreate runs a new workspace's setup: at once when its wada.json is
// trusted, else once the user trusts it. A fork's setup always asks.
func (p *Plugin) afterCreate(w agent.Workspace, fork bool) {
	rec, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.WorkspaceConfig, error) {
		return rec.Config(ctx, w.ID)
	}, func(cfg agent.WorkspaceConfig, err error) {
		if err != nil || !cfg.Found || len(cfg.Setup) == 0 {
			return
		}
		if cfg.Trusted && !fork {
			p.runSetup(w)
			return
		}
		p.askTrust(w, cfg, fork, "setup")
	})
}

// readConfig reads the open workspace's wada.json.
func (p *Plugin) readConfig() {
	w, ok := p.here()
	rec, has := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok || !has {
		p.cfgTask.Cancel()
		p.cfg, p.cfgFor = agent.WorkspaceConfig{}, ""
		return
	}
	if w.ID != p.cfgFor {
		p.cfg = agent.WorkspaceConfig{}
	}
	id := w.ID
	kernel.Go(p.ctx, &p.cfgTask, func(ctx context.Context) (agent.WorkspaceConfig, error) {
		return rec.Config(ctx, id)
	}, func(cfg agent.WorkspaceConfig, err error) {
		if err != nil {
			return
		}
		p.cfg, p.cfgFor = cfg, id
		p.ctx.Invalidate()
	})
}

func (p *Plugin) askTrust(w agent.Workspace, cfg agent.WorkspaceConfig, fork bool, then string) {
	p.trustOn, p.trustWs, p.trustCfg, p.trustFork, p.trustThen = true, w, cfg, fork, then
}

// trust allows the wada.json the prompt showed, then runs what asked.
func (p *Plugin) trust() {
	rec, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		return
	}
	w, hash, then := p.trustWs, p.trustCfg.Hash, p.trustThen
	p.trustOn = false
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, rec.TrustConfig(ctx, w.ID, hash)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "wada.json was not trusted", Description: err.Error(), Type: components.ToastError})
			return
		}
		switch {
		case then == "setup":
			p.runSetup(w)
		case strings.HasPrefix(then, "service:"):
			p.startService(w, strings.TrimPrefix(then, "service:"))
		default:
			p.readConfig()
		}
	})
}

// runSetup starts the workspace's setup. A failed start is reported, not
// tried again.
func (p *Plugin) runSetup(w agent.Workspace) {
	p.run(w, "The setup didn’t start", "setup", func(ctx context.Context, rec agent.Workspaces) (string, error) {
		return rec.RunSetup(ctx, w.ID)
	})
}

func (p *Plugin) startService(w agent.Workspace, name string) {
	p.run(w, "“"+name+"” didn’t start", "service:"+name, func(ctx context.Context, rec agent.Workspaces) (string, error) {
		return rec.StartService(ctx, w.ID, name)
	})
}

func (p *Plugin) stopService(w agent.Workspace, name string) {
	p.run(w, "“"+name+"” didn’t stop", "", func(ctx context.Context, rec agent.Workspaces) (string, error) {
		return "", rec.StopService(ctx, w.ID, name)
	})
}

// run starts or stops something of the workspace's wada.json; one refused as
// untrusted asks to trust the file, then does what was asked.
func (p *Plugin) run(w agent.Workspace, failed, then string, fn func(context.Context, agent.Workspaces) (string, error)) {
	rec, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok {
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (string, error) {
		return fn(ctx, rec)
	}, func(_ string, err error) {
		switch {
		case errors.Is(err, agent.ErrUntrusted) && then != "":
			var cfgTask kernel.Task
			kernel.Go(p.ctx, &cfgTask, func(ctx context.Context) (agent.WorkspaceConfig, error) {
				return rec.Config(ctx, w.ID)
			}, func(cfg agent.WorkspaceConfig, err error) {
				if err == nil && cfg.Found {
					p.askTrust(w, cfg, false, then)
				}
			})
		case err != nil:
			p.host.Toast(ui.Toast{Title: failed, Description: err.Error(), Type: components.ToastError})
		}
		p.readConfig()
	})
}

// showTerminal opens a terminal's tab in the workspace's space.
func (p *Plugin) showTerminal(w agent.Workspace, id string) {
	if t, ok := kernel.Use(p.ctx, tabs.ServiceKey); ok && id != "" {
		t.Open(contractterm.PaneKind, id, w.Directory)
	}
}

// ---- The work panel

// readPR reads the pull request of the open worktree workspace's branch.
func (p *Plugin) readPR() {
	w, ok := p.current()
	if !ok {
		p.prTask.Cancel()
		p.pr, p.prFor = nil, ""
		return
	}
	if w.Directory == p.prFor {
		return
	}
	p.pr, p.prFor = nil, w.Directory
	prs, ok := kernel.Use(p.ctx, agent.WorktreesKey)
	if !ok {
		return
	}
	dir := w.Directory
	kernel.Go(p.ctx, &p.prTask, func(ctx context.Context) (*agent.PRStatus, error) {
		return prs.PRStatus(ctx, dir)
	}, func(pr *agent.PRStatus, err error) {
		if err == nil && dir == p.prFor {
			p.pr = pr
			p.ctx.Invalidate()
		}
	})
}
