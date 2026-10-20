package main

import (
	"fmt"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestChatTabsOpenSwitchAndClose keeps several conversations one press
// apart: each opened chat joins the strip, its title opens it, and its
// cross closes it, handing the view to the neighbouring tab or a draft.
func TestChatTabsOpenSwitchAndClose(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.openConversation(a.scope().sessions[0])
	a.openConversation(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	if got := len(a.scope().tabs); got != 2 {
		t.Fatalf("opened %d tabs, want 2", got)
	}
	if !tt.HasText("Alpha") || !tt.HasText("Beta") {
		t.Fatalf("the strip is missing a title: %v", tt.Texts())
	}
	if err := tt.Click("Open Alpha"); err != nil {
		t.Fatal(err)
	}
	if got := string(a.conversation.target.session); got != "ses_alpha" {
		t.Fatalf("the Alpha tab opened %q", got)
	}

	if err := tt.Click("Close Alpha"); err != nil {
		t.Fatal(err)
	}
	if got := string(a.conversation.target.session); got != "ses_beta" {
		t.Fatalf("closing the open tab did not fall back to Beta: %q", got)
	}
	if got := len(a.scope().tabs); got != 1 {
		t.Fatalf("closing left %d tabs", got)
	}

	// The last tab's strip folds with it; closing the tab itself still
	// hands the view to a fresh draft.
	a.closeTab("ses_beta")
	if !a.conversation.target.isDraft() || len(a.scope().tabs) != 0 {
		t.Fatalf("closing the last tab left target=%q tabs=%d",
			a.conversation.target.session, len(a.scope().tabs))
	}
}

// TestChatTabsCycle moves through the strip with the keyboard, wrapping
// from the last tab to the first and back the other way.
func TestChatTabsCycle(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_alpha" {
		t.Fatalf("Ctrl+Tab landed on %q", got)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_beta" {
		t.Fatalf("Ctrl+Shift+Tab landed on %q", got)
	}
}

// TestChatTabsKeepTheNewest bounds the strip: opening more chats than fit
// drops the oldest tab instead of growing the row without limit.
func TestChatTabsKeepTheNewest(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	for i := range chatTabLimit + 3 {
		s := opencode.Session{ID: fmt.Sprintf("ses_%02d", i), Title: fmt.Sprintf("Chat %02d", i)}
		a.scope().sessions = append(a.scope().sessions, s)
		a.adoptSession(s)
	}
	tabs := a.scope().tabs
	if len(tabs) != chatTabLimit {
		t.Fatalf("the strip grew to %d tabs", len(tabs))
	}
	if tabs[0].id != "ses_03" || tabs[len(tabs)-1].id != "ses_10" {
		t.Fatalf("the strip kept %q … %q", tabs[0].id, tabs[len(tabs)-1].id)
	}
}

// TestChatTabsStartADraft keeps the strip while a draft is open, so the
// plus does not lose the conversations it sits beside.
func TestChatTabsStartADraft(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("New chat tab"); err != nil {
		t.Fatal(err)
	}
	if !a.conversation.target.isDraft() {
		t.Fatal("the plus did not start a draft")
	}
	if len(a.scope().tabs) != 2 || !tt.HasText("Alpha") || !tt.HasText("Beta") {
		t.Fatalf("the draft dropped the strip: tabs=%d", len(a.scope().tabs))
	}
}
