// Package worktrees makes and archives the workspaces that live in Git
// worktrees: the New Workspace dialog, Archive Workspace, and the work
// panel's Workspace section with the branch, its base and its pull request.
package worktrees

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
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

	// The work panel's pull request.
	pr     *agent.PRStatus
	prFor  string
	prTask kernel.Task
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "worktrees", Title: "Worktree workspaces",
		Description: "New Workspace…, Archive Workspace…, and the work panel's Workspace section.",
		Requires:    []kernel.Named{chat.ControllerKey, workspace.ServiceKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	kernel.On(c, chat.Changed, func(chat.Conversation) { p.readPR() })
	kernel.On(c, workspace.Changed, func(struct{}) { p.readPR() })
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
	kernel.Contribute(c, shell.Overlays, "worktrees.dialogs", 0, shell.View{Render: p.dialogs})
	return kernel.Contribute(c, chat.Work, "worktrees.workspace", 5, chat.WorkSection{Title: "Workspace",
		Shown: func() bool { _, ok := p.current(); return ok }, Render: p.section})
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
	p.ws.CreateWorkspace(req, func(w agent.Workspace, err error) {
		p.creating = false
		if err != nil {
			p.problem = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.newOn = false
		p.chat.NewChat(w.Directory)
	})
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
