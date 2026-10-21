package desktop

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
	terminalModule(a).Open = true
	terminalModule(a).Tabs = []*terminalTab{
		{Id: 1, Title: "one", ExitCode: -1},
		{Id: 2, Title: "two", ExitCode: -1},
		{Id: 3, Title: "three", ExitCode: -1},
	}
	terminalModule(a).Active = 1
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
	if len(terminalModule(a).Tabs) != 1 || terminalModule(a).Tabs[0].Id != 2 || terminalModule(a).Active != 0 {
		t.Fatalf("the strip kept %+v active=%d", terminalModule(a).Tabs, terminalModule(a).Active)
	}
	if !terminalModule(a).Open {
		t.Fatal("closing the other shells folded the panel")
	}

	terminalModule(a).Tabs = []*terminalTab{
		{Id: 2, Title: "two", ExitCode: -1},
		{Id: 4, Title: "four", ExitCode: -1},
	}
	terminalModule(a).Active, terminalModule(a).Open = 0, true
	tt.Frame()
	if err := tt.RightClick("two"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Close all tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(terminalModule(a).Tabs) != 0 || terminalModule(a).Open || terminalModule(a).Active != -1 {
		t.Fatalf("close all left %+v open=%v", terminalModule(a).Tabs, terminalModule(a).Open)
	}
}
