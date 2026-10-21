package desktop

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
	if !terminalModule(a).Open {
		t.Fatal("Ctrl+` did not open the terminal")
	}
	if !tt.HasText("Terminal") || !tt.HasText("The terminal did not start") {
		t.Fatalf("the terminal panel is missing: %v", tt.Texts())
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if terminalModule(a).Open {
		t.Fatal("the close button left the terminal open")
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !terminalModule(a).Open {
		t.Fatal("Cmd+J did not reopen the terminal")
	}
}

// TestTerminalPanelResizes drags the terminal divider up to give the shell
// more room, and double-clicks it back to the opening height.
func TestTerminalPanelResizes(t *testing.T) {
	a := newTestApp(t)
	terminalModule(a).Open = true
	a.ensureTerminal()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	divider, ok := tt.Find("Resize terminal")
	if !ok {
		t.Fatalf("the terminal divider is missing: %v", tt.Texts())
	}
	before := terminalModule(a).Height
	x, y := divider.X+divider.W/2, divider.Y+divider.H/2
	tt.Press(x, y)
	tt.Move(x, y-70)
	tt.Release(x, y-70)
	tt.Frame()
	if got := terminalModule(a).Height; got < before+60 || got > before+80 {
		t.Fatalf("dragging the terminal divider moved its height from %v to %v", before, got)
	}

	divider, ok = tt.Find("Resize terminal")
	if !ok {
		t.Fatal("the terminal divider left after resizing")
	}
	tt.ClickAt(divider.X+divider.W/2, divider.Y+divider.H/2)
	tt.ClickAt(divider.X+divider.W/2, divider.Y+divider.H/2)
	tt.Frame()
	if terminalModule(a).Height != terminalDefaultHeight {
		t.Fatalf("double-clicking the terminal divider left height %v, want %v",
			terminalModule(a).Height, terminalDefaultHeight)
	}
}

// TestTerminalTabs opens a second shell, switches between tabs, and
// closes them one at a time; folding the panel keeps the strips alive.
func TestTerminalTabs(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	if len(terminalModule(a).Tabs) != 1 {
		t.Fatalf("opening the panel made %d tabs", len(terminalModule(a).Tabs))
	}
	if err := tt.Click("New terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 2 || terminalModule(a).Active != 1 {
		t.Fatalf("new tab: tabs=%d active=%d", len(terminalModule(a).Tabs), terminalModule(a).Active)
	}
	if !tt.HasText("Terminal 1") || !tt.HasText("Terminal 2") {
		t.Fatalf("the tab strip is missing a title: %v", tt.Texts())
	}
	if err := tt.Click("Terminal 1"); err != nil {
		t.Fatal(err)
	}
	if terminalModule(a).Active != 0 {
		t.Fatalf("clicking Terminal 1 left active=%d", terminalModule(a).Active)
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if terminalModule(a).Open || len(terminalModule(a).Tabs) != 2 {
		t.Fatalf("folding the panel: open=%v tabs=%d", terminalModule(a).Open, len(terminalModule(a).Tabs))
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !terminalModule(a).Open || terminalModule(a).Active != 0 {
		t.Fatalf("reopening the panel: open=%v active=%d", terminalModule(a).Open, terminalModule(a).Active)
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 1 || !terminalModule(a).Open {
		t.Fatalf("closing a tab: tabs=%d open=%v", len(terminalModule(a).Tabs), terminalModule(a).Open)
	}
	if err := tt.Click("Close terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 0 || terminalModule(a).Open {
		t.Fatalf("closing the last tab: tabs=%d open=%v", len(terminalModule(a).Tabs), terminalModule(a).Open)
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
	if !terminalModule(a).Maximized {
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
	if terminalModule(a).Maximized {
		t.Fatal("the panel did not restore")
	}
	if terminalModule(a).Height != terminalDefaultHeight {
		t.Fatalf("restored height %v", terminalModule(a).Height)
	}
}

// TestTerminalTabCloseControl closes an inactive tab without changing
// which shell is on show.
func TestTerminalTabCloseControl(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	if err := tt.Click("New terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 2 || terminalModule(a).Active != 1 {
		t.Fatalf("tabs=%d active=%d", len(terminalModule(a).Tabs), terminalModule(a).Active)
	}
	if err := tt.Click("Close Terminal 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 1 || terminalModule(a).Active != 0 {
		t.Fatalf("after closing: tabs=%d active=%d", len(terminalModule(a).Tabs), terminalModule(a).Active)
	}
	if got := terminalTabLabel(terminalModule(a).Tabs[0]); got != "Terminal 2" {
		t.Fatalf("the surviving tab is %q", got)
	}
}
