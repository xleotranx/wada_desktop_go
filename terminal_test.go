package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTerminalPanelToggles drives both terminal keys: Ctrl+` opens the
// bottom panel and Cmd+J folds it away, as the reference app binds them.
// The shell factory fails in tests, so the panel shows its error face.
func TestTerminalPanelToggles(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	if !a.terminal.open {
		t.Fatal("Ctrl+` did not open the terminal")
	}
	if !tt.HasText("Terminal") || !tt.HasText("The terminal did not start") {
		t.Fatalf("the terminal panel is missing: %v", tt.Texts())
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.terminal.open {
		t.Fatal("the close button left the terminal open")
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !a.terminal.open {
		t.Fatal("Cmd+J did not reopen the terminal")
	}
}

// TestTerminalPanelResizes drags the terminal divider up to give the shell
// more room, and double-clicks it back to the opening height.
func TestTerminalPanelResizes(t *testing.T) {
	a := newTestApp(t)
	a.terminal.open = true
	a.ensureTerminal()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	divider, ok := tt.Find("Resize terminal")
	if !ok {
		t.Fatalf("the terminal divider is missing: %v", tt.Texts())
	}
	before := a.terminal.height
	x, y := divider.X+divider.W/2, divider.Y+divider.H/2
	tt.Press(x, y)
	tt.Move(x, y-70)
	tt.Release(x, y-70)
	tt.Frame()
	if got := a.terminal.height; got < before+60 || got > before+80 {
		t.Fatalf("dragging the terminal divider moved its height from %v to %v", before, got)
	}

	divider, ok = tt.Find("Resize terminal")
	if !ok {
		t.Fatal("the terminal divider left after resizing")
	}
	tt.ClickAt(divider.X+divider.W/2, divider.Y+divider.H/2)
	tt.ClickAt(divider.X+divider.W/2, divider.Y+divider.H/2)
	tt.Frame()
	if a.terminal.height != terminalDefaultHeight {
		t.Fatalf("double-clicking the terminal divider left height %v, want %v",
			a.terminal.height, terminalDefaultHeight)
	}
}

// TestTerminalTabs opens a second shell, switches between tabs, and
// closes them one at a time; folding the panel keeps the strips alive.
func TestTerminalTabs(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	if len(a.terminal.tabs) != 1 {
		t.Fatalf("opening the panel made %d tabs", len(a.terminal.tabs))
	}
	if err := tt.Click("New terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 2 || a.terminal.active != 1 {
		t.Fatalf("new tab: tabs=%d active=%d", len(a.terminal.tabs), a.terminal.active)
	}
	if !tt.HasText("Terminal 1") || !tt.HasText("Terminal 2") {
		t.Fatalf("the tab strip is missing a title: %v", tt.Texts())
	}
	if err := tt.Click("Terminal 1"); err != nil {
		t.Fatal(err)
	}
	if a.terminal.active != 0 {
		t.Fatalf("clicking Terminal 1 left active=%d", a.terminal.active)
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if a.terminal.open || len(a.terminal.tabs) != 2 {
		t.Fatalf("folding the panel: open=%v tabs=%d", a.terminal.open, len(a.terminal.tabs))
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !a.terminal.open || a.terminal.active != 0 {
		t.Fatalf("reopening the panel: open=%v active=%d", a.terminal.open, a.terminal.active)
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 1 || !a.terminal.open {
		t.Fatalf("closing a tab: tabs=%d open=%v", len(a.terminal.tabs), a.terminal.open)
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 0 || a.terminal.open {
		t.Fatalf("closing the last tab: tabs=%d open=%v", len(a.terminal.tabs), a.terminal.open)
	}
}

// TestTerminalMaximize gives the panel most of the window and restores
// the divider's height afterwards.
func TestTerminalMaximize(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	normal, ok := tt.Find("Maximize terminal")
	if !ok {
		t.Fatalf("no maximize control: %v", tt.Texts())
	}
	if err := tt.Click("Maximize terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.terminal.maximized {
		t.Fatal("the panel did not maximize")
	}
	large, ok := tt.Find("Restore terminal")
	if !ok {
		t.Fatalf("no restore control: %v", tt.Texts())
	}
	if large.Y >= normal.Y {
		t.Fatalf("maximized header Y=%v, normal Y=%v", large.Y, normal.Y)
	}
	if err := tt.Click("Restore terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.terminal.maximized {
		t.Fatal("the panel did not restore")
	}
	if a.terminal.height != terminalDefaultHeight {
		t.Fatalf("restored height %v", a.terminal.height)
	}
}
