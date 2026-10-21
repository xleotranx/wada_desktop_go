package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

func sampleTranscript() []chat.Block {
	return []chat.Block{
		{Kind: chat.KindUser, Text: "Ship the boundary check"},
		{Kind: chat.KindText, Text: "The check guards the edge.", Cost: 0.0123, Tokens: 460},
		{Kind: chat.KindActivity, Text: "shell", Detail: []string{"$ go test ./...", "ok"}},
		{Kind: chat.KindNote, Text: "the engine blinked"},
	}
}

// TestTranscriptMarkdownShapesTheExport keeps the export honest: what was
// asked and answered, what the agent ran with its transcript of detail,
// and the engine notes, in the order the view draws them.
func TestTranscriptMarkdownShapesTheExport(t *testing.T) {
	out := transcriptMarkdown("Boundary work", sampleTranscript(), time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC))
	for _, want := range []string{
		"# Boundary work",
		"## You\n\nShip the boundary check",
		"## Codex\n\nThe check guards the edge.",
		"_$0.01",
		"460 tokens",
		"```", "$ go test ./...", "```",
		"> the engine blinked",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the export lost %q:\n%s", want, out)
		}
	}
}

// TestExportNameFilenames keeps titles from breaking the file system:
// separators turn into dashes, runes stay whole, and a blank title
// stands as "conversation".
func TestExportNameFilenames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Plan the folder", "Plan the folder"},
		{"a/b\\c:d", "a-b-c-d"},
		{"...dots...", "dots"},
		{"", "conversation"},
		{"a\nb", "a-b"},
	}
	for _, tc := range cases {
		if got := exportName(tc.in); got != tc.want {
			t.Fatalf("exportName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := exportName(strings.Repeat("春", 80)); len([]rune(got)) != 60 {
		t.Fatalf("exportName truncated %q to %q", strings.Repeat("春", 80), got)
	}
}

// TestExportTranscriptWritesTheFile drives the export end to end: the
// chooser's answer lands the conversation in markdown, and the chat menu
// offers the item to get there.
func TestExportTranscriptWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Boundary work.md")

	a := newTestApp(t)
	a.runtime.pickExport = func(done func(path string)) { done(path) }
	a.scope().sessionsLoaded = true
	a.conversation.selected = "Boundary work"
	a.conversation.target.selectSession("ses-export")
	seedTranscript(a, sampleTranscript())
	a.exportTranscript()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the export did not land: %v", err)
	}
	if !strings.Contains(string(body), "# Boundary work") {
		t.Fatalf("the export misses the title: %s", body)
	}

	// No chooser answer, no file: a cancel exports nothing.
	cancel := filepath.Join(dir, "cancel.md")
	a.runtime.pickExport = func(done func(path string)) { done("") }
	a.exportTranscript()
	if _, err := os.Stat(cancel); !os.IsNotExist(err) {
		t.Fatal("a canceled chooser exported a file")
	}
}

// TestChatMenuCarriesExport keeps the title's kebab wired: it opens with
// the rename, export and delete items the sidebar's kebab carries.
func TestChatMenuCarriesExport(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)

	if tt.HasText("Export transcript…") {
		t.Fatalf("a draft shows a conversation menu: %v", tt.Texts())
	}
	a.scope().sessions = append(a.scope().sessions, agent.Session{ID: "ses-menu", Title: "Menu bearer"})
	a.adoptSession(a.scope().sessions[0])
	a.conversation.target.selectSession("ses-menu")
	a.conversation.selected = a.titleOf("ses-menu")
	tt.Frame()
	if err := tt.Click("Chat options, Menu bearer"); err != nil {
		t.Fatalf("the chat kebab did not open: %v", err)
	}
	menu := tt.Menu()
	if len(menu) == 0 {
		t.Fatal("the menu did not open")
	}
	for _, want := range []string{"Rename…", "Export transcript…", "Delete"} {
		found := false
		for _, item := range menu {
			if strings.Contains(item, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("the chat menu carries no %q: %v", want, menu)
		}
	}
}
