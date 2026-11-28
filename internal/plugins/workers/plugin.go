// Package workers provides desktop UI for choosing the agent harness on new chats,
// monitoring spawned external CLI workers, inspecting their live transcripts and
// per-worktree diffs in a drawer, and managing secondary Git worktrees.
package workers

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

type drawerTab uint8

const (
	tabTranscript drawerTab = iota
	tabDiff
)

// Plugin owns the composer harness picker, the Work panel's Agents & Worktrees
// section, and the Worker Inspector drawer.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller
	host  shell.Host

	conv    chat.Conversation
	attempt uint64

	harnesses        []agent.Harness
	selectedProvider string
	picker           onechat.PickerState
	loadHarnesses    kernel.Task

	workers      []agent.Worker
	workersEpoch uint64
	worktrees    []agent.Worktree
	loadWorkers  kernel.Task
	loadTrees    kernel.Task
	// One task per worker stopped and per worktree removed, so one action
	// never cancels another's.
	stopTasks   map[agent.SessionID]*kernel.Task
	removeTasks map[string]*kernel.Task
	// seen is each worker's last status and worktree, to refresh the
	// worktrees only when one appears, moves or finishes.
	seen map[agent.SessionID]workerMark

	inspected      agent.SessionID
	drawerTab      drawerTab
	drawerMessages []agent.Message
	drawerChanges  []agent.FileChange
	drawerOpenDiff map[string]bool
	drawerLive     strings.Builder
	drawerLoading  bool
	loadDrawer     kernel.Task
}

type workerMark struct {
	status agent.WorkerStatus
	path   string
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID:          "workers",
		Title:       "Agents & Worktrees",
		Description: "Select an agent harness, supervise external CLI workers, and inspect worktrees and diffs.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.selectedProvider = agent.EmbeddedHarness
	p.drawerOpenDiff = map[string]bool{}
	p.stopTasks = map[agent.SessionID]*kernel.Task{}
	p.removeTasks = map[string]*kernel.Task{}
	p.seen = map[agent.SessionID]workerMark{}

	p.follow(p.chat.Current())
	kernel.On(c, chat.Changed, p.follow)
	kernel.On(c, agent.StatusChanged, p.onStatusChanged)
	kernel.On(c, agent.WorkerUpdated, p.onWorkerUpdated)
	kernel.On(c, agent.Events, p.onEvents)
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if p.agent.Status().State == agent.Ready {
			p.reloadHarnesses()
			p.reloadWorkspaceState()
		}
	})

	if p.agent.Status().State == agent.Ready {
		p.attempt = p.agent.Status().Attempt
		p.reloadHarnesses()
		p.reloadWorkspaceState()
	}

	// Stamp chosen harness early on new chats before outgoing attachments/models commit.
	kernel.Transform(c, chat.Outgoing, -15, p.outgoing)
	// After every transform that adds files, skills or a workflow.
	kernel.Transform(c, chat.Outgoing, 5000, p.textOnly)

	kernel.Contribute(c, chat.ComposerAccessory, "workers.harness", -5, shell.View{Render: p.harnessPickerView})
	kernel.Contribute(c, chat.Work, "workers.panel", 15, chat.WorkSection{
		Title:  "Agents & worktrees",
		Shown:  p.workShown,
		Render: p.workPanelView,
	})
	kernel.Contribute(c, chat.HideWork, "workers.hide-work", 0, func() bool { return p.inspected != "" })
	kernel.Contribute(c, chat.Drawers, "workers.drawer", 10, shell.View{Render: p.drawerView})
	return nil
}

func (p *Plugin) workShown() bool {
	return p.conv.Session != "" && (len(p.workers) > 0 || p.secondaryWorktreeCount() > 0)
}

func (p *Plugin) secondaryWorktreeCount() int {
	count := 0
	for _, wt := range p.worktrees {
		if !wt.IsMain {
			count++
		}
	}
	return count
}

func (p *Plugin) onStatusChanged(st agent.Status) {
	if st.State == agent.Ready && st.Attempt != p.attempt {
		p.attempt = st.Attempt
		p.reloadHarnesses()
		p.reloadWorkspaceState()
	}
}

func (p *Plugin) follow(conv chat.Conversation) {
	changedSess := conv.Session != p.conv.Session
	changedDir := conv.Directory != p.conv.Directory
	p.conv = conv
	if !changedSess && !changedDir {
		return
	}
	p.loadWorkers.Cancel()
	p.loadTrees.Cancel()
	p.loadDrawer.Cancel()
	p.workers = nil
	p.workersEpoch++
	p.worktrees = nil
	if changedSess {
		p.closeDrawer()
	}
	if p.agent.Status().State == agent.Ready {
		p.reloadWorkspaceState()
	}
}

func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	if d.Session != "" {
		return d, nil, nil
	}
	chosen := p.selectedProvider
	if !agent.ExternalHarness(chosen) {
		return d, nil, nil
	}
	for _, h := range p.harnesses {
		if h.ID == chosen && !h.Available {
			return d, nil, fmt.Errorf("%s CLI (%s) is not installed or unavailable on PATH", cmp.Or(h.Name, h.ID), h.Binary)
		}
	}
	if d.Provider == "" {
		d.Provider = chosen
	}
	return d, nil, nil
}

