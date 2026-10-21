package desktop

import (
	"testing"

	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

// TestQuickOpenListsAndOpensRootFile shows the picker's initial list and
// lands the chosen file in the Files pane with its search cleared.
func TestQuickOpenListsAndOpensRootFile(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := a.chatDir()
	if dir == "" {
		dir = a.preferences.home
	}
	codingModule(a).Files = &filesState{
		Location: dir,
		Open:     map[string]bool{},
		Entries: map[string][]workspace.FileEntry{".": {
			{Path: "main.go", Name: "main.go"},
			{Path: "README.md", Name: "README.md"},
			{Path: "src", Name: "src", Directory: true},
		}},
		Loading: map[string]bool{},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	tt.Frame()
	if !codingModule(a).QuickOpen {
		t.Fatal("Cmd+P did not open the file picker")
	}
	if !tt.HasText("main.go") || !tt.HasText("README.md") || tt.HasText("src") {
		t.Fatalf("the root listing is wrong: %v", tt.Texts())
	}
	if err := tt.Click("Open main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if codingModule(a).QuickOpen {
		t.Fatal("choosing a file left the picker open")
	}
	if a.layout.browserTool != "Files" || a.layout.browserCollapsed {
		t.Fatalf("the file landed in tool=%q collapsed=%v",
			a.layout.browserTool, a.layout.browserCollapsed)
	}
	if codingModule(a).Files.Path != "main.go" || codingModule(a).Files.Query != "" {
		t.Fatalf("pane path=%q query=%q",
			codingModule(a).Files.Path, codingModule(a).Files.Query)
	}
}

// TestQuickOpenEntriesFollowsTheQuery lists matches when a query stands.
func TestQuickOpenEntriesFollowsTheQuery(t *testing.T) {
	a := newTestApp(t)
	st := &filesState{
		Query:   "main",
		Entries: map[string][]workspace.FileEntry{".": {{Path: "root.go"}}},
		Results: []workspace.FileEntry{{Path: "cmd/main.go"}, {Path: "internal/main.go"}},
		Loading: map[string]bool{},
	}
	entries := a.quickOpenEntries(st)
	if len(entries) != 2 || entries[0].Path != "cmd/main.go" {
		t.Fatalf("query entries %+v", entries)
	}
	st.Query = ""
	entries = a.quickOpenEntries(st)
	if len(entries) != 1 || entries[0].Path != "root.go" {
		t.Fatalf("root entries %+v", entries)
	}
}

// TestQuickOpenArrowsChoose walks the picker with Down and opens the
// selected row with Enter.
func TestQuickOpenArrowsChoose(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := a.chatDir()
	if dir == "" {
		dir = a.preferences.home
	}
	codingModule(a).Files = &filesState{
		Location: dir,
		Open:     map[string]bool{},
		Entries: map[string][]workspace.FileEntry{".": {
			{Path: "alpha.go"}, {Path: "beta.go"}, {Path: "gamma.go"},
		}},
		Loading: map[string]bool{},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if codingModule(a).QuickSelected != 2 {
		t.Fatalf("two Downs chose %d", codingModule(a).QuickSelected)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if codingModule(a).Files.Path != "gamma.go" {
		t.Fatalf("Enter opened %q", codingModule(a).Files.Path)
	}
	if codingModule(a).QuickOpen {
		t.Fatal("the picker stayed open after Enter")
	}
}

// TestQuickOpenShowsTheDirectory shows a result's folder under its name.
func TestQuickOpenShowsTheDirectory(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := a.chatDir()
	if dir == "" {
		dir = a.preferences.home
	}
	codingModule(a).Files = &filesState{
		Location: dir,
		Open:     map[string]bool{},
		Entries: map[string][]workspace.FileEntry{".": {
			{Path: "cmd/server/main.go", Name: "main.go"},
		}},
		Loading: map[string]bool{},
	}
	a.openQuickOpen()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("main.go") || !tt.HasText("cmd/server") {
		t.Fatalf("the result metadata is missing: %v", tt.Texts())
	}
}
