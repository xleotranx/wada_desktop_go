package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// mentionServer answers file searches over one remote workspace.
func mentionServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/find", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("location[directory]") != "/srv/app" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		switch r.URL.Query().Get("query") {
		case "sr":
			w.Write([]byte(`{"data":[{"path":"src/app.go","type":"file"},` +
				`{"path":"src/agent.go","type":"file"},{"path":"src","type":"directory"}]}`))
		default:
			w.Write([]byte(`{"data":[]}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestMentionToken finds the @-word only where it starts a word and runs
// to the end of the draft.
func TestMentionToken(t *testing.T) {
	cases := []struct {
		draft string
		start int
		query string
		ok    bool
	}{
		{"look at @src/app.go", 8, "src/app.go", true},
		{"@", 0, "", true},
		{"fix\n@src/", 4, "src/", true},
		{"mail me@example.com", 0, "", false},
		{"@done and sent", 0, "", false},
		{"no token", 0, "", false},
	}
	for _, tc := range cases {
		start, query, ok := mentionToken(tc.draft)
		if ok != tc.ok || (ok && (start != tc.start || query != tc.query)) {
			t.Errorf("mentionToken(%q) = %d, %q, %v; want %d, %q, %v",
				tc.draft, start, query, ok, tc.start, tc.query, tc.ok)
		}
	}
}

// TestMentionSearchesAndInserts types an @-word, walks the found files and
// writes one into the draft instead of sending the half-typed word; the
// next Enter sends the completed prompt.
func TestMentionSearchesAndInserts(t *testing.T) {
	srv := mentionServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-mention", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-mention"
	a.scope().sessions = []agent.Session{{ID: "ses_m", Title: "Mention", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_m")
	a.conversation.selected = "Mention"
	a.layout.composerFocus = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Type("@sr")
	tt.Frame()
	if !tt.HasText("src/app.go") || !tt.HasText("src/agent.go") {
		t.Fatalf("the @ menu lacks the found files: %v", tt.Texts())
	}
	if len(codingModule(a).Mention.Entries) != 2 {
		t.Fatalf("the menu kept directories or lost files: %+v", codingModule(a).Mention.Entries)
	}
	if _, found := tt.Find("Mention src"); found {
		t.Fatal("a directory reached the file mentions")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := a.conversation.draft; got != "@src/agent.go " {
		t.Fatalf("Enter left the draft as %q", got)
	}
	if len(commandsOf(a)) != 0 {
		t.Fatal("Enter sent the half-typed mention instead of completing it")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	job := <-commandsOf(a)
	if job.kind != "prompt" || job.text != "@src/agent.go" || job.dir != "/srv/app" {
		t.Fatalf("the completed prompt queued %+v", job)
	}
	if a.conversation.draft != "" {
		t.Fatalf("the sent draft remains %q", a.conversation.draft)
	}
}

// TestMentionEscapeDismisses mutes the menu until the @-word is gone, then
// lets a new one open again.
func TestMentionEscapeDismisses(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-mention", name: "Build box", url: mentionServer(t).URL})
	a.connections.activeServer = "srv-mention"
	a.scope().sessions = []agent.Session{{ID: "ses_m", Title: "Mention", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_m")
	a.layout.composerFocus = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Type("@sr")
	tt.Frame()
	if !tt.HasText("src/app.go") {
		t.Fatalf("the menu did not open: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("src/app.go") {
		t.Fatalf("Escape left the menu up: %v", tt.Texts())
	}
	tt.Type("c")
	tt.Frame()
	if tt.HasText("src/app.go") || tt.HasText("Mention ") {
		t.Fatalf("typing after Escape brought the menu back: %v", tt.Texts())
	}
	// A space ends the word, which clears the mute for the next one.
	tt.Type(" ")
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	if !a.mentionOpen() {
		t.Fatalf("a new @-word stayed muted: %v", tt.Texts())
	}
}

// TestMentionNeedsAFolder keeps the menu honest on a remote draft that has
// no workspace folder to search.
func TestMentionNeedsAFolder(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-mention", name: "Build box", url: mentionServer(t).URL})
	a.connections.activeServer = "srv-mention"
	a.layout.composerFocus = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	if !tt.HasText("Choose a project to mention its files.") {
		t.Fatalf("the menu guessed a folder: %v", tt.Texts())
	}
	if codingModule(a).Mention.Loading || len(codingModule(a).Mention.Entries) != 0 {
		t.Fatalf("a search ran without a folder: %+v", codingModule(a).Mention)
	}
}
