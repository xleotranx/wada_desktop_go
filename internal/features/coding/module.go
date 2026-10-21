// Package coding implements the optional native workspace and review tools.
package coding

import (
	"context"
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"path/filepath"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"
)

type Module struct {
	started               bool
	stop                  context.CancelFunc
	Policy                PolicyState
	stopPolicy            context.CancelFunc
	closed                bool
	VCS                   map[string]workspace.VCSInfo
	VCSRequest            feature.Request
	Mention               MentionState
	host                  feature.Host
	Files                 *FilesState
	Changes               *ChangesState
	QuickOpen, QuickFocus bool
	QuickSelected         int
	QuickList             ui.ListState
	FilesWrap, DiffSplit  bool
	Recent                map[string][]RecentFile
}

func New(host feature.Host) feature.Module {
	lifetime, cancel := context.WithCancel(host.Lifetime)
	host.Lifetime = lifetime
	return &Module{stop: cancel, host: host, Recent: map[string][]RecentFile{}, Policy: PolicyState{Dirs: make(chan workspace.PolicyTarget, 1), Writes: make(chan workspace.PolicyChange, 16)}}
}
func (a *Module) ID() string { return "coding" }
func (a *Module) Close() {
	if a.closed {
		return
	}
	a.closed = true
	a.stop()
	a.cancel()
	a.CloseMention()
	a.VCSRequest.Stop()
	a.Policy.Request.Stop()
	if a.stopPolicy != nil {
		a.stopPolicy()
	}
	a.Files = nil
	a.Changes = nil
	a.QuickOpen = false
}
func (a *Module) cancel() {
	if st := a.Files; st != nil {
		st.ReadRequest.Stop()
		st.FindRequest.Stop()
		for _, r := range st.ListRequests {
			r.Stop()
		}
	}
	if st := a.Changes; st != nil {
		st.Request.Stop()
	}
}
func (a *Module) ContextChanged() {
	a.cancel()
	a.CloseMention()
	a.Files = nil
	a.Changes = nil
	a.QuickOpen = false
	a.VCSRequest.Stop()
	a.EnsureVcs()
	if a.started {
		a.startPolicyMonitor()
	}
	a.RefreshPolicy()
}
func (a *Module) Actions() []feature.Action {
	return []feature.Action{
		{ID: "folder", Title: "Open folder", Description: "Choose a workspace folder", Shortcut: "open-folder", Mods: ui.Cmd, Key: ui.KeyO, Icon: lucide.Folder(), MenuGroup: "File", MenuLabel: "Open Folder…", Run: a.host.OpenFolder},
		{ID: "files", Title: "Quick open file", Description: "Find and open a workspace file", Shortcut: "quick-open", Mods: ui.Cmd, Key: ui.KeyP, MenuGroup: "File", MenuLabel: "Quick Open File…", Slash: "/files", Icon: lucide.Folder(), Run: a.OpenQuickOpen},
		{ID: "changes", Title: "Open Changes", Description: "Review the working tree or a branch", Shortcut: "changes", Mods: ui.Ctrl | ui.Shift, Key: ui.KeyG, Slash: "/changes", Icon: lucide.GitBranch(), Run: func() { a.host.OpenPanel("Changes") }},
		{ID: "toggle-files", Title: "Toggle Files", Shortcut: "files", Mods: ui.Cmd | ui.Shift, Key: ui.KeyE, Run: a.ToggleFiles},
	}
}
func (a *Module) Panels() []feature.Panel {
	return []feature.Panel{
		{ID: "Files", Title: "Files", Description: "Browse the files in the current project.", Icon: lucide.Folder(), Open: a.OpenFiles, Render: func(c *ui.Context) { a.FilesBody(c, native.PaletteFor(c)) }, Refresh: a.FilesRefresh},
		{ID: "Changes", Title: "Changes", Description: "Review the files changed in this workspace.", Icon: lucide.GitBranch(), Open: func() { a.OpenChanges(changesWorking) }, Render: func(c *ui.Context) { a.ChangesBody(c, native.PaletteFor(c)) }, Refresh: func() { a.OpenChanges(a.ChangesMode()) }},
	}
}
func (a *Module) Dialogs(c *ui.Context) { a.QuickOpenDialog(c, native.PaletteFor(c)) }
func (a *Module) Modal() bool           { return a.QuickOpen }
func (a *Module) OpenFile(path string)  { a.OpenFiles(); a.host.OpenPanel("Files"); a.FilesRead(path) }
func (a *Module) Snapshot() json.RawMessage {
	raw, _ := json.Marshal(struct {
		FilesWrap, DiffSplit bool
		Recent               map[string][]RecentFile
	}{a.FilesWrap, a.DiffSplit, a.Recent})
	return raw
}
func (a *Module) Restore(raw json.RawMessage) {
	var state struct {
		FilesWrap, DiffSplit bool
		Recent               map[string][]RecentFile
	}
	if json.Unmarshal(raw, &state) == nil {
		a.FilesWrap = state.FilesWrap
		a.DiffSplit = state.DiffSplit
		if state.Recent != nil {
			a.Recent = state.Recent
		}
	}
}
func sameDir(a, b string) bool  { return filepath.Clean(a) == filepath.Clean(b) }
func dirBase(dir string) string { return filepath.Base(dir) }

