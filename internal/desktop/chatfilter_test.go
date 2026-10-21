package desktop

import (
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// TestSidebarFilters narrows the sidebar to unread chats, drafts and
// attention, then clears back to the pinned/projects view.
func TestSidebarFilters(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{
		{ID: "ses_a", Title: "Alpha Chat"},
		{ID: "ses_b", Title: "Beta Chat"},
		{ID: "ses_c", Title: "Gamma Chat"},
	}
	a.scope().sessionsLoaded = true
	a.markUnread("ses_b", false)
	a.markUnread("ses_c", true)
	a.conversation.target.selectSession("ses_a")
	tt := ui.NewTester(a.view, 1400, 920)

	a.setSidebarFilter(sidebarFilterUnread)
	tt.Frame()
	if !tt.HasText("Unread chats") || !tt.HasText("Beta Chat") || !tt.HasText("Gamma Chat") {
		t.Fatalf("unread filter rows: %v", tt.Texts())
	}
	if tt.HasText("Alpha Chat") {
		t.Fatal("the read chat came through the unread filter")
	}

	a.setSidebarFilter(sidebarFilterAttention)
	tt.Frame()
	if !tt.HasText("Gamma Chat") || tt.HasText("Beta Chat") {
		t.Fatalf("attention filter rows: %v", tt.Texts())
	}

	a.conversation.draft = "unsent words"
	a.setSidebarFilter(sidebarFilterDrafts)
	tt.Frame()
	if !tt.HasText("Alpha Chat") || !tt.HasText("Chats with drafts") {
		t.Fatalf("draft filter rows: %v", tt.Texts())
	}
	if tt.HasText("Beta Chat") {
		t.Fatal("a chat without a draft came through the draft filter")
	}

	if err := tt.Click("Clear chat filter"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.sidebarFilter != "" || !tt.HasText("Pinned") {
		t.Fatalf("clearing left filter=%q texts=%v", a.layout.sidebarFilter, tt.Texts())
	}
}

// TestSidebarFilterEmpty names the empty state and the search closes when
// a filter starts.
func TestSidebarFilterEmptyAndSearch(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_a", Title: "Alpha Chat"}}
	a.scope().sessionsLoaded = true
	a.search.searchOpen = true
	a.search.searchText = "alpha"
	a.setSidebarFilter(sidebarFilterDrafts)
	if a.search.searchOpen || a.search.searchText != "" {
		t.Fatalf("filter left search open=%v text=%q", a.search.searchOpen, a.search.searchText)
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("No chats with drafts.") {
		t.Fatalf("empty filter text: %v", tt.Texts())
	}
}
