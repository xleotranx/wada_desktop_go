package main

import (
	"fmt"
	"os"

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
)

// terminalTabLabel is the name a tab shows before its shell names itself.
func terminalTabLabel(tab *terminalTab) string {
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

// newTerminalTab opens another shell in the bottom panel.
func (a *app) newTerminalTab() {
	if len(a.terminal.tabs) >= terminalTabLimit {
		return
	}
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.terminal.open = true
	a.addTerminalTab()
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
	options := terminal.Options{
		Dir:  dir,
		Font: terminal.Font{Size: 12.5},
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
	index := a.terminal.active
	if index < 0 || index >= len(a.terminal.tabs) {
		return
	}
	a.stopTerminalTab(a.terminal.tabs[index])
	a.terminal.tabs = append(a.terminal.tabs[:index], a.terminal.tabs[index+1:]...)
	if len(a.terminal.tabs) == 0 {
		a.terminal.active = -1
		a.terminal.open = false
		return
	}
	a.terminal.active = min(index, len(a.terminal.tabs)-1)
	a.terminal.tabs[a.terminal.active].focus = true
	a.ensureTerminal()
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
		for i, tab := range a.terminal.tabs {
			a.terminalTabButton(c, p, i, tab)
		}
		ui.Spacer(c)
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
	b := ui.ButtonBase(c).Label(label).Height(24).Radius(6).Padding(0, 8).
		AlignItems(ui.Center).Shrink(1).MinWidth(0).MaxWidth(170).Tooltip(label)
	if active {
		b.Background(p.selected)
	}
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.selectTerminalTab(index) })
	b.Children(func() {
		tint := p.muted
		if active {
			tint = p.text
		}
		ui.Text(c, label).FontSize(11.5).TextColor(tint).SingleLine().Ellipsis("…")
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
