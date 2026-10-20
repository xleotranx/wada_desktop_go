package main

import (
	"fmt"
	"os"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// The bottom panel's opening height, the room it keeps and the limit that
// leaves the conversation usable. The header holds its title and actions.
const (
	terminalDefaultHeight float32 = 220
	terminalMinHeight     float32 = 120
	terminalMaxHeight     float32 = 480
	terminalHeaderHeight  float32 = 34
)

// toggleTerminal folds the shell in or out. Opening it from the settings
// page returns to the workspace first; the shell itself keeps running when
// the panel is folded away, as in an editor's terminal.
func (a *app) toggleTerminal() {
	if a.terminal.open {
		a.terminal.open = false
		return
	}
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.terminal.open = true
	a.terminal.focus = true
	a.ensureTerminal()
}

// restartTerminal replaces the shell, closing the old one first. A Start
// from the error face of the panel also lands here.
func (a *app) restartTerminal() {
	a.terminal.open = true
	a.stopTerminal()
	a.terminal.err = ""
	a.terminal.exited = false
	a.terminal.exitCode = -1
	a.terminal.title = ""
	a.terminal.focus = true
	a.ensureTerminal()
}

// ensureTerminal starts the shell once, off the UI thread: loading the
// terminal's native library can take a moment the first time. The
// generation drops a shell whose start outlived a restart or the window.
func (a *app) ensureTerminal() {
	if a.terminal.term != nil || a.terminal.starting || a.terminal.err != "" {
		return
	}
	if a.runtime.background == nil || a.runtime.newTerminal == nil {
		a.terminal.err = "The terminal is not ready yet."
		return
	}
	dir := a.terminalDirectory()
	a.terminal.generation++
	generation := a.terminal.generation
	a.terminal.starting = true
	options := terminal.Options{
		Dir:  dir,
		Font: terminal.Font{Size: 12.5},
		OnTitle: func(title string) {
			a.publishTerminal(func() {
				if a.terminal.generation == generation {
					a.terminal.title = title
				}
			})
		},
		OnExit: func(code int) {
			a.publishTerminal(func() {
				if a.terminal.generation != generation {
					return
				}
				a.terminal.term = nil
				a.terminal.starting = false
				a.terminal.exited = true
				a.terminal.exitCode = code
			})
		},
	}
	a.runtime.background(func() func() {
		term, err := a.runtime.newTerminal(options)
		return func() {
			if a.runtime.closed || a.terminal.generation != generation {
				if term != nil {
					term.Close()
				}
				return
			}
			a.terminal.starting = false
			if err != nil {
				a.terminal.err = "The terminal did not start: " + err.Error()
				return
			}
			a.terminal.term = term
			a.terminal.title = "Terminal"
			a.terminal.focus = true
		}
	})
}

// stopTerminal closes the current shell and invalidates any start still in
// flight. It leaves the panel state alone for the caller to restore.
func (a *app) stopTerminal() {
	a.terminal.generation++
	if a.terminal.term != nil {
		a.terminal.term.Close()
		a.terminal.term = nil
	}
	a.terminal.starting = false
}

// closeTerminal releases the shell when the window closes.
func (a *app) closeTerminal() {
	a.terminal.generation++
	if a.terminal.term != nil {
		a.terminal.term.Close()
		a.terminal.term = nil
	}
	a.terminal.starting = false
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

// terminalPanel is the bottom pane: a compact toolbar over the shell, an
// error while the shell starts, or the exit notice that offers another.
func (a *app) terminalPanel(c *ui.Context, p palette) {
	ui.Column(c).FillWidth().FillHeight().Background(p.sidebarBg).Children(func() {
		ui.Row(c).FillWidth().Height(terminalHeaderHeight).Padding(0, 10).
			AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icTerminal).FontSize(14).TextColor(p.muted).Shrink(0)
			title := a.terminal.title
			if title == "" {
				title = "Terminal"
			}
			ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(p.text).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			switch {
			case a.terminal.starting:
				ui.Text(c, "Starting…").FontSize(11.5).TextColor(p.faint).SingleLine().Shrink(0)
			case a.terminal.exited:
				ui.Text(c, fmt.Sprintf("Exited (%d)", a.terminal.exitCode)).
					FontSize(11.5).TextColor(p.faint).SingleLine().Shrink(0)
			}
			ui.Spacer(c)
			a.browserIconButton(c, p, "New terminal", icPlus, a.restartTerminal, a.terminal.starting)
			a.browserIconButton(c, p, "Close terminal", icClose, func() {
				a.terminal.open = false
			}, false)
		})
		ui.Box(c).FillWidth().Height(1).Background(p.hairline)
		a.terminalBody(c, p)
	})
}

// terminalBody draws the shell or the one thing the panel can do next.
func (a *app) terminalBody(c *ui.Context, p palette) {
	if a.terminal.term != nil {
		ui.Box(c).Grow(1).FillWidth().Padding(4, 8).Children(func() {
			view := terminal.View(c, a.terminal.term)
			view.Fill()
			if a.terminal.focus {
				view.AutoFocus()
				a.terminal.focus = false
			}
		})
		return
	}
	ui.Column(c).Grow(1).FillWidth().Center().Gap(8).Padding(18).Children(func() {
		switch {
		case a.terminal.starting:
			ui.Text(c, "Starting the shell…").FontSize(13).TextColor(p.muted)
		case a.terminal.err != "":
			ui.Text(c, a.terminal.err).FontSize(12.5).TextColor(p.danger).
				TextAlign(ui.Center)
			if components.Button(c, "Try again", components.ButtonSecondary).Clicked() {
				a.restartTerminal()
			}
		case a.terminal.exited:
			ui.Text(c, fmt.Sprintf("The shell exited with code %d.", a.terminal.exitCode)).
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
