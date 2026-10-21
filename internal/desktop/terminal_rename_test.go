package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTerminalTabRename names a shell tab from its menu and hands it back
// to the shell's title when the name is cleared.
func TestTerminalTabRename(t *testing.T) {
	a := newTestApp(t)
	terminalModule(a).Open = true
	terminalModule(a).Tabs = []*terminalTab{
		{Id: 1, Title: "one", ExitCode: -1},
		{Id: 2, Title: "two", ExitCode: -1},
	}
	terminalModule(a).Active = 0
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.RightClick("two"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Rename tab…") {
		t.Fatalf("the terminal menu lacks rename: %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Rename tab…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st := terminalModule(a).Rename; st == nil || !st.Open {
		t.Fatalf("the rename dialog is not open: %+v", st)
	}
	if err := tt.Click("Terminal name"); err != nil {
		t.Fatal(err)
	}
	tt.Type("tests")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if terminalModule(a).Tabs[1].Custom != "tests" {
		t.Fatalf("the tab reads %q", terminalModule(a).Tabs[1].Custom)
	}
	if terminalModule(a).Rename != nil {
		t.Fatal("the rename dialog stayed open")
	}
	if _, ok := tt.Find("tests"); !ok {
		t.Fatalf("the named tab is not drawn: %v", tt.Texts())
	}

	if err := tt.RightClick("tests"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Clear name") {
		t.Fatalf("the menu cannot clear the name: %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Clear name"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if terminalModule(a).Tabs[1].Custom != "" {
		t.Fatalf("the name stayed %q", terminalModule(a).Tabs[1].Custom)
	}
	if _, ok := tt.Find("two"); !ok {
		t.Fatalf("the shell title did not return: %v", tt.Texts())
	}
}
