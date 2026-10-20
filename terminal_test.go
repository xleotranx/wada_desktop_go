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
