package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
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
func terminalTabLabel(tab *terminalTab) string {
	if tab.custom != "" {
		return tab.custom
	}
	if tab.title != "" {
		return tab.title
	}
	return fmt.Sprintf("Terminal %d", tab.id)
}

// activeTerminal is the shell the panel shows, nil when no tab exists.
func (a *app) activeTerminal() *terminalTab {
	if a.terminal.active < 0 || a.terminal.active >= len(a.terminal.tabs) {
		return nil
	}
	return a.terminal.tabs[a.terminal.active]
}

// terminalTabIndex finds one shell by its stable ID, -1 when it has left
// the strip.
func (a *app) terminalTabIndex(id uint64) int {
	for i, tab := range a.terminal.tabs {
		if tab.id == id {
			return i
		}
	}
	return -1
}

// addTerminalTab appends a shell and makes it the one on show.
func (a *app) addTerminalTab() *terminalTab {
	a.terminal.nextID++
	tab := &terminalTab{id: a.terminal.nextID, exitCode: -1}
	a.terminal.tabs = append(a.terminal.tabs, tab)
	a.terminal.active = len(a.terminal.tabs) - 1
	return tab
}

// selectTerminalTab shows one existing shell.
func (a *app) selectTerminalTab(index int) {
	if index < 0 || index >= len(a.terminal.tabs) {
		return
	}
	a.terminal.active = index
	a.terminal.tabs[index].focus = true
	a.ensureTerminal()
}

// toggleTerminalMaximize gives the panel most of the window, leaving a
// readable strip of conversation, and restores the divider's height.
func (a *app) toggleTerminalMaximize() {
	if !a.terminal.maximized {
		a.terminal.savedH = a.terminal.height
		a.terminal.maximized = true
		return
	}
	a.terminal.maximized = false
	height := a.terminal.savedH
	if height <= 0 {
		height = terminalDefaultHeight
	}
	a.terminal.height = min(max(height, terminalMinHeight), terminalMaxHeight)
}

// toggleTerminal folds the panel in or out. Opening it from the settings
// page returns to the workspace first; the shells keep running when the
// panel is folded away, as in an editor's terminal.
func (a *app) toggleTerminal() {
	if a.terminal.open {
		a.terminal.open = false
		return
	}
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.terminal.open = true
	if tab := a.activeTerminal(); tab != nil {
		tab.focus = true
	} else {
		a.addTerminalTab()
	}
	a.ensureTerminal()
}

