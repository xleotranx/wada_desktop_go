package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPaneTabContextMenuClosesTabs closes the other right-pane tabs, then
// the whole strip, from one tab's menu.
func TestPaneTabContextMenuClosesTabs(t *testing.T) {
	a := newTestApp(t)
	a.layout.paneTabs = []paneTab{{kind: "Files"}, {kind: "Activity"}, {kind: "Changes"}}
	a.layout.browserTool, a.layout.browserCollapsed = "Activity", false
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.RightClick("Open Activity"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Close tab", "Close other tabs", "Close all tabs"} {
		if !slices.Contains(tt.Menu(), label) {
			t.Fatalf("the pane tab menu lacks %q: %v", label, tt.Menu())
		}
	}
	if err := tt.ChooseMenuItem("Close other tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.layout.paneTabs, []paneTab{{kind: "Activity"}}) {
		t.Fatalf("the strip kept %+v", a.layout.paneTabs)
	}
	if a.layout.browserTool != "Activity" || a.layout.browserCollapsed {
		t.Fatalf("the kept tab is not on show: %q collapsed=%v", a.layout.browserTool, a.layout.browserCollapsed)
	}

	// A strip of one hides, so seed two again before closing everything.
	a.layout.paneTabs = []paneTab{{kind: "Activity"}, {kind: "Files"}}
	a.layout.browserTool, a.layout.browserCollapsed = "Activity", false
	tt.Frame()
	if err := tt.RightClick("Open Activity"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Close all tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.layout.paneTabs) != 0 || !a.layout.browserCollapsed || a.layout.browserTool != "" {
		t.Fatalf("close all left %+v collapsed=%v tool=%q", a.layout.paneTabs, a.layout.browserCollapsed, a.layout.browserTool)
	}
}

// TestTerminalTabContextMenuClosesTabs closes the other shells, then all
// of them, from one terminal tab's menu.
func TestTerminalTabContextMenuClosesTabs(t *testing.T) {
	a := newTestApp(t)
	a.terminal.open = true
	a.terminal.tabs = []*terminalTab{
		{id: 1, title: "one", exitCode: -1},
		{id: 2, title: "two", exitCode: -1},
		{id: 3, title: "three", exitCode: -1},
	}
	a.terminal.active = 1
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.RightClick("two"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Close other tabs") || !slices.Contains(tt.Menu(), "Close all tabs") {
		t.Fatalf("the terminal menu reads %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Close other tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 1 || a.terminal.tabs[0].id != 2 || a.terminal.active != 0 {
		t.Fatalf("the strip kept %+v active=%d", a.terminal.tabs, a.terminal.active)
	}
	if !a.terminal.open {
		t.Fatal("closing the other shells folded the panel")
	}

	a.terminal.tabs = []*terminalTab{
		{id: 2, title: "two", exitCode: -1},
		{id: 4, title: "four", exitCode: -1},
	}
	a.terminal.active, a.terminal.open = 0, true
	tt.Frame()
	if err := tt.RightClick("two"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Close all tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 0 || a.terminal.open || a.terminal.active != -1 {
		t.Fatalf("close all left %+v open=%v", a.terminal.tabs, a.terminal.open)
	}
}
