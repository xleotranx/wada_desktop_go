package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// findMenuItem walks a menu template for one label.
func findMenuItem(items []*mygo.MenuItem, label string) *mygo.MenuItem {
	for _, item := range items {
		if item.Label == label {
			return item
		}
		if found := findMenuItem(item.Submenu, label); found != nil {
			return found
		}
	}
	return nil
}

// TestAppMenuShape keeps the menus and their actions together, without
// accelerators that would take keys from the remappable map.
func TestAppMenuShape(t *testing.T) {
	a := newTestApp(t)
	menu := appMenu(a)
	for _, label := range []string{
		"File", "Chat", "View", "Help",
		"New Chat", "Open Folder…",
		"Export Transcript…", "Quick Open File…", "Find…", "Search Chats…",
		"Chat History…", "Copy Transcript", "Choose Model…",
		"Toggle Sidebar", "Toggle Tools", "Toggle Terminal", "Toggle Full View",
		"Reset Pane Sizes", "Keyboard Shortcuts", "Slash Commands",
	} {
		item := findMenuItem(menu.Items(), label)
		if item == nil {
			t.Fatalf("the menu lacks %q", label)
		}
		if item.Role == "" && item.Accelerator != "" {
			t.Fatalf("%q carries accelerator %q", label, item.Accelerator)
		}
	}
}

// TestAppMenuActions runs the app's menu choices through the menu hook and
// sees the same actions the shortcuts run.
func TestAppMenuActions(t *testing.T) {
	a := newTestApp(t)
	a.runtime.menu = func(fn func()) { fn() }
	menu := appMenu(a)
	click := func(label string) {
		t.Helper()
		item := findMenuItem(menu.Items(), label)
		if item == nil || item.Click == nil {
			t.Fatalf("%q has no action", label)
		}
		item.Click(item, nil)
	}

	click("Settings…")
	if !a.layout.settingsOpen {
		t.Fatal("Settings… did not open the page")
	}
	a.layout.settingsOpen = false
	click("Keyboard Shortcuts")
	if !a.layout.settingsOpen || a.layout.settingsTab != tabShortcuts {
		t.Fatalf("Keyboard Shortcuts opened %v %q", a.layout.settingsOpen, a.layout.settingsTab)
	}
	a.layout.settingsOpen = false
	click("Slash Commands")
	if !a.layout.slashHelp {
		t.Fatal("Slash Commands did not open the help sheet")
	}
	a.layout.slashHelp = false
	click("Chat History…")
	if !a.layout.historyOpen {
		t.Fatal("Chat History… did not open the history")
	}
	a.layout.historyOpen = false
	click("Quick Open File…")
	if !a.layout.quickOpen {
		t.Fatal("Quick Open File… did not open the picker")
	}
	a.layout.quickOpen = false
	click("Toggle Sidebar")
	if !a.layout.sidebarCollapsed {
		t.Fatal("Toggle Sidebar did not fold the sidebar")
	}
	click("Toggle Sidebar")
	if a.layout.sidebarCollapsed {
		t.Fatal("Toggle Sidebar did not unfold the sidebar")
	}

	// A new chat from the menu starts a draft revision.
	first := a.conversation.target.revision
	click("New Chat")
	if a.conversation.target.revision == first || !a.conversation.target.isDraft() {
		t.Fatal("New Chat did not start a fresh draft")
	}

	// A closed app's menu does nothing.
	a.runtime.closed = true
	a.layout.settingsOpen = false
	click("Settings…")
	if a.layout.settingsOpen {
		t.Fatal("a closed app ran a menu choice")
	}
}