// localShells lists the login shells this machine offers, the user's own
// first, from $SHELL and the system's /etc/shells. Missing programs are
// left out.
func localShells() []string {
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

// newTerminalTab opens another shell in the bottom panel.
func (a *app) newTerminalTab() {
	a.newTerminalTabWithShell("")
}

// newTerminalTabWithShell opens a shell of the chosen program; an empty
// shell lets the terminal run the user's login shell as usual.
func (a *app) newTerminalTabWithShell(shell string) {
	if len(a.terminal.tabs) >= terminalTabLimit {
		return
	}
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.terminal.open = true
	tab := a.addTerminalTab()
	tab.shell = shell
	a.ensureTerminal()
}

// restartTerminal replaces the active shell, closing it first. Start from
// the error and exit faces of the panel lands here too.
func (a *app) restartTerminal() {
	tab := a.activeTerminal()
	if tab == nil {
		if len(a.terminal.tabs) >= terminalTabLimit {
			return
		}
		tab = a.addTerminalTab()
	}
	a.terminal.open = true
	a.stopTerminalTab(tab)
	tab.err = ""
	tab.exited = false
	tab.exitCode = -1
	tab.title = ""
	tab.focus = true
	a.ensureTerminal()
}

// ensureTerminal starts the active shell once, off the UI thread: loading
// the terminal's native library can take a moment the first time. The
// generation drops a shell whose start outlived a restart, a close or the
// window.
func (a *app) ensureTerminal() {
	tab := a.activeTerminal()
	if tab == nil || tab.term != nil || tab.starting || tab.err != "" {
		return
	}
	if a.runtime.background == nil || a.runtime.newTerminal == nil {
		tab.err = "The terminal is not ready yet."
		return
	}
	dir := a.terminalDirectory()
	tab.generation++
	generation := tab.generation
	tab.starting = true
	id := tab.id
	var command []string
	if tab.shell != "" {
		command = []string{tab.shell}
	}
	options := terminal.Options{
		Dir:     dir,
		Command: command,
		Font:    terminal.Font{Size: 12.5},
		OnTitle: func(title string) {
			a.publishTerminal(func() {
				if index := a.terminalTabIndex(id); index >= 0 && a.terminal.tabs[index].generation == generation {
					a.terminal.tabs[index].title = title
				}
			})
		},
		OnExit: func(code int) {
			a.publishTerminal(func() {
				index := a.terminalTabIndex(id)
				if index < 0 || a.terminal.tabs[index].generation != generation {
					return
				}
				current := a.terminal.tabs[index]
				current.term = nil
				current.starting = false
				current.exited = true
				current.exitCode = code
			})
		},
	}
	a.runtime.background(func() func() {
		term, err := a.runtime.newTerminal(options)
		return func() {
			index := a.terminalTabIndex(id)
			current := (*terminalTab)(nil)
			if index >= 0 {
				current = a.terminal.tabs[index]
			}
			if a.runtime.closed || current == nil || current.generation != generation {
				if term != nil {
					term.Close()
				}
				return
			}
			current.starting = false
			if err != nil {
				current.err = "The terminal did not start: " + err.Error()
				return
			}
			current.term = term
			current.title = terminalTabLabel(current)
			current.focus = true
		}
	})
}

// stopTerminalTab closes one shell and invalidates any start in flight.
func (a *app) stopTerminalTab(tab *terminalTab) {
	tab.generation++
	if tab.term != nil {
		tab.term.Close()
		tab.term = nil
	}
	tab.starting = false
}

// closeTerminalTab stops the active shell and removes its tab; the last
// tab closes the panel.
func (a *app) closeTerminalTab() {
	a.closeTerminalTabAt(a.terminal.active)
}

// closeTerminalTabAt removes one tab, keeping the shell on show when a
// different tab closes and handing it to a neighbour when this one goes.
func (a *app) closeTerminalTabAt(index int) {
	if index < 0 || index >= len(a.terminal.tabs) {
		return
	}
	active := a.terminal.active
	a.stopTerminalTab(a.terminal.tabs[index])
	a.terminal.tabs = append(a.terminal.tabs[:index], a.terminal.tabs[index+1:]...)
	if len(a.terminal.tabs) == 0 {
		a.terminal.active = -1
		a.terminal.open = false
		return
	}
	if index < active {
		a.terminal.active = active - 1
	} else if index == active {
		a.terminal.active = min(index, len(a.terminal.tabs)-1)
	}
	a.terminal.tabs[a.terminal.active].focus = true
	a.ensureTerminal()
}

// terminalRenameState is the name dialog of one shell tab.
type terminalRenameState struct {
	open  bool
	id    uint64
	draft string
}

// requestTerminalRename opens the name dialog for one shell tab.
func (a *app) requestTerminalRename(tab *terminalTab) {
	if tab == nil {
		return
	}
	a.layout.terminalRename = &terminalRenameState{open: true, id: tab.id, draft: tab.custom}
	a.layout.terminalRenameFocus = true
}

// saveTerminalRename keeps the typed name on its tab; an empty name hands
// the tab back to the shell's own title.
func (a *app) saveTerminalRename() {
	st := a.layout.terminalRename
	if st == nil {
		return
	}
	if index := a.terminalTabIndex(st.id); index >= 0 {
		a.terminal.tabs[index].custom = strings.TrimSpace(st.draft)
	}
	a.layout.terminalRename = nil
}

// clearTerminalName hands one tab back to its shell's title.
func (a *app) clearTerminalName(id uint64) {
	if index := a.terminalTabIndex(id); index >= 0 {
		a.terminal.tabs[index].custom = ""
	}
}

// terminalRenameDialog names one shell tab, with the shell's own title
// standing in until a name is kept.
func (a *app) terminalRenameDialog(c *ui.Context, p palette) {
	st := a.layout.terminalRename
	if st == nil || !st.open {
		return
	}
	placeholder := "Terminal"
	if index := a.terminalTabIndex(st.id); index >= 0 {
		placeholder = terminalTabLabel(a.terminal.tabs[index])
	}
	dialog(c, p, &st.open, 380, "Rename terminal", func() {
		ui.Text(c, "Rename terminal").FontSize(14).FontWeight(600)
		ui.Text(c, "The name stays with this tab; emptying it brings the shell's own title back.").
			FontSize(12.5).TextColor(p.muted)
		in := components.TextBox(c, &st.draft, components.TextBoxOptions{}).
			FillWidth().Label("Terminal name").Placeholder(placeholder).FontSize(13)
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEnter {
				a.saveTerminalRename()
				return true
			}
			return false
		})
		if a.layout.terminalRenameFocus {
			in.Focus()
			a.layout.terminalRenameFocus = false
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
				a.layout.terminalRename = nil
			}
			if components.Button(c, "Save", components.ButtonPrimary).Clicked() {
				a.saveTerminalRename()
			}
		})
	})
	if !st.open && a.layout.terminalRename == st {
		a.layout.terminalRename = nil
	}
}

