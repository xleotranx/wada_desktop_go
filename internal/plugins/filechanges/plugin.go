// Package filechanges shows session changes, local uncommitted Git changes or
// a worktree workspace's changes since its base, with line counts and
// expandable diffs: below Context in the work panel, and as a workspace's
// Changes tab.
package filechanges

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
)

type changeSource uint8

const (
	sessionChanges changeSource = iota
	uncommittedChanges
	baseChanges
)

func (source changeSource) label() string {
	switch source {
	case uncommittedChanges:
		return "Uncommitted Git changes"
	case baseChanges:
		return "Changes since the base"
	case sessionChanges:
	}
	return "Session changes"
}

// base is the workspace at the view's folder when it has a base to compare
// with.
func (p *changeView) base() (agent.Workspace, bool) {
	ws, ok := kernel.Use(p.ctx, workspace.ServiceKey)
	if !ok || p.directory == "" {
		return agent.Workspace{}, false
	}
	w, ok := ws.At(p.directory)
	return w, ok && w.BaseRef != "" && w.Isolation != agent.IsolateProject
}

// sources are the sources the view offers: a workspace's tab has no session.
func (p *changeView) sources() []changeSource {
	var out []changeSource
	if !p.workspace {
		out = append(out, sessionChanges)
	}
	out = append(out, uncommittedChanges)
	if _, ok := p.base(); ok {
		out = append(out, baseChanges)
	}
	return out
}

// PaneKind is the Changes tab's kind in tabs.Panes.
const PaneKind = "filechanges.changes"

// Plugin is the work panel's file changes, and the workspaces' Changes tabs.
type Plugin struct {
	changeView            // the work panel's, following the open conversation
	pane       changeView // the Changes tab on show
	paneOn     bool
}

// changeView is one list of changes: its source, folder and what it shows.
type changeView struct {
	ctx       *kernel.Context
	workspace bool // a workspace's Changes tab rather than a conversation's
	sourced   bool // a provider of agent.ChangesKey was there at the last read
	session   agent.SessionID
	directory string
	mode      changeSource
	changes   []agent.FileChange
	load      kernel.Task
	known     bool
	problem   string
	open      map[string]bool // files whose diff is shown, by path
	expanded  bool            // the file list; collapsed by default, independent of diffs
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "filechanges", Title: "File Changes",
		Description: "Session, uncommitted and since-the-base changes with diffs, in the work panel and a Changes tab.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.open = map[string]bool{}
	p.pane = changeView{ctx: c, workspace: true, open: map[string]bool{}, expanded: true}
	kernel.On(c, chat.Changed, p.follow)
	kernel.On(c, agent.Events, p.events)
	// Session changes come from an optional provider; read again when one
	// comes or goes.
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if _, ok := kernel.Use(c, agent.ChangesKey); ok != p.sourced && p.session != "" && p.mode == sessionChanges {
			p.readChanges()
		}
	})
	// A read before the backend connected failed; read again once it has.
	attempt := kernel.Require(c, agent.ServiceKey).Status().Attempt
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != attempt && p.session != "" {
			p.readChanges()
		}
		if st.State == agent.Ready {
			attempt = st.Attempt
		}
	})
	p.follow(kernel.Require(c, chat.ControllerKey).Current())
	kernel.Contribute(c, tabs.Panes, PaneKind, 10, tabs.PaneKind{
		Title: func(string) string { return "Changes" }, Icon: lucide.GitCompareArrows,
		Render: func(c *ui.Context, _ tabs.Tab) { p.paneView(c) }, Show: p.showPane, Hide: func() { p.paneOn = false }})
	kernel.Contribute(c, kernel.Commands, "filechanges.show", 0, kernel.Command{
		Title: "Show Changes", Menu: "View", Keys: []string{"shift+cmd+d"}, Run: p.openTab,
		Enabled: func() bool { _, ok := kernel.Use(c, tabs.ServiceKey); return ok }})
	return kernel.Contribute(c, chat.Work, "filechanges.files", 25, chat.WorkSection{Title: "File changes",
		Shown: func() bool { return p.session != "" }, Render: p.panelView})
}

