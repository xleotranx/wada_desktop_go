package desktop

import (
	"slices"
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// TestHeaderMenuPinsAndMarks keeps the title's menu level with the
// sidebar's: pinning and the unread mark work with the sidebar folded.
func TestHeaderMenuPinsAndMarks(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_a", Title: "Alpha"}}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	open := func() {
		t.Helper()
		if err := tt.Click("Chat options, Alpha"); err != nil {
			t.Fatal(err)
		}
	}
	open()
	if !slices.Contains(tt.Menu(), "Pin chat") || !slices.Contains(tt.Menu(), "Mark as unread") {
		t.Fatalf("the title menu reads %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Pin chat"); err != nil {
		t.Fatal(err)
	}
	if !a.scope().pinned["ses_a"] {
		t.Fatal("Pin chat did not pin")
	}
	open()
	if !slices.Contains(tt.Menu(), "Unpin from Pinned") {
		t.Fatalf("the menu cannot unpin: %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Unpin from Pinned"); err != nil {
		t.Fatal(err)
	}
	if a.scope().pinned["ses_a"] {
		t.Fatal("Unpin left the chat pinned")
	}
	open()
	if err := tt.ChooseMenuItem("Mark as unread"); err != nil {
		t.Fatal(err)
	}
	if !a.scope().unread["ses_a"] {
		t.Fatal("Mark as unread did not mark it")
	}
	open()
	if !slices.Contains(tt.Menu(), "Mark as read") {
		t.Fatalf("the menu cannot mark it read: %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Mark as read"); err != nil {
		t.Fatal(err)
	}
	if a.scope().unread["ses_a"] {
		t.Fatal("Mark as read kept the mark")
	}
}
