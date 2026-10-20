package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestWorkspacePanesResize drags both workspace dividers: the sidebar
// gives the conversation more room when dragged right, the tools divider
// gives the tools more when dragged left, and a double click on either
// returns the pane to its opening width.
func TestWorkspacePanesResize(t *testing.T) {
	a := newTestApp(t)
	a.openTool("")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	sidebar, ok := tt.Find("Resize sidebar")
	if !ok {
		t.Fatalf("the sidebar divider is missing: %v", tt.Texts())
	}
	start := a.layout.sidebarW
	x, y := sidebar.X+sidebar.W/2, sidebar.Y+sidebar.H/2
	tt.Press(x, y)
	tt.Move(x+80, y)
	tt.Release(x+80, y)
	tt.Frame()
	if got := a.layout.sidebarW; got < start+70 || got > start+90 {
		t.Fatalf("dragging the sidebar divider moved its width from %v to %v", start, got)
	}

	tools, ok := tt.Find("Resize tools")
	if !ok {
		t.Fatalf("the tools divider is missing: %v", tt.Texts())
	}
	browser := a.layout.browserW
	x, y = tools.X+tools.W/2, tools.Y+tools.H/2
	tt.Press(x, y)
	tt.Move(x-70, y)
	tt.Release(x-70, y)
	tt.Frame()
	if got := a.layout.browserW; got < browser+60 || got > browser+80 {
		t.Fatalf("dragging the tools divider moved its width from %v to %v", browser, got)
	}

	sidebar, ok = tt.Find("Resize sidebar")
	if !ok {
		t.Fatal("the sidebar divider left after resizing")
	}
	tt.ClickAt(sidebar.X+sidebar.W/2, sidebar.Y+sidebar.H/2)
	tt.ClickAt(sidebar.X+sidebar.W/2, sidebar.Y+sidebar.H/2)
	tt.Frame()
	if a.layout.sidebarW != sidebarDefaultWidth {
		t.Fatalf("double-clicking the sidebar divider left width %v, want %v", a.layout.sidebarW, sidebarDefaultWidth)
	}
	tools, ok = tt.Find("Resize tools")
	if !ok {
		t.Fatal("the tools divider left after resetting")
	}
	tt.ClickAt(tools.X+tools.W/2, tools.Y+tools.H/2)
	tt.ClickAt(tools.X+tools.W/2, tools.Y+tools.H/2)
	tt.Frame()
	if a.layout.browserW != browserDefaultWidth {
		t.Fatalf("double-clicking the tools divider left width %v, want %v", a.layout.browserW, browserDefaultWidth)
	}
}

// TestWorkspaceDividerKeys moves a focused divider with the keyboard and
// keeps its size within the pane limits, so the conversation cannot be
// squeezed out of the window.
func TestWorkspaceDividerKeys(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	if _, ok := tt.Find("Resize sidebar"); !ok {
		t.Fatalf("the sidebar divider is missing: %v", tt.Texts())
	}
	center := func() (float32, float32) {
		r, ok := tt.Find("Resize sidebar")
		if !ok {
			t.Fatal("the sidebar divider disappeared")
		}
		return r.X + r.W/2, r.Y + r.H/2
	}
	x, y := center()
	tt.ClickAt(x, y)
	before := a.layout.sidebarW
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.layout.sidebarW <= before {
		t.Fatalf("the arrow key did not widen the sidebar: %v to %v", before, a.layout.sidebarW)
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if a.layout.sidebarW != sidebarMinWidth {
		t.Fatalf("Home left the sidebar at %v, want %v", a.layout.sidebarW, sidebarMinWidth)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.layout.sidebarW != sidebarMaxWidth {
		t.Fatalf("End left the sidebar at %v, want %v", a.layout.sidebarW, sidebarMaxWidth)
	}
}

// TestPaneWidthsRestoreClamped keeps a stored layout usable: a width from
// an older window returns within the current limits.
func TestPaneWidthsRestoreClamped(t *testing.T) {
	a := newTestApp(t)
	a.restoreState(appMetadata{sidebarW: 80, browserW: 5000, terminalH: 9000})
	if a.layout.sidebarW != sidebarMinWidth {
		t.Fatalf("narrow sidebar restored as %v, want %v", a.layout.sidebarW, sidebarMinWidth)
	}
	if a.layout.browserW != browserMaxWidth {
		t.Fatalf("wide tools pane restored as %v, want %v", a.layout.browserW, browserMaxWidth)
	}
	if a.terminal.height != terminalMaxHeight {
		t.Fatalf("tall terminal restored as %v, want %v", a.terminal.height, terminalMaxHeight)
	}
}