// textOnly refuses what an external CLI harness's conversation cannot take,
// rather than dropping it on the way: it takes a message's text only.
func (p *Plugin) textOnly(d chat.Draft) (chat.Draft, func(), error) {
	provider := d.Provider
	if d.Session != "" && d.Session == p.conv.Session {
		provider = cmp.Or(provider, p.conv.Provider)
	}
	if !agent.ExternalHarness(provider) {
		return d, nil, nil
	}
	var unsupported []string
	if len(d.Files) > 0 {
		unsupported = append(unsupported, "attached files")
	}
	if len(d.Skills) > 0 {
		unsupported = append(unsupported, "skills")
	}
	if d.Workflow != "" {
		unsupported = append(unsupported, "workflow")
	}
	if d.Instructions != "" {
		unsupported = append(unsupported, "instructions")
	}
	if len(unsupported) == 0 {
		return d, nil, nil
	}
	return d, nil, fmt.Errorf("%s takes text only; remove the %s", p.harnessName(provider), strings.Join(unsupported, " and "))
}

func (p *Plugin) harnessName(id string) string {
	for _, h := range p.harnesses {
		if h.ID == id {
			return cmp.Or(h.Name, h.ID)
		}
	}
	return id
}

func (p *Plugin) reloadHarnesses() {
	svc, ok := kernel.Use(p.ctx, agent.HarnessesKey)
	if !ok {
		p.loadHarnesses.Cancel()
		p.harnesses = nil
		return
	}
	kernel.Go(p.ctx, &p.loadHarnesses, func(ctx context.Context) ([]agent.Harness, error) {
		return svc.Harnesses(ctx)
	}, func(list []agent.Harness, err error) {
		if err != nil {
			return
		}
		p.harnesses = list
		p.ctx.Invalidate()
	})
}

func (p *Plugin) reloadWorkspaceState() {
	if p.conv.Session == "" && p.conv.Directory == "" {
		return
	}
	if p.conv.Session != "" {
		if wsvc, ok := kernel.Use(p.ctx, agent.WorkersKey); ok {
			parent := p.conv.Session
			epoch := p.workersEpoch
			kernel.Go(p.ctx, &p.loadWorkers, func(ctx context.Context) ([]agent.Worker, error) {
				return wsvc.Workers(ctx, parent)
			}, func(list []agent.Worker, err error) {
				if err != nil || parent != p.conv.Session {
					return
				}
				if p.workersEpoch == epoch {
					p.workers = list
				} else {
					for _, w := range list {
						if !slices.ContainsFunc(p.workers, func(cur agent.Worker) bool { return cur.ID == w.ID }) {
							p.workers = append(p.workers, w)
						}
					}
				}
				p.ctx.Invalidate()
			})
		}
	}
	if p.conv.Directory != "" {
		if tsvc, ok := kernel.Use(p.ctx, agent.WorktreesKey); ok {
			dir := p.conv.Directory
			kernel.Go(p.ctx, &p.loadTrees, func(ctx context.Context) ([]agent.Worktree, error) {
				list, err := tsvc.ListWorktrees(ctx, dir)
				if err != nil {
					return nil, err
				}
				for i := range list {
					if !list[i].IsMain {
						if pr, perr := tsvc.PRStatus(ctx, list[i].Path); perr == nil && pr != nil {
							list[i].PR = pr
						}
					}
				}
				return list, nil
			}, func(list []agent.Worktree, err error) {
				if err != nil || dir != p.conv.Directory {
					return
				}
				p.worktrees = list
				p.ctx.Invalidate()
			})
		}
	}
}

func (p *Plugin) onWorkerUpdated(w agent.Worker) {
	if w.ParentSession == p.conv.Session && p.conv.Session != "" {
		p.workersEpoch++
		idx := slices.IndexFunc(p.workers, func(cur agent.Worker) bool { return cur.ID == w.ID })
		if idx >= 0 {
			p.workers[idx] = w
		} else {
			p.workers = append([]agent.Worker{w}, p.workers...)
		}
		p.ctx.Invalidate()
	}
	if p.inspected != "" && w.ID == p.inspected {
		if isTerminalWorkerStatus(w.Status) {
			p.loadInspected(w.ID)
		}
		p.ctx.Invalidate()
	}
	prev, known := p.seen[w.ID]
	p.seen[w.ID] = workerMark{status: w.Status, path: w.WorktreePath}
	moved := !known || prev.path != w.WorktreePath || (isTerminalWorkerStatus(w.Status) && !isTerminalWorkerStatus(prev.status))
	if moved && p.conv.Directory != "" && (w.ParentSession == p.conv.Session || w.WorktreePath != "") {
		p.refreshWorktreesOnly()
	}
}

