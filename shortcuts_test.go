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

	// Cmd+P opens the Files pane the tools grid advertises.
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	if but := a.layout.browserTool; but != "Files" || a.layout.browserCollapsed {
		t.Fatalf("Cmd+P opened %q (collapsed=%v)", but, a.layout.browserCollapsed)
	}

	// Ctrl+Shift+G opens Changes.
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyG)
	tt.Frame()
	if but := a.layout.browserTool; but != "Changes" {
		t.Fatalf("Ctrl+Shift+G opened %q", but)
	}

	// Cmd+J folds the tools pane and brings it back, and Cmd+B does the
	// same for the sidebar, as the reference app's keys do.
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if !a.layout.browserCollapsed {
		t.Fatal("Cmd+J did not hide the tools pane")
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if a.layout.browserCollapsed {
		t.Fatal("Cmd+J did not bring the tools pane back")
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

	// Cmd+Shift+F aims the sidebar's search field: the key opens it and
	// asks the field to take the keyboard on its next build. Cmd+F itself
	// belongs to find in the transcript.
	tt.Key(ui.Cmd|ui.Shift, ui.KeyF)
	if !a.search.searchOpen {
		t.Fatalf("Cmd+Shift+F did not open the search: open=%v focus=%v", a.search.searchOpen, a.search.focusSearch)
	}
	tt.Frame()
	if !a.search.searchOpen {
		t.Fatalf("Cmd+Shift+F did not keep the search open")
	}

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