// closeOtherTerminalTabs stops every shell but one, which stays on show.
func (a *app) closeOtherTerminalTabs(index int) {
	if index < 0 || index >= len(a.terminal.tabs) {
		return
	}
	kept := a.terminal.tabs[index]
	for i, tab := range a.terminal.tabs {
		if i != index {
			a.stopTerminalTab(tab)
		}
	}
	a.terminal.tabs = []*terminalTab{kept}
	a.terminal.active = 0
	kept.focus = true
	a.ensureTerminal()
}

// closeAllTerminalTabs stops every shell and folds the panel.
func (a *app) closeAllTerminalTabs() {
	for _, tab := range a.terminal.tabs {
		a.stopTerminalTab(tab)
	}
	a.terminal.tabs = nil
	a.terminal.active = -1
	a.terminal.open = false
}

// closeTerminal releases every shell when the window closes.
func (a *app) closeTerminal() {
	for _, tab := range a.terminal.tabs {
		a.stopTerminalTab(tab)
	}
	a.terminal.tabs = nil
	a.terminal.active = -1
}

// publishTerminal posts a shell callback's result to the UI thread. A
// window already gone drops it; tests without one run it here.
func (a *app) publishTerminal(fn func()) {
	if win := a.runtime.win; win != nil {
		win.Update(fn)
		return
	}
	fn()
}

