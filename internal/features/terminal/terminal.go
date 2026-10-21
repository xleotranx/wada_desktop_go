package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"

	term "github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// The bottom panel's opening height, the room it keeps and the limit that
// leaves the conversation usable. The header holds the tab strip and its
// actions.
const (
	terminalDefaultHeight float32 = 220
	terminalMinHeight     float32 = 120
	terminalMaxHeight     float32 = 480
	terminalHeaderHeight  float32 = 34
	terminalTabLimit              = 6
	// A maximized panel keeps this much conversation above it.
	terminalMaximizedChatMin float32 = 240
)

// terminalTabLabel is the name a tab shows before its shell names itself.
func TabLabel(tab *Tab) string {
	if tab.Custom != "" {
		return tab.Custom
	}
	if tab.Title != "" {
		return tab.Title
	}
	return fmt.Sprintf("Terminal %d", tab.Id)
}

// ActiveTerminal is the shell the panel shows, nil when no tab exists.
func (a *Module) ActiveTerminal() *Tab {
	if a.Active < 0 || a.Active >= len(a.Tabs) {
		return nil
	}
	return a.Tabs[a.Active]
}

// TerminalTabIndex finds one shell by its stable ID, -1 when it has left
// the strip.
func (a *Module) TerminalTabIndex(id uint64) int {
	for i, tab := range a.Tabs {
		if tab.Id == id {
			return i
		}
	}
	return -1
}

// AddTerminalTab appends a shell and makes it the one on show.
func (a *Module) AddTerminalTab() *Tab {
	a.NextID++
	tab := &Tab{Id: a.NextID, ExitCode: -1}
	a.Tabs = append(a.Tabs, tab)
	a.Active = len(a.Tabs) - 1
	return tab
}

// SelectTerminalTab shows one existing shell.
func (a *Module) SelectTerminalTab(index int) {
	if index < 0 || index >= len(a.Tabs) {
		return
	}
	a.Active = index
	a.Tabs[index].Focus = true
	a.EnsureTerminal()
}

// ToggleTerminalMaximize gives the panel most of the window, leaving a
// readable strip of conversation, and restores the divider's height.
func (a *Module) ToggleTerminalMaximize() {
	if !a.Maximized {
		a.SavedH = a.Height
		a.Maximized = true
		return
	}
	a.Maximized = false
	height := a.SavedH
	if height <= 0 {
		height = terminalDefaultHeight
	}
	a.Height = min(max(height, terminalMinHeight), terminalMaxHeight)
}

// ToggleTerminal folds the panel in or out. Opening it from the settings
// page returns to the workspace first; the shells keep running when the
// panel is folded away, as in an editor's terminal.
func (a *Module) ToggleTerminal() {
	if a.Open {
		a.Open = false
		return
	}
	a.host.Reveal()
	a.Open = true
	if tab := a.ActiveTerminal(); tab != nil {
		tab.Focus = true
	} else {
		a.AddTerminalTab()
	}
	a.EnsureTerminal()
}

// localShells lists the login shells this machine offers, the user's own
// first, from $SHELL and the system's /etc/shells. Missing programs are
// left out.
func LocalShells() []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	add(os.Getenv("SHELL"))
	if data, err := os.ReadFile("/etc/shells"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			add(line)
		}
	}
	return out
}

// NewTerminalTab opens another shell in the bottom panel.
func (a *Module) NewTerminalTab() {
	a.NewTerminalTabWithShell("")
}

// NewTerminalTabWithShell opens a shell of the chosen program; an empty
// shell lets the terminal run the user's login shell as usual.
func (a *Module) NewTerminalTabWithShell(shell string) {
	if len(a.Tabs) >= terminalTabLimit {
		return
	}
	a.host.Reveal()
	a.Open = true
	tab := a.AddTerminalTab()
	tab.Shell = shell
	a.EnsureTerminal()
}

// RestartTerminal replaces the active shell, closing it first. Start from
// the error and exit faces of the panel lands here too.
func (a *Module) RestartTerminal() {
	tab := a.ActiveTerminal()
	if tab == nil {
		if len(a.Tabs) >= terminalTabLimit {
			return
		}
		tab = a.AddTerminalTab()
	}
	a.Open = true
	a.StopTerminalTab(tab)
	tab.Err = ""
	tab.Exited = false
	tab.ExitCode = -1
	tab.Title = ""
	tab.Focus = true
	a.EnsureTerminal()
}