// openTab shows the Changes tab of the space on show.
func (p *Plugin) openTab() {
	if t, ok := kernel.Use(p.ctx, tabs.ServiceKey); ok {
		t.Open(PaneKind, "", t.Space())
	}
}

// showPane reads the changes of the tab's workspace; it starts on the
// changes since the base where there is one.
func (p *Plugin) showPane(t tabs.Tab) {
	p.paneOn = true
	dir := t.Folder
	if dir == "" {
		if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
			if box, ok := ws.At(""); ok {
				dir = box.Directory
			}
		}
	}
	v := &p.pane
	if dir != v.directory {
		v.directory = dir
		v.mode = uncommittedChanges
		if _, ok := v.base(); ok {
			v.mode = baseChanges
		}
		v.reset()
	}
	v.readChanges()
}

func (p *Plugin) follow(conv chat.Conversation) {
	if conv.Session == p.session && conv.Directory == p.directory {
		return
	}
	p.session, p.directory = conv.Session, conv.Directory
	if !slices.Contains(p.sources(), p.mode) {
		p.mode = sessionChanges
	}
	p.reset()
	if p.session != "" {
		p.readChanges()
	}
}

// reset drops results and expansion when their source or folder changes.
func (p *changeView) reset() {
	p.load.Cancel()
	p.changes, p.known, p.problem = nil, false, ""
	clear(p.open)
}

func (p *changeView) chooseSource() {
	p.reset()
	p.readChanges()
}

func (p *Plugin) events(batch []agent.Event) {
	panel, pane := false, false
	for _, ev := range batch {
		if ev.Kind != agent.TurnSucceeded && ev.Kind != agent.TurnFailed && ev.Kind != agent.TurnInterrupted {
			continue
		}
		panel = panel || ev.Session == p.session && p.session != ""
		pane = pane || p.paneOn // any turn may have changed the workspace's files
	}
	if panel {
		p.readChanges()
	}
	if pane {
		p.pane.readChanges()
	}
}

// A failed refresh keeps the last known changes visible.
func (p *changeView) readChanges() {
	source, ok := kernel.Use(p.ctx, agent.ChangesKey)
	p.sourced = ok
	id, mode, directory := p.session, p.mode, p.directory
	based, hasBase := p.base()
	record, hasRecord := kernel.Use(p.ctx, agent.WorkspacesKey)
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) ([]agent.FileChange, error) {
		switch mode {
		case uncommittedChanges:
			return gitChanges(ctx, directory)
		case baseChanges:
			if !hasBase || !hasRecord {
				return nil, errors.New("this workspace has no base to compare with")
			}
			changes, err := record.BaseChanges(ctx, based.ID)
			if err != nil {
				return nil, errors.New("couldn’t compare with " + based.BaseRef)
			}
			return changes, nil
		case sessionChanges:
		}
		if !ok {
			return nil, errors.New("session changes aren’t available with this backend")
		}
		changes, err := source.Changes(ctx, id)
		if err != nil {
			return nil, errors.New("couldn’t load file changes")
		}
		return changes, nil
	}, func(changes []agent.FileChange, err error) {
		if id != p.session || directory != p.directory || mode != p.mode {
			return
		}
		p.problem = ""
		if err != nil {
			message := []rune(err.Error())
			if len(message) > 0 {
				message[0] = unicode.ToUpper(message[0])
				p.problem = string(message)
			}
		} else {
			p.changes, p.known = slices.Clone(changes), true
			slices.SortFunc(p.changes, func(a, b agent.FileChange) int {
				if dir := strings.Compare(path.Dir(a.Path), path.Dir(b.Path)); dir != 0 {
					return dir
				}
				return strings.Compare(a.Path, b.Path)
			})
		}
	})
}
