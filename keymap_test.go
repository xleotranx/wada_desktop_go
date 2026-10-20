package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestShortcutOverrideReplacesTheDefault shows the new chord registering
// while the default one no longer opens the command.
func TestShortcutOverrideReplacesTheDefault(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	a.setShortcut("palette", chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK})
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if a.layout.commandOpen {
		t.Fatal("the default palette key still fires")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyK)
	tt.Frame()
	if !a.layout.commandOpen {
		t.Fatal("the remapped palette key did not fire")
	}
}

// TestShortcutDialogRecordsAChord drives the editor and recorder through
// their UI.
func TestShortcutDialogRecordsAChord(t *testing.T) {
	a := newTestApp(t)
	a.openShortcuts()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Window shortcuts") {
		t.Fatalf("the editor is missing: %v", tt.Texts())
	}
	if err := tt.Click("Change Command palette"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.shortcutRecord != "palette" {
		t.Fatalf("recording %q", a.layout.shortcutRecord)
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyK)
	tt.Frame()
	if got := a.preferences.keymap["palette"]; got != (chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK}) {
		t.Fatalf("recorded %+v", got)
	}
	if a.layout.shortcutRecord != "" {
		t.Fatal("the recorder stayed open after a chord")
	}
	if !tt.HasText("⌘⌥K") && ui.Cmd != ui.Ctrl {
		t.Fatalf("the editor did not show the new key: %v", tt.Texts())
	}
	if err := tt.Click("Reset Command palette"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.preferences.keymap["palette"]; ok {
		t.Fatal("Reset left the override behind")
	}
}

// TestShortcutConflict keeps a chord another command already uses.
func TestShortcutConflict(t *testing.T) {
	a := newTestApp(t)
	if label, used := a.shortcutConflict("palette", chord{Mods: ui.Cmd, Key: ui.KeyN}); !used || label != "New chat" {
		t.Fatalf("New chat conflict: %q %v", label, used)
	}
	if label, used := a.shortcutConflict("palette", chord{Mods: ui.Cmd, Key: ui.Key1}); !used || label != "Open a recent chat" {
		t.Fatalf("recent-chat conflict: %q %v", label, used)
	}
	if _, used := a.shortcutConflict("palette", chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK}); used {
		t.Fatal("a free chord was refused")
	}
}

// TestShortcutMetadata carries overrides through the snapshot and back.
func TestShortcutMetadata(t *testing.T) {
	a := newTestApp(t)
	a.setShortcut("palette", chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK})
	snap := a.stateSnapshot()
	if snap.keymap["palette"] != (chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK}) {
		t.Fatalf("snapshot %+v", snap.keymap)
	}
	b := newTestApp(t)
	b.restoreState(appMetadata{keymap: snap.keymap})
	if b.shortcutChord("palette") != (chord{Mods: ui.Cmd | ui.Alt, Key: ui.KeyK}) {
		t.Fatalf("restored %+v", b.shortcutChord("palette"))
	}
}

// TestShortcutLabelsFollowOverrides shows the keyboard page using the
// recorded chord instead of its printed default.
func TestShortcutLabelsFollowOverrides(t *testing.T) {
	a := newTestApp(t)
	a.setShortcut("find", chord{Mods: ui.Cmd | ui.Alt | ui.Shift, Key: ui.KeyP})
	a.openSettings(tabShortcuts)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	keys := a.shortcutKeys("find")
	if !tt.HasText(keys) {
		t.Fatalf("the keyboard page does not show %q: %v", keys, tt.Texts())
	}
}
