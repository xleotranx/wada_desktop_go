package main

import (
	"strings"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestArchiveHidesAndRestoresChat puts a chat away from its project, finds
// it under the archived filter, and brings it back from its own menu.
func TestArchiveHidesAndRestoresChat(t *testing.T) {
	a := newTestApp(t)
	picked := t.TempDir()
	a.addProject(picked)
	dir := canonDir(picked)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha Chat", Directory: dir},
		{ID: "ses_b", Title: "Beta Chat", Directory: dir},
	}
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Alpha Chat") || !tt.HasText("Beta Chat") {
		t.Fatalf("the project does not list its chats: %v", tt.Texts())
	}
	if err := tt.Click("Conversation options, Beta Chat"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Archive chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.chatArchived("ses_b") {
		t.Fatal("the chat was not archived")
	}
	if tt.HasText("Beta Chat") {
		t.Fatalf("an archived chat stayed in its project: %v", tt.Texts())
	}
	if !tt.HasText("Alpha Chat") || !tt.HasText("Chat archived") {
		t.Fatalf("archiving hid the wrong thing: %v", tt.Texts())
	}

	a.setSidebarFilter(sidebarFilterArchived)
	tt.Frame()
	if !tt.HasText("Archived chats") || !tt.HasText("Beta Chat") {
		t.Fatalf("the archived filter does not list the chat: %v", tt.Texts())
	}
	if tt.HasText("Alpha Chat") {
		t.Fatal("a live chat came through the archived filter")
	}
	if err := tt.Click("Conversation options, Beta Chat"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Unarchive chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.chatArchived("ses_b") {
		t.Fatal("the chat stayed archived")
	}
	if !tt.HasText("No archived chats.") {
		t.Fatalf("the emptied filter read wrong: %v", tt.Texts())
	}
	a.setSidebarFilter("")
	tt.Frame()
	if !tt.HasText("Beta Chat") {
		t.Fatalf("the restored chat did not return to its project: %v", tt.Texts())
	}
}

// TestArchivedChatsLeaveCountsAndBanners keeps put-away chats out of the
// unread counts, the bell and the desktop banners until they come back.
func TestArchivedChatsLeaveCountsAndBanners(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_news", Title: "News"},
		{ID: "ses_ask", Title: "Ask"},
	}
	a.markUnread("ses_news", false)
	a.markUnread("ses_ask", true)
	if attention, unread := a.unreadCounts(); attention != 1 || unread != 2 {
		t.Fatalf("counts before archiving: %d, %d", attention, unread)
	}
	a.toggleArchiveChat(a.scope().sessions[0])
	a.toggleArchiveChat(a.scope().sessions[1])
	if attention, unread := a.unreadCounts(); attention != 0 || unread != 0 {
		t.Fatalf("counts after archiving: %d, %d", attention, unread)
	}
	if rows, _ := a.bellChats(); len(rows) != 0 {
		t.Fatalf("the bell lists archived chats: %+v", rows)
	}
	a.openNextMarked()
	if len(commandsOf(a)) != 0 {
		t.Fatal("the next-marked shortcut opened an archived chat")
	}
	var notes []desktopNotice
	a.runtime.notify = func(n desktopNotice) { notes = append(notes, n) }
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionSucceeded, SessionID: "ses_news"})
	a.applyUpdate(opencode.Update{Kind: opencode.PermissionAsked, SessionID: "ses_ask",
		Permission: &opencode.PermissionData{ID: "perm_a", SessionID: "ses_ask", Action: "bash"}})
	if len(notes) != 0 {
		t.Fatalf("archived chats raised banners: %+v", notes)
	}

	// Restoring brings the unread news back into the counts.
	a.toggleArchiveChat(a.scope().sessions[0])
	if attention, unread := a.unreadCounts(); attention != 0 || unread != 1 {
		t.Fatalf("counts after restoring: %d, %d", attention, unread)
	}
}

// TestArchiveDropsPin keeps an archived chat out of the pinned section.
func TestArchiveDropsPin(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_p", Title: "Pinned chat"}}
	a.scope().setPins([]string{"ses_p"})
	a.toggleArchiveChat(a.scope().sessions[0])
	if a.scope().pinned["ses_p"] {
		t.Fatal("an archived chat stayed pinned")
	}
	if a.chatArchived("ses_p") != true {
		t.Fatal("the chat was not archived")
	}
	// Deleting a chat takes its archive away too.
	a.dropSession("ses_p")
	if a.chatArchived("ses_p") {
		t.Fatalf("the deleted chat stayed archived: %v", a.scope().archived)
	}
}

// TestArchivedFilterEmptyAndLabel covers the filter's own wording.
func TestArchivedFilterEmptyAndLabel(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	a.setSidebarFilter(sidebarFilterArchived)
	tt.Frame()
	if !tt.HasText("Archived chats") || !tt.HasText("No archived chats.") {
		t.Fatalf("empty archived filter: %v", tt.Texts())
	}
	if !strings.Contains(a.bellLabel(), "Notifications") {
		t.Fatalf("bell label %q", a.bellLabel())
	}
}

// TestArchiveLeavesRecentsAndBoot keeps the recent-chat keys and the
// launch's landing chat on the chats still listed.
func TestArchiveLeavesRecentsAndBoot(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_new", Title: "Newest"},
		{ID: "ses_old", Title: "Older"},
	}
	a.toggleArchiveChat(a.scope().sessions[0])
	a.openRecentChat(0)
	job := <-commandsOf(a)
	if job.kind != "open" || job.sid != "ses_old" {
		t.Fatalf("the first recent chat is %+v", job)
	}

	b := newTestApp(t)
	b.scope().archived = map[string]bool{"ses_new": true}
	b.settleBoot([]opencode.Session{
		{ID: "ses_new", Title: "Newest"},
		{ID: "ses_old", Title: "Older"},
	}, "", nil)
	if b.conversation.target.session != "ses_old" {
		t.Fatalf("the boot landed on %q", b.conversation.target.session)
	}

	c := newTestApp(t)
	c.scope().archived = map[string]bool{"ses_new": true}
	c.settleBoot([]opencode.Session{{ID: "ses_new", Title: "Newest"}}, "", nil)
	if c.conversation.target.session != "" || !c.conversation.target.isDraft() {
		t.Fatalf("a launch with only archived chats landed on %q", c.conversation.target.session)
	}
}
