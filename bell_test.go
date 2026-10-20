package main

import (
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestBellPanelListsAndOpensChats groups the marked chats by what they are
// waiting on and opens one from its row.
func TestBellPanelListsAndOpensChats(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_wait", Title: "Waiting wiring", Directory: "/work/wait"},
		{ID: "ses_fresh", Title: "Fresh answer", Directory: "/work/fresh"},
		{ID: "ses_old", Title: "Older answer", Directory: "/work/old"},
	}
	a.markUnread("ses_wait", true)
	a.markUnread("ses_fresh", false)
	a.markUnread("ses_old", false)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click(a.bellLabel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.layout.bellOpen {
		t.Fatal("the bell did not open the panel")
	}
	for _, want := range []string{"Notifications", "Needs attention", "Unread",
		"Waiting wiring", "Fresh answer", "Older answer", "Mark all read"} {
		if !tt.HasText(want) {
			t.Fatalf("the panel lacks %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Open Fresh answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.bellOpen {
		t.Fatal("opening a chat left the panel up")
	}
	job := <-commandsOf(a)
	if job.kind != "open" || job.sid != "ses_fresh" {
		t.Fatalf("the row queued %+v", job)
	}
	if a.scope().unread["ses_fresh"] {
		t.Fatal("the opened chat kept its unread mark")
	}
}

// TestBellPanelMarksAllRead keeps chats whose turn still waits on an
// approval marked, and clears every other unread one.
func TestBellPanelMarksAllRead(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_wait", Title: "Waiting wiring"},
		{ID: "ses_done", Title: "Fresh answer"},
	}
	a.markUnread("ses_wait", true)
	a.markUnread("ses_done", false)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click(a.bellLabel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Mark all read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.bellOpen {
		t.Fatal("marking read left the panel up")
	}
	if a.scope().unread["ses_done"] || a.scope().attention["ses_done"] {
		t.Fatal("a finished chat stayed marked")
	}
	if !a.scope().unread["ses_wait"] || !a.scope().attention["ses_wait"] {
		t.Fatal("an approval waiting was marked read")
	}
	if !tt.HasText("Unread chats marked read") {
		t.Fatalf("the action did not confirm: %v", tt.Texts())
	}
}

// TestBellPanelAllCaughtUp shows the quiet face and no mark-all action.
func TestBellPanelAllCaughtUp(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Notifications"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("You're all caught up.") {
		t.Fatalf("the quiet panel is missing: %v", tt.Texts())
	}
	if tt.HasText("Mark all read") {
		t.Fatal("an empty panel offered to mark read")
	}
}

// TestBellPanelShowsApprovals lists the open chat's pending approval and
// closes the panel when it is picked.
func TestBellPanelShowsApprovals(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_open", Title: "Open chat", Directory: "/work/o"}}
	a.conversation.target.selectSession("ses_open")
	a.conversation.selected = "Open chat"
	a.conversation.transcript.Ask(&chat.Permission{
		ID: "perm_1", Session: "ses_open", Action: "bash",
	})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click(a.bellLabel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Waiting in this chat") || !tt.HasText("Approval needed") {
		t.Fatalf("the approval is not listed: %v", tt.Texts())
	}
	if _, found := tt.Find("Show the approval: run a shell command"); !found {
		t.Fatalf("the approval row has no action: %v", tt.Texts())
	}
	if err := tt.Click("Show the approval: run a shell command"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.bellOpen {
		t.Fatal("picking the approval left the panel up")
	}
}
