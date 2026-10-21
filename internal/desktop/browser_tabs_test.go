package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPaneTabsKeepToolsOpen switches among Files, Changes and Activity in
// the right pane, closing tabs one at a time until the pane folds away.
func TestPaneTabsKeepToolsOpen(t *testing.T) {
	a := newTestApp(t)
	a.openTool("Files")
	a.openTool("Changes")
	a.openTool("Activity")
	if got := len(a.layout.paneTabs); got != 3 {
		t.Fatalf("opened %d pane tabs", got)
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Files") || !tt.HasText("Changes") || !tt.HasText("Activity") {
		t.Fatalf("the pane strip is missing a tool: %v", tt.Texts())
	}
	if tt.HasText("Search or enter a URL") {
		t.Fatal("a tool tab still showed the page address field")
	}
	if err := tt.Click("Open Files"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browserTool != "Files" {
		t.Fatalf("the Files tab opened %q", a.layout.browserTool)
	}
	if err := tt.Click("Close Changes"); err != nil {
		t.Fatal(err)
	}
	if got := len(a.layout.paneTabs); got != 2 {
		t.Fatalf("closing an inactive tab left %d", got)
	}
	if err := tt.Click("Close Files"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browserTool != "Activity" {
		t.Fatalf("closing the active tab showed %q", a.layout.browserTool)
	}
	a.closePaneTab(0)
	if len(a.layout.paneTabs) != 0 || !a.layout.browserCollapsed {
		t.Fatalf("the last tab left tabs=%d collapsed=%v",
			len(a.layout.paneTabs), a.layout.browserCollapsed)
	}
}

func TestToolsDoNotAdvertisePagePreview(t *testing.T) {
	a := newTestApp(t)
	a.openTool("")
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("New page") || tt.HasText("Page preview") || tt.HasText("Search or enter a URL") {
		t.Fatal("page preview returned")
	}
}