func (p *Plugin) refreshWorktreesOnly() {
	tsvc, ok := kernel.Use(p.ctx, agent.WorktreesKey)
	if !ok || p.conv.Directory == "" {
		return
	}
	dir := p.conv.Directory
	kernel.Go(p.ctx, &p.loadTrees, func(ctx context.Context) ([]agent.Worktree, error) {
		list, err := tsvc.ListWorktrees(ctx, dir)
		if err != nil {
			return nil, err
		}
		for i := range list {
			if !list[i].IsMain {
				if pr, perr := tsvc.PRStatus(ctx, list[i].Path); perr == nil && pr != nil {
					list[i].PR = pr
				}
			}
		}
		return list, nil
	}, func(list []agent.Worktree, err error) {
		if err != nil || dir != p.conv.Directory {
			return
		}
		p.worktrees = list
		p.ctx.Invalidate()
	})
}

func (p *Plugin) onEvents(batch []agent.Event) {
	if p.inspected == "" {
		return
	}
	reload := false
	changed := false
	for _, ev := range batch {
		if ev.Session != p.inspected {
			continue
		}
		switch ev.Kind {
		case agent.TurnStarted:
			p.drawerLive.Reset()
			changed = true
		case agent.TextDelta:
			if ev.Text != nil && ev.Text.Delta != "" {
				p.drawerLive.WriteString(ev.Text.Delta)
				changed = true
			}
		case agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			p.drawerLive.Reset()
			reload = true
		case agent.TextStarted, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested,
			agent.ModelChanged, agent.SessionRenamed:
		}
	}
	if reload {
		p.loadInspected(p.inspected)
	} else if changed {
		p.ctx.Invalidate()
	}
}

func (p *Plugin) openDrawer(id agent.SessionID) {
	if id == "" {
		return
	}
	p.inspected = id
	p.drawerMessages = nil
	p.drawerChanges = nil
	p.drawerLive.Reset()
	clear(p.drawerOpenDiff)
	p.loadInspected(id)
}

func (p *Plugin) closeDrawer() {
	p.inspected = ""
	p.drawerMessages = nil
	p.drawerChanges = nil
	p.drawerLive.Reset()
	p.drawerLoading = false
	p.loadDrawer.Cancel()
}

type drawerPayload struct {
	msgs    []agent.Message
	changes []agent.FileChange
}

func (p *Plugin) loadInspected(id agent.SessionID) {
	p.drawerLoading = true
	backend := p.agent
	changesSvc, hasChanges := kernel.Use(p.ctx, agent.ChangesKey)
	kernel.Go(p.ctx, &p.loadDrawer, func(ctx context.Context) (drawerPayload, error) {
		msgs, err := backend.Messages(ctx, id)
		if err != nil {
			return drawerPayload{}, err
		}
		var ch []agent.FileChange
		if hasChanges {
			ch, _ = changesSvc.Changes(ctx, id)
		}
		return drawerPayload{msgs: msgs, changes: ch}, nil
	}, func(res drawerPayload, err error) {
		if id != p.inspected {
			return
		}
		p.drawerLoading = false
		if err == nil {
			p.drawerMessages = res.msgs
			p.drawerChanges = res.changes
		}
		p.ctx.Invalidate()
	})
}

func (p *Plugin) inspectedWorker() (agent.Worker, bool) {
	for _, w := range p.workers {
		if w.ID == p.inspected {
			return w, true
		}
	}
	return agent.Worker{}, false
}

func (p *Plugin) stopWorker(id agent.SessionID) {
	backend := p.agent
	task := p.stopTasks[id]
	if task == nil {
		task = new(kernel.Task)
		p.stopTasks[id] = task
	}
	kernel.Go(p.ctx, task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, backend.Abort(ctx, id)
	}, func(_ struct{}, err error) {
		delete(p.stopTasks, id)
		if err != nil {
			p.host.Toast(ui.Toast{Title: "Could not stop the agent", Description: err.Error(), Type: components.ToastError})
		}
		p.reloadWorkspaceState()
	})
}

func (p *Plugin) removeWorktree(wtPath string) {
	tsvc, ok := kernel.Use(p.ctx, agent.WorktreesKey)
	if !ok || p.conv.Directory == "" {
		return
	}
	dir := p.conv.Directory
	task := p.removeTasks[wtPath]
	if task == nil {
		task = new(kernel.Task)
		p.removeTasks[wtPath] = task
	}
	kernel.Go(p.ctx, task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, tsvc.RemoveWorktree(ctx, dir, wtPath, false)
	}, func(_ struct{}, err error) {
		delete(p.removeTasks, wtPath)
		if err != nil {
			p.host.Toast(ui.Toast{Title: "Could not remove worktree", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.refreshWorktreesOnly()
	})
}

func isTerminalWorkerStatus(st agent.WorkerStatus) bool {
	switch st {
	case agent.WorkerCompleted, agent.WorkerFailed, agent.WorkerStopped:
		return true
	case agent.WorkerIdle, agent.WorkerRunning, agent.WorkerWaitingApproval:
		return false
	}
	return false
}
