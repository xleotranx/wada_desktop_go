package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestBackgroundTurnMarksChatUnread gives another chat a finished turn,
// then a background approval, and checks the bell, the row dot and the
// clearing that opening the chat does.
func TestBackgroundTurnMarksChatUnread(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Wired turn"},
		{ID: "ses_b", Title: "Background turn"},
	}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Wired turn"

	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionSucceeded, SessionID: "ses_b"})
	if !a.scope().unread["ses_b"] || a.scope().attention["ses_b"] {
		t.Fatalf("background finish: unread=%v attention=%v",
			a.scope().unread["ses_b"], a.scope().attention["ses_b"])
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Notifications, 1 unread chats") {
		t.Fatalf("the bell missed the unread chat: %v", tt.Texts())
	}

	a.applyUpdate(opencode.Update{Kind: opencode.PermissionAsked, SessionID: "ses_b"})
	if !a.scope().attention["ses_b"] {
		t.Fatal("a background approval was not marked")
	}
	tt.Frame()
	if !tt.HasText("Notifications, 1 unread chats needing attention") {
		t.Fatalf("the bell missed the approval: %v", tt.Texts())
	}
	if a.conversation.target.session != "ses_a" {
		t.Fatal("a background event moved the open chat")
	}

	a.openNextMarked()
	if a.conversation.target.session != "ses_b" {
		t.Fatalf("next marked opened %q", a.conversation.target.session)
	}
	if a.scope().unread["ses_b"] || a.scope().attention["ses_b"] {
		t.Fatal("opening the chat left its marks behind")
	}
}

// TestNextMarkedPrefersAttention walks the order the bell uses: approvals
// before plain unread chats.
func TestNextMarkedPrefersAttention(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Wired turn"},
		{ID: "ses_b", Title: "Unread chat"},
		{ID: "ses_c", Title: "Waiting approval"},
	}
	a.conversation.target.selectSession("ses_a")
	a.markUnread("ses_b", false)
	a.markUnread("ses_c", true)
	a.openNextMarked()
	if a.conversation.target.session != "ses_c" {
		t.Fatalf("next marked opened %q", a.conversation.target.session)
	}
}

// TestUnreadToggleAndDeleteMark covers the manual mark used by the menu
// and key, and removing a chat's marks when it is deleted.
func TestUnreadToggleAndDeleteMark(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Wired turn"}}
	a.conversation.target.selectSession("ses_a")

	a.toggleUnread()
	if !a.scope().unread["ses_a"] {
		t.Fatal("the open chat did not mark unread")
	}
	a.toggleUnread()
	if a.scope().unread["ses_a"] {
		t.Fatal("the second toggle did not mark it read")
	}
	a.markUnread("ses_a", true)
	a.dropSession("ses_a")
	if a.scope().unread["ses_a"] || a.scope().attention["ses_a"] {
		t.Fatal("deleting a chat left its marks behind")
	}
}

// TestUnreadKeyMarksTheOpenChat drives the shortcut itself.
func TestUnreadKeyMarksTheOpenChat(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Wired turn"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Wired turn"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyU)
	if !a.scope().unread["ses_a"] {
		t.Fatal("Cmd+Shift+U did not mark the chat unread")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyU)
	if a.scope().unread["ses_a"] {
		t.Fatal("Cmd+Shift+U did not mark it read again")
	}
}

// TestUnreadSurvivesRestoration keeps the marks in the snapshot and puts
// them back into their server's scope, sorted for a stable preference.
func TestUnreadSurvivesRestoration(t *testing.T) {
	a := newTestApp(t)
	a.scope().unread["ses_b"] = true
	a.scope().unread["ses_a"] = true
	snap := a.stateSnapshot()
	if got := snap.unread[localServerID]; len(got) != 2 || got[0] != "ses_a" || got[1] != "ses_b" {
		t.Fatalf("snapshot unread %v", got)
	}
	b := newTestApp(t)
	b.restoreState(appMetadata{unread: map[string][]string{localServerID: {"ses_a", "ses_b"}}})
	if !b.scope().unread["ses_a"] || !b.scope().unread["ses_b"] {
		t.Fatalf("restored unread %v", b.scope().unread)
	}
}