// terminalDirectory picks where a new shell starts: the open chat's local
// folder when it exists, else the user's home. A remote server's paths are
// not this machine's, so a remote chat starts at home.
func (a *app) terminalDirectory() string {
	exists := func(dir string) bool {
		info, err := os.Stat(dir)
		return err == nil && info.IsDir()
	}
	if a.localActive() {
		if dir := a.chatDir(); dir != "" && exists(dir) {
			return dir
		}
	}
	if a.preferences.home != "" && exists(a.preferences.home) {
		return a.preferences.home
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

// terminalPanel is the bottom pane: a tab strip over the active shell, a
// start face while it loads, or the error and exit notices that offer
// another.
func (a *app) terminalPanel(c *ui.Context, p palette) {
	ui.Column(c).FillWidth().FillHeight().Background(p.sidebarBg).Children(func() {
		a.terminalHeader(c, p)
		ui.Box(c).FillWidth().Height(1).Background(p.hairline)
		a.terminalBody(c, p)
	})
}

func (a *app) terminalHeader(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(terminalHeaderHeight).Padding(0, 10).
		AlignItems(ui.Center).Gap(5).Children(func() {
		ui.Icon(c, icTerminal).FontSize(14).TextColor(p.muted).Shrink(0)
		// A menu choice closes shells while this strip is drawn, so walk a
		// copy rather than the state it rewrites.
		for i, tab := range slices.Clone(a.terminal.tabs) {
			a.terminalTabButton(c, p, i, tab)
		}
		ui.Spacer(c)
		maxLabel, maxIcon := "Maximize terminal", icExpand
		if a.terminal.maximized {
			maxLabel, maxIcon = "Restore terminal", icCollapse
		}
		a.browserIconButton(c, p, maxLabel, maxIcon, a.toggleTerminalMaximize, false)
		if len(a.terminal.shells) > 0 {
			c.Key("terminal-shells")
			shell := ui.ButtonBase(c).Label("New terminal with a shell").Size(22, 22).
				Radius(6).Center().Tooltip("Choose a shell for a new terminal")
			if shell.Hovered() {
				shell.Background(p.hover)
			}
			shell.Menu(func(m *ui.Menu) {
				for _, program := range a.terminal.shells {
					label := "New terminal: " + filepath.Base(program)
					if m.Item(label).Chosen() {
						a.newTerminalTabWithShell(program)
					}
				}
			})
			shell.Children(func() { ui.Icon(c, icChevDown).FontSize(12).TextColor(p.muted) })
		}
		a.browserIconButton(c, p, "New terminal", icPlus, a.newTerminalTab,
			len(a.terminal.tabs) >= terminalTabLimit)
		a.browserIconButton(c, p, "Close terminal", icClose, a.closeTerminalTab,
			len(a.terminal.tabs) == 0)
	})
}

func (a *app) terminalTabButton(c *ui.Context, p palette, index int, tab *terminalTab) {
	active := index == a.terminal.active
	c.Key(fmt.Sprintf("terminal-tab:%d", tab.id))
	label := terminalTabLabel(tab)
	row := ui.Row(c).Height(24).MaxWidth(190).Shrink(1).MinWidth(0).
		Radius(6).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.selected)
	} else if row.Hovered() {
		row.Background(p.hover)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename tab…").Chosen() {
			a.requestTerminalRename(tab)
		}
		if tab.custom != "" && m.Item("Clear name").Chosen() {
			a.clearTerminalName(tab.id)
		}
		m.Separator()
		if m.Item("Close tab").Chosen() {
			a.closeTerminalTabAt(index)
		}
		if m.Item("Close other tabs").Chosen() {
			a.closeOtherTerminalTabs(index)
		}
		if m.Item("Close all tabs").Chosen() {
			a.closeAllTerminalTabs()
		}
	})
	row.Children(func() {
		open := ui.ButtonBase(c).Label(label).Height(24).Radius(6).Padding(0, 8).
			Grow(1).Shrink(1).MinWidth(0).Tooltip(label)
		open.OnClick(func() { a.selectTerminalTab(index) })
		open.Children(func() {
			tint := p.muted
			if active {
				tint = p.text
			}
			ui.Text(c, label).FontSize(11.5).TextColor(tint).SingleLine().Ellipsis("…")
		})
		close := ui.ButtonBase(c).Label("Close "+label).Size(18, 18).Radius(5).Center().
			Tooltip("Close " + label)
		if close.Hovered() {
			close.Background(p.hover)
		}
		close.OnClick(func() { a.closeTerminalTabAt(index) })
		close.Children(func() {
			ui.Icon(c, icClose).FontSize(9).TextColor(p.faint)
		})
	})
}

// terminalBody draws the active shell or the one thing the panel can do
// next.
func (a *app) terminalBody(c *ui.Context, p palette) {
	tab := a.activeTerminal()
	if tab != nil && tab.term != nil {
		ui.Box(c).Grow(1).FillWidth().Padding(4, 8).Children(func() {
			view := terminal.View(c, tab.term)
			view.Fill()
			if tab.focus {
				view.AutoFocus()
				tab.focus = false
			}
		})
		return
	}
	ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Padding(18).Children(func() {
		switch {
		case tab == nil:
			ui.Text(c, "Open a shell in this workspace.").FontSize(13).TextColor(p.muted)
			if components.Button(c, "Start a shell", components.ButtonPrimary).Clicked() {
				a.newTerminalTab()
			}
		case tab.starting:
			ui.Text(c, "Starting the shell…").FontSize(13).TextColor(p.muted)
		case tab.err != "":
			ui.Text(c, tab.err).FontSize(12.5).TextColor(p.danger).
				TextAlign(ui.Center)
			if components.Button(c, "Try again", components.ButtonSecondary).Clicked() {
				a.restartTerminal()
			}
		case tab.exited:
			ui.Text(c, fmt.Sprintf("The shell exited with code %d.", tab.exitCode)).
				FontSize(13).TextColor(p.muted)
			if components.Button(c, "Start a new shell", components.ButtonPrimary).Clicked() {
				a.restartTerminal()
			}
		default:
			ui.Text(c, "Open a shell in this workspace.").FontSize(13).TextColor(p.muted)
			if components.Button(c, "Start a shell", components.ButtonPrimary).Clicked() {
				a.ensureTerminal()
			}
		}
	})
}
