package desktop

import (
	"fmt"
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

func sidebarListFixture(t testing.TB, mode string, n int) *app {
	t.Helper()
	var a *app
	if mode == "chat" {
		a = newCoreTestApp(t)
	} else {
		a = newTestApp(t)
	}
	dir := canonDir(t.TempDir())
	for i := range n {
		id := fmt.Sprintf("chat-%04d", i)
		a.scope().sessions = append(a.scope().sessions, agent.Session{ID: id, Title: fmt.Sprintf("Chat %04d", i), Directory: dir})
		if mode == "pins" {
			a.scope().pinned[id] = true
			a.scope().pinOrder = append(a.scope().pinOrder, id)
		}
		if mode == "filter" {
			a.scope().unread[id] = true
		}
	}
	if mode == "project" {
		a.preferences.projects = []string{dir}
		a.layout.projectChatsShown[dir] = true
	}
	if mode == "filter" {
		a.setSidebarFilter(sidebarFilterUnread)
	}
	a.scope().sessionsLoaded = true
	return a
}

func TestNormalSidebarBuildsOnlyVisibleRows(t *testing.T) {
	for _, mode := range []string{"chat", "pins", "project", "filter"} {
		t.Run(mode, func(t *testing.T) {
			a := sidebarListFixture(t, mode, 500)
			tt := ui.NewTester(a.view, 1400, 920)
			// Building hundreds of offscreen buttons is the regression. A warm
			// viewport has a bounded allocation count, independent of row count.
			if allocs := testing.AllocsPerRun(3, tt.Frame); allocs > 1500 {
				t.Errorf("sidebar builds offscreen controls: %.0f allocations/frame", allocs)
			}
			tt.Scroll(180, 350, 0, 100000)
			if err := tt.Click("Chat 0499"); err != nil {
				t.Fatal(err)
			}
			if a.conversation.target.session != "chat-0499" {
				t.Fatal("the tail row opened the wrong conversation")
			}
		})
	}
}

func BenchmarkNavigationSidebar(b *testing.B) {
	for _, mode := range []string{"chat", "pins", "project", "filter"} {
		for _, n := range []int{50, 500, 2000} {
			b.Run(fmt.Sprintf("%s/%d", mode, n), func(b *testing.B) {
				a := sidebarListFixture(b, mode, n)
				tt := ui.NewTester(a.view, 1400, 920)
				tt.Frame()
				b.ReportAllocs()
				for b.Loop() {
					tt.Frame()
				}
			})
		}
	}
}

func TestSidebarProjectExpansionSurvivesVirtualRows(t *testing.T) {
	a := sidebarListFixture(t, "project", 8)
	dir := a.preferences.projects[0]
	a.layout.projectChatsShown[dir] = false
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Chat 0007") {
		t.Fatal("project preview includes hidden chats")
	}
	if err := tt.Click("Show 3 more chats"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Chat 0007") {
		t.Fatal("expanding the project did not expose its tail")
	}
	if err := tt.Click("Show fewer chats"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Chat 0007") {
		t.Fatal("project preview did not fold back")
	}
	if err := tt.Click("Hide chats in " + dirBase(dir)); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Chat 0000") {
		t.Fatal("collapsed project retained its chat rows")
	}
	if err := tt.Click("Show chats in " + dirBase(dir)); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Chat 0000") {
		t.Fatal("project did not reopen")
	}
}

func TestSidebarKeepsItsPlaceAcrossSearchAndRowInsertion(t *testing.T) {
	a := sidebarListFixture(t, "chat", 500)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Scroll(180, 350, 0, 300)
	before, ok := tt.Find("Chat 0010")
	if !ok || before.H == 0 {
		t.Fatal("scrolled fixture did not expose a chat")
	}
	a.openChatSearch()
	a.search.searchText = "Chat"
	a.search.searchResults = a.scope().sessions
	tt.Frame()
	a.closeChatSearch()
	a.search.searchOpen = false
	tt.Frame()
	if got, _ := tt.Find("Chat 0010"); got != before {
		t.Fatalf("returning from search moved the chat: %+v -> %+v", before, got)
	}
	// A newly arrived chat above the visible range must not move the anchor.
	a.scope().sessions = append([]agent.Session{{ID: "new", Title: "New arrival"}}, a.scope().sessions...)
	tt.Frame()
	if got, _ := tt.Find("Chat 0010"); got != before {
		t.Fatalf("inserting a chat moved the anchor: %+v -> %+v", before, got)
	}
}

func TestSidebarFilterStartsAtTopAfterNormalListScrolled(t *testing.T) {
	a := sidebarListFixture(t, "chat", 500)
	for _, session := range a.scope().sessions {
		a.scope().unread[session.ID] = true
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Scroll(180, 350, 0, 100000)
	a.setSidebarFilter(sidebarFilterUnread)
	tt.Frame()
	if !tt.HasText("Unread chats") || !tt.HasText("Chat 0000") {
		t.Fatal("filter inherited the previous list's scroll position")
	}
}
