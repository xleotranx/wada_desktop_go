package sessions

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat, *plugintest.Host) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{
		{ID: "s1", Title: "First", Directory: "/w/app"}, {ID: "s2", Title: "Second"}}, Next: "c2"}
	a.Pages["c2|"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s2", Title: "Second"}, {ID: "s3", Title: "Third"}}}
	a.Pages["|thi"] = agent.SessionPage{Sessions: []agent.Session{{ID: "s3", Title: "Third"}}}
	chat, host := &plugintest.Chat{}, &plugintest.Host{}
	p := &Plugin{searchDelay: 1}
	h := kerneltest.New(t, a, chat, host, p)
	h.Flush()
	return h, p, a, chat, host
}

func ids(list []agent.Session) []agent.SessionID {
	var out []agent.SessionID
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

func TestListLoadsPagesAndOpens(t *testing.T) {
	h, p, _, chat, _ := start(t)
	if !slices.Equal(ids(p.sessions), []agent.SessionID{"s1", "s2"}) || p.next != "c2" {
		t.Fatalf("first page %v next %q", ids(p.sessions), p.next)
	}
	p.loadMore()
	h.Flush()
	if !slices.Equal(ids(p.sessions), []agent.SessionID{"s1", "s2", "s3"}) || p.next != "" {
		t.Fatalf("after more %v next %q", ids(p.sessions), p.next)
	}
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if !tt.HasText("First") || !tt.HasText("app") || tt.HasText("Show more") {
		t.Fatalf("rows %q", tt.Texts())
	}
	if err := tt.Click("Second"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if chat.Conv.Session != "s2" {
		t.Fatalf("opened %+v", chat.Conv)
	}
}

func TestSearchReplacesTheList(t *testing.T) {
	h, p, a, _, _ := start(t)
	p.query = "t"
	p.searchFor(p.query)
	p.query = "thi"
	p.searchFor(p.query)
	h.Flush()
	if got := a.Calls(); !slices.Equal(got, []string{"sessions |", "sessions |thi"}) {
		t.Fatalf("calls %q", a.Calls())
	}
	tt := ui.NewTester(p.view, 320, 600)
	tt.Frame()
	if !tt.HasText("Third") || tt.HasText("First") {
		t.Fatalf("search rows %q", tt.Texts())
	}
	p.query = ""
	p.searchFor("")
	tt.Frame()
	if !tt.HasText("First") {
		t.Fatalf("cleared search %q", tt.Texts())
	}
}

func TestFinishedTurnElsewhereMarksUnread(t *testing.T) {
	h, p, a, chat, _ := start(t)
	chat.Open(agent.Session{ID: "s1"})
	before := a.Called("sessions |")
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.TurnSucceeded, Session: "s1"},
		{Kind: agent.TurnSucceeded, Session: "s2"},
		{Kind: agent.SessionRenamed, Session: "s2", Title: "Renamed"},
	})
	h.Flush()
	if p.unread["s1"] || !p.unread["s2"] || a.Called("sessions |") != before+1 {
		t.Fatalf("unread %v refreshes %d", p.unread, a.Called("sessions |")-before)
	}
	p.open(p.sessions[1])
	if p.unread["s2"] {
		t.Fatal("opening did not clear the mark")
	}
}

func TestRenameAndDelete(t *testing.T) {
	h, p, a, chat, host := start(t)
	chat.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	p.startRename(p.sessions[0])
	p.title = "  Better  "
	p.saveRename()
	h.Flush()
	if p.sessions[0].Title != "Better" || a.Called("rename s1 Better") != 1 {
		t.Fatalf("rename: %q calls %q", p.sessions[0].Title, a.Calls())
	}
	p.startDelete(p.sessions[0])
	p.confirmDelete()
	h.Flush()
	if slices.Contains(ids(p.sessions), "s1") || chat.Conv.Session != "" || chat.Conv.Directory != "/w/app" {
		t.Fatalf("delete: %v conv %+v", ids(p.sessions), chat.Conv)
	}
	a.Fail["Delete"] = errors.New("gone")
	p.startDelete(p.sessions[0])
	p.confirmDelete()
	h.Flush()
	if len(host.Toasts) != 1 || !slices.Contains(ids(p.sessions), "s2") {
		t.Fatalf("failed delete: toasts %q list %v", host.Toasts, ids(p.sessions))
	}
}
