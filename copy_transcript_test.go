package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestCopyTranscript puts the conversation on the clipboard from the chat
// menu and from the command palette, as the markdown the export writes.
func TestCopyTranscript(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.conversation.selected = "Boundary work"
	a.conversation.target.selectSession("ses-copy")
	seedTranscript(a, sampleTranscript())
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Chat options, Boundary work"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Copy transcript"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"# Boundary work", "## You", "## Codex", "> the engine blinked"} {
		if !strings.Contains(copied, want) {
			t.Fatalf("the copy lost %q:\n%s", want, copied)
		}
	}
	if !tt.HasText("Transcript copied") {
		t.Fatalf("the copy said nothing: %v", tt.Texts())
	}

	copied = ""
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	tt.Type("copy transcript")
	tt.Frame()
	if err := tt.Click("Copy transcript"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.Contains(copied, "## Codex") {
		t.Fatalf("the palette copied %q", copied)
	}
}
