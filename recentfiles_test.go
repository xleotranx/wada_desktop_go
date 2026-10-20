package main

import (
	"fmt"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestRecentFilesRememberOpened moves each read file to the front and
// keeps only the workspace it belongs to.
func TestRecentFilesRememberOpened(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := "/work/one"
	a.workspace.files = &filesState{
		location: dir,
		open:     map[string]bool{},
		entries:  map[string][]opencode.FileEntry{},
		loading:  map[string]bool{},
	}
	a.filesRead("main.go")
	a.filesRead("browser.go")
	recent := a.recentFilesFor(dir)
	if len(recent) != 2 || recent[0].Path != "browser.go" || recent[1].Path != "main.go" {
		t.Fatalf("recent after two reads: %+v", recent)
	}
	a.filesRead("main.go")
	recent = a.recentFilesFor(dir)
	if len(recent) != 2 || recent[0].Path != "main.go" {
		t.Fatalf("recent after reopening: %+v", recent)
	}
	if got := a.recentFilesFor("/work/two"); len(got) != 0 {
		t.Fatalf("another workspace saw %+v", got)
	}
}

// TestRecentFilesLimit keeps the newest eight.
func TestRecentFilesLimit(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := "/work/many"
	a.workspace.files = &filesState{location: dir, open: map[string]bool{},
		entries: map[string][]opencode.FileEntry{}, loading: map[string]bool{}}
	for i := range 10 {
		a.filesRead(fmt.Sprintf("file-%d.go", i))
	}
	recent := a.recentFilesFor(dir)
	if len(recent) != recentFileLimit {
		t.Fatalf("kept %d recent files", len(recent))
	}
	if recent[0].Path != "file-9.go" {
		t.Fatalf("newest recent file %q", recent[0].Path)
	}
}

// TestQuickOpenShowsRecentFiles labels and lists the recent files when
// there is no query.
func TestQuickOpenShowsRecentFiles(t *testing.T) {
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
			{Path: "root.go", Name: "root.go"},
		}},
		loading: map[string]bool{},
	}
	a.preferences.recent[localServerID] = []recentFile{
		{Dir: dir, Path: "main.go"},
	}
	a.openQuickOpen()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Recent files") || !tt.HasText("main.go") {
		t.Fatalf("recent picker: %v", tt.Texts())
	}
	if tt.HasText("root.go") {
		t.Fatal("the root listing also showed beside recent files")
	}
}