// EnsureTerminal starts the active shell once, off the UI thread: loading
// the terminal's native library can take a moment the first time. The
// generation drops a shell whose start outlived a restart, a close or the
// window.
func (a *Module) EnsureTerminal() {
	tab := a.ActiveTerminal()
	if tab == nil || tab.Term != nil || tab.Starting || tab.Err != "" {
		return
	}
	if a.host.Background == nil || a.NewTerminal == nil {
		tab.Err = "The terminal is not ready yet."
		return
	}
	dir := a.TerminalDirectory()
	tab.Generation++
	generation := tab.Generation
	tab.Starting = true
	id := tab.Id
	var command []string
	if tab.Shell != "" {
		command = []string{tab.Shell}
	}
	options := term.Options{
		Dir:     dir,
		Command: command,
		Font:    term.Font{Size: 12.5},
		OnTitle: func(title string) {
			a.PublishTerminal(func() {
				if index := a.TerminalTabIndex(id); index >= 0 && a.Tabs[index].Generation == generation {
					a.Tabs[index].Title = title
				}
			})
		},
		OnExit: func(code int) {
			a.PublishTerminal(func() {
				index := a.TerminalTabIndex(id)
				if index < 0 || a.Tabs[index].Generation != generation {
					return
				}
				current := a.Tabs[index]
				current.Term = nil
				current.Starting = false
				current.Exited = true
				current.ExitCode = code
			})
		},
	}
	a.host.Background(func() func() {
		if a.host.Lifetime.Err() != nil {
			return func() {}
		}
		term, err := a.NewTerminal(options)
		return func() {
			index := a.TerminalTabIndex(id)
			current := (*Tab)(nil)
			if index >= 0 {
				current = a.Tabs[index]
			}
			if a.closed || current == nil || current.Generation != generation {
				if term != nil {
					term.Close()
				}
				return
			}
			current.Starting = false
			if err != nil {
				current.Err = "The terminal did not start: " + err.Error()
				return
			}
			current.Term = term
			current.Title = TabLabel(current)
			current.Focus = true
		}
	})
}

// StopTerminalTab closes one shell and invalidates any start in flight.
func (a *Module) StopTerminalTab(tab *Tab) {
	tab.Generation++
	if tab.Term != nil {
		tab.Term.Close()
		tab.Term = nil
	}
	tab.Starting = false
}

// CloseTerminalTab stops the active shell and removes its tab; the last
// tab closes the panel.
func (a *Module) CloseTerminalTab() {
	a.CloseTerminalTabAt(a.Active)
}

// CloseTerminalTabAt removes one tab, keeping the shell on show when a
// different tab closes and handing it to a neighbour when this one goes.
func (a *Module) CloseTerminalTabAt(index int) {
	if index < 0 || index >= len(a.Tabs) {
		return
	}
	active := a.Active
	a.StopTerminalTab(a.Tabs[index])
	a.Tabs = append(a.Tabs[:index], a.Tabs[index+1:]...)
	if len(a.Tabs) == 0 {
		a.Active = -1
		a.Open = false
		return
	}
	if index < active {
		a.Active = active - 1
	} else if index == active {
		a.Active = min(index, len(a.Tabs)-1)
	}
	a.Tabs[a.Active].Focus = true
	a.EnsureTerminal()
}

// RenameState is the name dialog of one shell tab.
type RenameState struct {
	Open  bool
	Id    uint64
	Draft string
}

// RequestTerminalRename opens the name dialog for one shell tab.
func (a *Module) RequestTerminalRename(tab *Tab) {
	if tab == nil {
		return
	}
	a.Rename = &RenameState{Open: true, Id: tab.Id, Draft: tab.Custom}
	a.RenameFocus = true
}

// SaveTerminalRename keeps the typed name on its tab; an empty name hands
// the tab back to the shell's own title.
func (a *Module) SaveTerminalRename() {
	st := a.Rename
	if st == nil {
		return
	}
	if index := a.TerminalTabIndex(st.Id); index >= 0 {
		a.Tabs[index].Custom = strings.TrimSpace(st.Draft)
	}
	a.Rename = nil
}

// ClearTerminalName hands one tab back to its shell's title.
func (a *Module) ClearTerminalName(id uint64) {
	if index := a.TerminalTabIndex(id); index >= 0 {
		a.Tabs[index].Custom = ""
	}
}

