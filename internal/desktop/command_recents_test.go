package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestCommandRecentsFloatToTop keeps the palette's recent actions in a
// group of their own, newest first, each standing once.
func TestCommandRecentsFloatToTop(t *testing.T) {
	a := newTestApp(t)
	a.runCommand("action:filter-unread")
	a.runCommand("action:activity")
	items := a.commandItems()
	if items[0].Group != "Filter" {
		t.Fatalf("the scope switcher lost the top: %+v", items)
	}
	start := -1
	for i, item := range items {
		if item.Group == "Recent" {
			start = i
			break
		}
	}
	if start < 0 || len(items) < start+2 {
		t.Fatalf("the palette has no Recent group: %+v", items)
	}
	if items[start].ID != "action:activity" || items[start+1].ID != "action:filter-unread" {
		t.Fatalf("the recent order is %q, %q", items[start].ID, items[start+1].ID)
	}
	counts := map[string]int{}
	for _, item := range items {
		counts[item.ID]++
	}
	if counts["action:activity"] != 1 || counts["action:filter-unread"] != 1 {
		t.Fatalf("an action stands twice: %v", counts)
	}
}

// TestCommandRecentsLimitAndDedupe keeps the newest five once, and skips
// an action the window no longer offers.
func TestCommandRecentsLimitAndDedupe(t *testing.T) {
	a := newTestApp(t)
	for i := range 7 {
		a.rememberCommand(fmt.Sprintf("action:remembered-%d", i))
	}
	a.rememberCommand("action:remembered-6")
	if len(a.preferences.commandRecents) != commandRecentsLimit {
		t.Fatalf("the history kept %d", len(a.preferences.commandRecents))
	}
	if a.preferences.commandRecents[0] != "action:remembered-6" ||
		a.preferences.commandRecents[1] != "action:remembered-5" {
		t.Fatalf("the order is %v", a.preferences.commandRecents)
	}
	a.preferences.commandRecents = []string{"action:gone", "action:new-chat"}
	items := a.commandItems()
	recent := 0
	first := ""
	for _, item := range items {
		if item.Group == "Recent" {
			recent++
			if first == "" {
				first = item.ID
			}
		}
	}
	if recent != 1 || first != "action:new-chat" {
		t.Fatalf("the unknown action was not skipped: %d recent, %q", recent, first)
	}
}

// TestCommandRecentsInPalette shows the group heading in the dialog.
func TestCommandRecentsInPalette(t *testing.T) {
	a := newTestApp(t)
	a.runCommand("action:filter-unread")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !tt.HasText("Recent") || !tt.HasText("Show unread chats") {
		t.Fatalf("the palette lacks its recent group: %v", tt.Texts())
	}
}
