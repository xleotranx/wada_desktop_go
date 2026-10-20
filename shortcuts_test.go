package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestAppShortcuts drives the window's command keys: navigation responds
// wherever focus sits, and the keys the tool cards promise do open
// those panes.
func TestAppShortcuts(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	// Cmd+N resets the view: an open conversation hands back to a draft.
	a.adoptSession(opencode.Session{ID: "ses-keys", Title: "Shortcut target"})
	a.conversation.target.selectSession("ses-keys")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyN)
	tt.Frame()
	if !a.conversation.target.isDraft() {
		t.Fatal("Cmd+N did not restart the draft")
	}
	if a.layout.settingsOpen || a.search.searchOpen {
		t.Fatalf("Cmd+N left pages open: settings=%v search=%v", a.layout.settingsOpen, a.search.searchOpen)
	}

	// Cmd+2 opens the second chat the server listed, counting from the
	// newest.
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_newest", Title: "Newest"},
		{ID: "ses_second", Title: "Second"},
	}
	tt.Key(ui.Cmd, ui.Key2)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_second" {
		t.Fatalf("Cmd+2 opened %q", got)
	}

	// Cmd+O opens a folder and saves it as a project.
	dir := t.TempDir()
	a.runtime.pickFolder = func(done func(path string)) { done(dir) }
	tt.Key(ui.Cmd, ui.KeyO)
	tt.Frame()
	if len(a.projectDirectories()) == 0 {
		t.Fatal("Cmd+O did not add a project")
	}

	// Cmd+P opens the quick file dialog; closing it leaves Cmd+Shift+E
	// free to fold and restore the Files pane.
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	if !a.layout.quickOpen {
		t.Fatal("Cmd+P did not open the file picker")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.layout.quickOpen {
		t.Fatal("Escape did not close the file picker")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyE)
	tt.Frame()
	if a.layout.browserCollapsed || a.layout.browserTool != "Files" {
		t.Fatal("Cmd+Shift+E did not open the file pane")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyE)
	tt.Frame()
	if !a.layout.browserCollapsed {
		t.Fatal("Cmd+Shift+E did not fold the file pane")
	}

	// Ctrl+Shift+G opens Changes.
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyG)
	tt.Frame()
	if but := a.layout.browserTool; but != "Changes" {
		t.Fatalf("Ctrl+Shift+G opened %q", but)
	}

	// Cmd+Shift+J folds the tools pane and brings it back, Cmd+B does the
	// same for the sidebar, and Cmd+J opens the bottom terminal.
	tt.Key(ui.Cmd|ui.Shift, ui.KeyJ)
	tt.Frame()
	if !a.layout.browserCollapsed {
		t.Fatal("Cmd+Shift+J did not hide the tools pane")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyJ)
	tt.Frame()
	if a.layout.browserCollapsed {
		t.Fatal("Cmd+Shift+J did not bring the tools pane back")
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !a.terminal.open || !tt.HasText("The terminal did not start") {
		t.Fatalf("Cmd+J opened no terminal: open=%v", a.terminal.open)
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if a.terminal.open {
		t.Fatal("Cmd+J did not fold the terminal away")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !a.layout.sidebarCollapsed {
		t.Fatal("Cmd+B did not hide the sidebar")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if a.layout.sidebarCollapsed {
		t.Fatal("Cmd+B did not bring the sidebar back")
	}

	// Cmd+Option+F aims the sidebar's search field: the key opens it and
	// asks the field to take the keyboard on its next build. Cmd+F itself
	// belongs to find in the transcript and Cmd+Shift+F to full view.
	tt.Key(ui.Cmd|ui.Alt, ui.KeyF)
	if !a.search.searchOpen {
		t.Fatalf("Cmd+Option+F did not open the search: open=%v focus=%v", a.search.searchOpen, a.search.focusSearch)
	}
	tt.Frame()
	if !a.search.searchOpen {
		t.Fatalf("Cmd+Option+F did not keep the search open")
	}

	// Ctrl+Shift+M opens the model picker over the composer.
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyM)
	tt.Frame()
	if !a.layout.pickerOpen {
		t.Fatal("Ctrl+Shift+M did not open the model picker")
	}
	a.layout.pickerOpen = false

	// Cmd+, opens the settings page, and Cmd+/ opens its keyboard tab.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if !a.layout.settingsOpen {
		t.Fatal("Cmd+, did not open the settings")
	}
	tt.Key(ui.Cmd, ui.KeySlash)
	tt.Frame()
	if !a.layout.settingsOpen || a.layout.settingsTab != tabShortcuts {
		t.Fatalf("Cmd+/ left the settings on tab %q", a.layout.settingsTab)
	}
	if !tt.HasText("Keyboard") || !tt.HasText("⌘B") || !tt.HasText("⌘F") {
		t.Fatalf("the keyboard tab is missing its groups: %v", tt.Texts())
	}
}

// TestShortcutsHoldWhileDialogsUp keeps the command keys from yanking the
// view out from under a modal dialog: a rename dialog stays where it is,
// and the conversation it belongs to stays open.
func TestShortcutsHoldWhileDialogsUp(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	a.conversation.target.selectSession("ses-dialog")
	a.layout.rename = &renameState{open: true, sid: "ses-dialog", draft: "Keep me"}
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyN)
	tt.Frame()
	if a.conversation.target.isDraft() {
		t.Fatal("Cmd+N restarted the draft under a dialog")
	}
	if a.layout.rename == nil || !a.layout.rename.open {
		t.Fatal("the rename dialog lost its state under a shortcut")
	}
	if a.layout.settingsOpen || a.layout.browserTool != "" {
		t.Fatalf("a dialog shortcut opened panes: settings=%v tool=%q",
			a.layout.settingsOpen, a.layout.browserTool)
	}
}

// TestFileSearchShortcutFocusesField opens Files with a ready tree and a
// quiet background so the search field is the thing the shortcut reaches.
func TestFileSearchShortcutFocusesField(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := a.chatDir()
	if dir == "" {
		dir = a.preferences.home
	}
	a.workspace.files = &filesState{
		location: dir,
		open:     map[string]bool{},
		entries:  map[string][]opencode.FileEntry{".": {}},
		loading:  map[string]bool{},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Search files") {
		t.Fatalf("Cmd+P did not focus the file search: %v", tt.Texts())
	}
}
