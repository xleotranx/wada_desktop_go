package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

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
	a.workspace.files = &filesState{
		location: dir,
		open:     map[string]bool{},
		entries: map[string][]opencode.FileEntry{".": {
			{Path: "main.go", Name: "main.go"},
			{Path: "README.md", Name: "README.md"},
			{Path: "src", Name: "src", Directory: true},
		}},
		loading: map[string]bool{},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	tt.Frame()
	if !a.layout.quickOpen {
		t.Fatal("Cmd+P did not open the file picker")
	}
	if !tt.HasText("main.go") || !tt.HasText("README.md") || tt.HasText("src") {
		t.Fatalf("the root listing is wrong: %v", tt.Texts())
	}
	if err := tt.Click("Open main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.quickOpen {
		t.Fatal("choosing a file left the picker open")
	}
	if a.layout.browserTool != "Files" || a.layout.browserCollapsed {
		t.Fatalf("the file landed in tool=%q collapsed=%v",
			a.layout.browserTool, a.layout.browserCollapsed)
	}
	if a.workspace.files.path != "main.go" || a.workspace.files.query != "" {
		t.Fatalf("pane path=%q query=%q",
			a.workspace.files.path, a.workspace.files.query)
	}
}

// TestQuickOpenEntriesFollowsTheQuery lists matches when a query stands.
func TestQuickOpenEntriesFollowsTheQuery(t *testing.T) {
	a := newTestApp(t)
	st := &filesState{
		query:   "main",
		entries: map[string][]opencode.FileEntry{".": {{Path: "root.go"}}},
		results: []opencode.FileEntry{{Path: "cmd/main.go"}, {Path: "internal/main.go"}},
		loading: map[string]bool{},
	}
	entries := a.quickOpenEntries(st)
	if len(entries) != 2 || entries[0].Path != "cmd/main.go" {
		t.Fatalf("query entries %+v", entries)
	}
	st.query = ""
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
	a.workspace.files = &filesState{
		location: dir,
		open:     map[string]bool{},
		entries: map[string][]opencode.FileEntry{".": {
			{Path: "alpha.go"}, {Path: "beta.go"}, {Path: "gamma.go"},
		}},
		loading: map[string]bool{},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if a.layout.quickSelected != 2 {
		t.Fatalf("two Downs chose %d", a.layout.quickSelected)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.workspace.files.path != "gamma.go" {
		t.Fatalf("Enter opened %q", a.workspace.files.path)
	}
	if a.layout.quickOpen {
		t.Fatal("the picker stayed open after Enter")
	}
}
