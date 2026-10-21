package desktop

import (
	"slices"
	"testing"
	"time"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo/ui"
)

// TestChatHistoryNewestFirst opens the history from the rail, checks its
// order and hiding of archived chats, filters to one chat and opens it
// with Enter.
func TestChatHistoryNewestFirst(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	a.scope().sessions = []agent.Session{
		{ID: "ses_old", Title: "Old chat", Directory: "/work/old", Updated: ago(3 * 24 * time.Hour)},
		{ID: "ses_new", Title: "New chat", Directory: "/work/new", Updated: ago(2 * time.Minute)},
		{ID: "ses_arch", Title: "Archived chat", Updated: ago(time.Second)},
		{ID: "ses_mid", Title: "Mid chat", Directory: "/work/mid", Updated: ago(2 * time.Hour)},
	}
	a.scope().archived["ses_arch"] = true
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("History"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.layout.historyOpen || !tt.HasText("Chat history") {
		t.Fatalf("the history did not open: %v", tt.Texts())
	}
	ids := []string{}
	for _, s := range a.historySessions() {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"ses_new", "ses_mid", "ses_old"}) {
		t.Fatalf("the history reads %v", ids)
	}
	if tt.HasText("Archived chat") {
		t.Fatalf("an archived chat is listed: %v", tt.Texts())
	}
	if !tt.HasText("2h ago") || !tt.HasText("3 days ago") {
		t.Fatalf("the times are missing: %v", tt.Texts())
	}

	if err := tt.Click("Filter history"); err != nil {
		t.Fatal(err)
	}
	tt.Type("mid")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.layout.historyOpen || a.conversation.target.session != "ses_mid" {
		t.Fatalf("Enter left open=%v chat=%q", a.layout.historyOpen, a.conversation.target.session)
	}

	// The command palette opens the same page.
	a.runCommand("action:history")
	if !a.layout.historyOpen {
		t.Fatal("the palette action did not open the history")
	}
}

// TestChatHistoryLoadsOlderPages asks for the next page and shows what the
// sink appends.
func TestChatHistoryLoadsOlderPages(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_a", Title: "Alpha", Updated: time.Now().UnixMilli()}}
	a.scope().sessionsLoaded = true
	a.scope().nextCursor = "page2"
	a.connections.booted = true
	a.layout.historyOpen = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Load older chats"); err != nil {
		t.Fatal(err)
	}
	job := <-commandsOf(a)
	if job.kind != "more" || job.cursor != "page2" {
		t.Fatalf("the page asked %+v", job)
	}
	if !a.scope().moreLoading {
		t.Fatal("the read does not show as running")
	}
	runnerSink{a: a}.Sessions(runner.Page{
		Sessions: []agent.Session{{ID: "ses_b", Title: "Beta", Updated: time.Now().Add(-time.Hour).UnixMilli()}},
		Cursor:   "page3", More: true,
	})
	if a.scope().moreLoading || a.scope().nextCursor != "page3" {
		t.Fatalf("the page landed loading=%v cursor=%q", a.scope().moreLoading, a.scope().nextCursor)
	}
	if !slices.ContainsFunc(a.historySessions(), func(s agent.Session) bool { return s.ID == "ses_b" }) {
		t.Fatalf("the older chat is missing: %+v", a.scope().sessions)
	}
	tt.Frame()
	if _, ok := tt.Find("Open chat Beta"); !ok {
		t.Fatalf("the older chat is not drawn: %v", tt.Texts())
	}
}

// TestHistoryWhenAt keeps the ages short and readable.
func TestHistoryWhenAt(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{30 * time.Hour, "yesterday"},
		{3 * 24 * time.Hour, "3 days ago"},
		{40 * 24 * time.Hour, now.Add(-40 * 24 * time.Hour).Format("Jan 2")},
		{0, "just now"},
	}
	for _, tc := range cases {
		if got := historyWhenAt(at(tc.ago), now); got != tc.want {
			t.Fatalf("%v reads %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := historyWhenAt(0, now); got != "" {
		t.Fatalf("no time reads %q", got)
	}
}