// TerminalRenameDialog names one shell tab, with the shell's own title
// standing in until a name is kept.
func (a *Module) TerminalRenameDialog(c *ui.Context, p native.Palette) {
	st := a.Rename
	if st == nil || !st.Open {
		return
	}
	placeholder := "Terminal"
	if index := a.TerminalTabIndex(st.Id); index >= 0 {
		placeholder = TabLabel(a.Tabs[index])
	}
	native.Dialog(c, &st.Open, 380, "Rename terminal", func() {
		ui.Text(c, "Rename terminal").FontSize(14).FontWeight(600)
		ui.Text(c, "The name stays with this tab; emptying it brings the shell's own title back.").
			FontSize(12.5).TextColor(p.Muted)
		in := components.TextBox(c, &st.Draft, components.TextBoxOptions{}).
			FillWidth().Label("Terminal name").Placeholder(placeholder).FontSize(13)
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEnter {
				a.SaveTerminalRename()
				return true
			}
			return false
		})
		if a.RenameFocus {
			in.Focus()
			a.RenameFocus = false
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
				a.Rename = nil
			}
			if components.Button(c, "Save", components.ButtonPrimary).Clicked() {
				a.SaveTerminalRename()
			}
		})
	})
	if !st.Open && a.Rename == st {
		a.Rename = nil
	}
}

// CloseOtherTerminalTabs stops every shell but one, which stays on show.
func (a *Module) CloseOtherTerminalTabs(index int) {
	if index < 0 || index >= len(a.Tabs) {
		return
	}
	kept := a.Tabs[index]
	for i, tab := range a.Tabs {
		if i != index {
			a.StopTerminalTab(tab)
		}
	}
	a.Tabs = []*Tab{kept}
	a.Active = 0
	kept.Focus = true
	a.EnsureTerminal()
}

// CloseAllTerminalTabs stops every shell and folds the panel.
func (a *Module) CloseAllTerminalTabs() {
	for _, tab := range a.Tabs {
		a.StopTerminalTab(tab)
	}
	a.Tabs = nil
	a.Active = -1
	a.Open = false
}

// CloseTerminal releases every shell when the window closes.
func (a *Module) CloseTerminal() {
	for _, tab := range a.Tabs {
		a.StopTerminalTab(tab)
	}
	a.Tabs = nil
	a.Active = -1
}

// PublishTerminal posts a shell callback's result to the UI thread. A
// window already gone drops it; tests without one run it here.
func (a *Module) PublishTerminal(fn func()) {
	a.host.Post(func() {
		if !a.closed {
			fn()
		}
	})
}

