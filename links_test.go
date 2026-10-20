package main

import (
	"slices"
	"strings"
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestChatLinksOpenFilesAndBrowsers sends a workspace link into Files, a
// web link to the desktop, and leaves a heading link where it is.
func TestChatLinksOpenFilesAndBrowsers(t *testing.T) {
	srv, _ := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []opencode.Session{{ID: "ses_links", Title: "Links", Directory: "/srv/app"}}
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
	st := a.workspace.files
	if a.layout.browserTool != "Files" || st == nil || st.path != "src/main.go" {
		t.Fatalf("the entry link landed in %q at %+v", a.layout.browserTool, st)
	}
	if !strings.Contains(st.content, "package main") {
		t.Fatalf("the entry did not read: %q", st.content)
	}
	if err := tt.Click("the notes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.path != "src/deep/notes.txt" || !strings.Contains(st.content, "a long line") {
		t.Fatalf("the notes link landed at %q: %q", st.path, st.content)
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
	if len(tt.OpenedURLs()) != opened || st.path != "src/deep/notes.txt" {
		t.Fatalf("the heading link moved somewhere: %v, %q", tt.OpenedURLs(), st.path)
	}
}