func (a *Module) Dismiss() { a.QuickOpen = false }

func (a *Module) SyncComposer()                 { a.SyncMention() }
func (a *Module) ComposerOverlay(c *ui.Context) { a.MentionMenu(c, native.PaletteFor(c)) }
func (a *Module) BeforeSend() bool {
	if !a.MentionOpen() {
		return false
	}
	a.CompleteMention()
	return true
}
func (a *Module) ResetComposer() { a.CloseMention() }
func (a *Module) ComposerKey(k ui.TextKey) bool {
	if !a.MentionOpen() {
		return false
	}
	m, choices := &a.Mention, a.MentionChoices()
	if k.Key == ui.KeyEscape {
		a.MuteMention()
		return true
	}
	if k.Key == ui.KeyDown {
		if len(choices) > 0 {
			m.Selected = min(m.Selected+1, len(choices)-1)
		}
		return true
	}
	if k.Key == ui.KeyUp {
		if len(choices) > 0 {
			m.Selected = max(m.Selected-1, 0)
		}
		return true
	}
	return false
}

func (a *Module) Header(c *ui.Context) {
	if dir := a.host.Context().Directory; dir != "" {
		a.WorkspaceChip(c, native.PaletteFor(c), dir)
	}
}
func (a *Module) Connected() {
	a.EnsureVcs()
	if a.started {
		a.startPolicyMonitor()
	}
	a.RefreshPolicy()
}

type PolicyState struct {
	Request                       feature.Request
	Version, Route, WriteRevision uint64
	Remote                        bool
	Tail                          <-chan struct{}
	Dir, Level                    string
	Dirs                          chan workspace.PolicyTarget
	Writes                        chan workspace.PolicyChange
}

func (a *Module) RefreshPolicy() {
	snapshot := a.host.Context()
	dir := snapshot.Directory
	if snapshot.Local {
		if dir == "" {
			dir = snapshot.Home
		}
		a.WatchWorkspacePolicy(dir)
	} else {
		a.WatchServerPolicy(dir)
	}
}
func (a *Module) Start() {
	if a.started || a.closed {
		return
	}
	a.started = true
	a.startPolicyMonitor()
	a.RefreshPolicy()
}

func (a *Module) startPolicyMonitor() {
	if a.stopPolicy != nil || a.closed {
		return
	}
	snapshot := a.host.Context()
	if !snapshot.Local {
		return
	}
	monitor, ok := snapshot.Policy.(workspace.PolicyMonitor)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(a.host.Lifetime)
	a.stopPolicy = cancel
	go monitor.MonitorPolicy(ctx, a.Policy.Dirs, a.Policy.Writes, func(result workspace.PolicyResult) {
		a.host.Post(func() {
			if !a.closed {
				a.AcceptPolicy(result)
			}
		})
	})
	a.RefreshPolicy()
}
func (a *Module) ComposerActions(c *ui.Context, compact bool) {
	if a.host.Context().Policy == nil {
		return
	}
	if compact {
		a.FullAccessIcon(c, native.PaletteFor(c))
	} else {
		a.FullAccessButton(c, native.PaletteFor(c))
	}
}

func (a *Module) DraftContent() feature.DraftContent {
	return feature.DraftContent{Description: "Describe the change you want. The agent reads the workspace, plans, and edits.", Suggestions: []feature.Suggestion{
		{Title: "Explain this codebase", Detail: "A guided tour of the app's pieces", Prompt: "Explain this codebase", Icon: lucide.BookOpen()},
		{Title: "Fix a failing test", Detail: "Run the tests, find the break, patch it", Prompt: "Find the failing tests and fix them", Icon: lucide.Terminal()},
		{Title: "Review my changes", Detail: "Read the diff and flag the risks", Prompt: "Review my uncommitted changes", Icon: lucide.GitBranch()},
	}}
}