// TerminalDirectory picks where a new shell starts: the open chat's local
// folder when it exists, else the user's home. A remote server's paths are
// not this machine's, so a remote chat starts at home.
func (a *Module) TerminalDirectory() string {
	exists := func(dir string) bool {
		info, err := os.Stat(dir)
		return err == nil && info.IsDir()
	}
	if a.host.Context().Local {
		if dir := a.host.Context().Directory; dir != "" && exists(dir) {
			return dir
		}
	}
	if a.host.Context().Home != "" && exists(a.host.Context().Home) {
		return a.host.Context().Home
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

// TerminalPanel is the bottom pane: a tab strip over the active shell, a
// start face while it loads, or the error and exit notices that offer
// another.
func (a *Module) TerminalPanel(c *ui.Context, p native.Palette) {
	ui.Column(c).FillWidth().FillHeight().Background(p.SidebarBg).Children(func() {
		a.TerminalHeader(c, p)
		ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
		a.TerminalBody(c, p)
	})
}

func (a *Module) TerminalHeader(c *ui.Context, p native.Palette) {
	ui.Row(c).FillWidth().Height(terminalHeaderHeight).Padding(0, 10).
		AlignItems(ui.Center).Gap(5).Children(func() {
		ui.Icon(c, lucide.Terminal()).FontSize(14).TextColor(p.Muted).Shrink(0)
		// A menu choice closes shells while this strip is drawn, so walk a
		// copy rather than the state it rewrites.
		for i, tab := range slices.Clone(a.Tabs) {
			a.TerminalTabButton(c, p, i, tab)
		}
		ui.Spacer(c)
		maxLabel, maxIcon := "Maximize terminal", lucide.Maximize2()
		if a.Maximized {
			maxLabel, maxIcon = "Restore terminal", lucide.Minimize2()
		}
		native.IconButton(c, p, maxLabel, maxIcon, a.ToggleTerminalMaximize, false)
		if len(a.Shells) > 0 {
			c.Key("terminal-shells")
			shell := ui.ButtonBase(c).Label("New terminal with a shell").Size(22, 22).
				Radius(6).Center().Tooltip("Choose a shell for a new terminal")
			if shell.Hovered() {
				shell.Background(p.Hover)
			}
			shell.Menu(func(m *ui.Menu) {
				for _, program := range a.Shells {
					label := "New terminal: " + filepath.Base(program)
					if m.Item(label).Chosen() {
						a.NewTerminalTabWithShell(program)
					}
				}
			})
			shell.Children(func() { ui.Icon(c, lucide.ChevronDown()).FontSize(12).TextColor(p.Muted) })
		}
		native.IconButton(c, p, "New terminal", lucide.Plus(), a.NewTerminalTab,
			len(a.Tabs) >= terminalTabLimit)
		native.IconButton(c, p, "Close terminal", lucide.X(), a.CloseTerminalTab,
			len(a.Tabs) == 0)
	})
}

func (a *Module) TerminalTabButton(c *ui.Context, p native.Palette, index int, tab *Tab) {
	active := index == a.Active
	c.Key(fmt.Sprintf("terminal-tab:%d", tab.Id))
	label := TabLabel(tab)
	row := ui.Row(c).Height(24).MaxWidth(190).Shrink(1).MinWidth(0).
		Radius(6).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.Selected)
	} else if row.Hovered() {
		row.Background(p.Hover)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename tab…").Chosen() {
			a.RequestTerminalRename(tab)
		}
		if tab.Custom != "" && m.Item("Clear name").Chosen() {
			a.ClearTerminalName(tab.Id)
		}
		m.Separator()
		if m.Item("Close tab").Chosen() {
			a.CloseTerminalTabAt(index)
		}
		if m.Item("Close other tabs").Chosen() {
			a.CloseOtherTerminalTabs(index)
		}
		if m.Item("Close all tabs").Chosen() {
			a.CloseAllTerminalTabs()
		}
	})
	row.Children(func() {
		open := ui.ButtonBase(c).Label(label).Height(24).Radius(6).Padding(0, 8).
			Grow(1).Shrink(1).MinWidth(0).Tooltip(label)
		open.OnClick(func() { a.SelectTerminalTab(index) })
		open.Children(func() {
			tint := p.Muted
			if active {
				tint = p.Text
			}
			ui.Text(c, label).FontSize(11.5).TextColor(tint).SingleLine().Ellipsis("…")
		})
		close := ui.ButtonBase(c).Label("Close "+label).Size(18, 18).Radius(5).Center().
			Tooltip("Close " + label)
		if close.Hovered() {
			close.Background(p.Hover)
		}
		close.OnClick(func() { a.CloseTerminalTabAt(index) })
		close.Children(func() {
			ui.Icon(c, lucide.X()).FontSize(9).TextColor(p.Faint)
		})
	})
}

// TerminalBody draws the active shell or the one thing the panel can do
// next.
func (a *Module) TerminalBody(c *ui.Context, p native.Palette) {
	tab := a.ActiveTerminal()
	if tab != nil && tab.Term != nil {
		ui.Box(c).Grow(1).FillWidth().Padding(4, 8).Children(func() {
			view := term.View(c, tab.Term)
			view.Fill()
			if tab.Focus {
				view.AutoFocus()
				tab.Focus = false
			}
		})
		return
	}
	ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Padding(18).Children(func() {
		switch {
		case tab == nil:
			ui.Text(c, "Open a shell in this workspace.").FontSize(13).TextColor(p.Muted)
			if components.Button(c, "Start a shell", components.ButtonPrimary).Clicked() {
				a.NewTerminalTab()
			}
		case tab.Starting:
			ui.Text(c, "Starting the shell…").FontSize(13).TextColor(p.Muted)
		case tab.Err != "":
			ui.Text(c, tab.Err).FontSize(12.5).TextColor(p.Danger).
				TextAlign(ui.Center)
			if components.Button(c, "Try again", components.ButtonSecondary).Clicked() {
				a.RestartTerminal()
			}
		case tab.Exited:
			ui.Text(c, fmt.Sprintf("The shell exited with code %d.", tab.ExitCode)).
				FontSize(13).TextColor(p.Muted)
			if components.Button(c, "Start a new shell", components.ButtonPrimary).Clicked() {
				a.RestartTerminal()
			}
		default:
			ui.Text(c, "Open a shell in this workspace.").FontSize(13).TextColor(p.Muted)
			if components.Button(c, "Start a shell", components.ButtonPrimary).Clicked() {
				a.EnsureTerminal()
			}
		}
	})
}

