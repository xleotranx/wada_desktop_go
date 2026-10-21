package desktop

import (
	"slices"
	"strings"
	"testing"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestChatLinksOpenFilesAndBrowsers sends a workspace link into Files, a
// web link to the desktop, and leaves a heading link where it is.
func TestChatLinksOpenFilesAndBrowsers(t *testing.T) {
	srv, _ := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []agent.Session{{ID: "ses_links", Title: "Links", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_links")
	a.conversation.selected = "Links"
	seedTranscript(a, []chat.Block{{Kind: chat.KindText, Text: "Read [the entry](./src/main.go#L3), " +
		"[the notes](src/deep/notes.txt) and [the docs](https://example.com).\n\n[Top](#links)"}})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("the entry"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	st := codingModule(a).Files
	if a.layout.browserTool != "Files" || st == nil || st.Path != "src/main.go" {
		t.Fatalf("the entry link landed in %q at %+v", a.layout.browserTool, st)
	}
	if !strings.Contains(st.Content, "package main") {
		t.Fatalf("the entry did not read: %q", st.Content)
	}
	if err := tt.Click("the notes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.Path != "src/deep/notes.txt" || !strings.Contains(st.Content, "a long line") {
		t.Fatalf("the notes link landed at %q: %q", st.Path, st.Content)
	}
	if err := tt.Click("the docs"); err != nil {
		t.Fatal(err)
	}
	if urls := tt.OpenedURLs(); !slices.Contains(urls, "https://example.com") {
		t.Fatalf("the web link opened %v", urls)
	}
	opened := len(tt.OpenedURLs())
	if err := tt.Click("Top"); err != nil {
		t.Fatal(err)
	}
	if len(tt.OpenedURLs()) != opened || st.Path != "src/deep/notes.txt" {
		t.Fatalf("the heading link moved somewhere: %v, %q", tt.OpenedURLs(), st.Path)
	}
}