// terminalTab is one shell in the bottom panel.
type Tab struct {
	Id         uint64
	Term       *term.Terminal
	Title      string
	Shell      string
	Custom     string
	Starting   bool
	Exited     bool
	ExitCode   int
	Err        string
	Generation uint64
	Focus      bool
}

// Module owns shells, tab identity, dialogs and panel geometry.
type Module struct {
	stop        context.CancelFunc
	host        feature.Host
	Open        bool
	Height      float32
	Maximized   bool
	SavedH      float32
	Tabs        []*Tab
	Active      int
	NextID      uint64
	Shells      []string
	Rename      *RenameState
	RenameFocus bool
	NewTerminal func(term.Options) (*term.Terminal, error)
	closed      bool
}

func New(h feature.Host) feature.Module {
	lifetime, cancel := context.WithCancel(h.Lifetime)
	h.Lifetime = lifetime
	return &Module{stop: cancel, host: h, Height: terminalDefaultHeight, Active: -1, Shells: LocalShells(), NewTerminal: term.New}
}
func (a *Module) ID() string { return "terminal" }
func (a *Module) Close() {
	if a.closed {
		return
	}
	a.closed = true
	a.stop()
	a.CloseTerminal()
}
func (a *Module) IsOpen() bool      { return a.Open }
func (a *Module) SetOpen(open bool) { a.Open = open }
func (a *Module) Toggle()           { a.ToggleTerminal() }
func (a *Module) ResetSize()        { a.Height = terminalDefaultHeight }
func (a *Module) Actions() []feature.Action {
	return []feature.Action{{ID: "terminal", Title: "Toggle terminal", Description: "Open the shell beneath the chat", ExtraKeys: []feature.KeyBinding{{Mods: ui.Ctrl, Key: ui.KeyBackquote}}, Shortcut: "terminal", Mods: ui.Cmd, Key: ui.KeyJ, MenuGroup: "View", MenuLabel: "Toggle Terminal", Slash: "/terminal", Icon: lucide.Terminal(), Run: a.ToggleTerminal}}
}
func (a *Module) Dialogs(c *ui.Context) { a.TerminalRenameDialog(c, native.PaletteFor(c)) }
func (a *Module) Modal() bool           { return a.Rename != nil && a.Rename.Open }
func (a *Module) Snapshot() json.RawMessage {
	raw, _ := json.Marshal(struct{ Height float32 }{a.Height})
	return raw
}
func (a *Module) Restore(raw json.RawMessage) {
	var state struct{ Height float32 }
	if json.Unmarshal(raw, &state) == nil && state.Height > 0 {
		a.Height = min(max(state.Height, terminalMinHeight), terminalMaxHeight)
	}
}
func (a *Module) Layout(c *ui.Context, headerH float32, main func()) {
	if !a.Open {
		main()
		return
	}
	_, windowH := c.Size()
	avail := max(windowH-headerH, 2)
	panelH := a.Height
	if a.Maximized {
		panelH = max(terminalMinHeight, avail-1-terminalMaximizedChatMin)
	}
	contentH := avail - 1 - panelH
	split := components.Split(c, &contentH, main, func() { a.TerminalPanel(c, native.PaletteFor(c)) }, components.SplitOptions{Orientation: components.Vertical, FirstMin: 220, SecondMin: terminalMinHeight, Default: max(0, avail-1-terminalDefaultHeight)})
	split.Label("Resize terminal").Fill()
	if split.Changed() {
		a.Maximized = false
		a.Height = min(max(avail-1-contentH, terminalMinHeight), terminalMaxHeight)
		a.host.Save()
	}
}

func (a *Module) Dismiss() { a.Rename = nil }

func (a *Module) Start() {
	if os.Getenv("WADA_CAPTURE") == "" {
		return
	}
	if value, ok := os.LookupEnv("WADA_TERMINAL"); ok {
		n, _ := strconv.Atoi(value)
		n = min(max(n, 1), terminalTabLimit)
		a.ToggleTerminal()
		for range n - 1 {
			a.NewTerminalTab()
		}
	}
	if _, ok := os.LookupEnv("WADA_TERMINAL_MAX"); ok {
		if !a.Open {
			a.ToggleTerminal()
		}
		if !a.Maximized {
			a.ToggleTerminalMaximize()
		}
	}
}
