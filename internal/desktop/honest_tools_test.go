package desktop

import (
	"strings"
	"testing"

	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestToolsPaneListsRealTools keeps the tools grid honest: it offers only
// what this pane implements, with no cards for terminal sessions or side
// chats the app cannot open, and no demo suggestions.
func TestToolsPaneListsRealTools(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Tools"); err != nil {
		t.Fatalf("opening the tools pane: %v", err)
	}
	for _, real := range []string{"Changes", "Files"} {
		if !tt.HasText(real) {
			t.Fatalf("the tools grid lost %q: %v", real, tt.Texts())
		}
	}
	for _, gone := range []string{"Terminal", "Side chat", "Suggested", "127.0.0.1", "New page"} {
		if tt.HasText(gone) {
			t.Fatalf("the tools pane still advertises %q: %v", gone, tt.Texts())
		}
	}
}

// TestSendButtonHonestStates checks the composer's right button follows
// the draft and the running turn: inert when there is nothing to send,
// clickable once the user typed, a stop control while the answer streams.
// The dead voice-session circle is gone.
func TestSendButtonHonestStates(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses-existing")
	seedTranscript(a, []chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
	tt := ui.NewTester(a.view, 1400, 920)

	// The empty draft's send button sits in place but an inert click
	// sends nothing.
	if tt.HasText("Voice session") {
		t.Fatalf("the voice session stub came back: %v", tt.Texts())
	}
	if !tt.HasText("Send message") {
		t.Fatalf("the empty draft lost its send button: %v", tt.Texts())
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("clicking the idle send button: %v", err)
	}
	tt.Frame()
	if len(commandsOf(a)) != 0 || a.conversation.transcript.Len() != 1 {
		t.Fatalf("an idle send submitted or changed the transcript: %d jobs, %+v",
			len(commandsOf(a)), a.conversation.transcript.Snapshot())
	}

	// Typing lights it up, and the click sends the draft as the user's
	// bubble.
	a.conversation.draft = "hello"
	tt.Frame()
	found := false
	for _, text := range tt.Texts() {
		if strings.Contains(text, "Send message") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the typed draft lost its send button: %v", tt.Texts())
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("clicking the typed send button: %v", err)
	}
	tt.Frame()
	if len(commandsOf(a)) != 1 {
		t.Fatalf("the typed send queued %d jobs, want 1", len(commandsOf(a)))
	}
	job := <-commandsOf(a)
	if job.kind != "prompt" || job.text != "hello" {
		t.Fatalf("the typed send queued %+v", job)
	}
	if !tt.HasText("hello") {
		t.Fatalf("the typed send button did not send: %v", tt.Texts())
	}

	// A running turn turns it into a stop control.
	a.conversation.transcript.Start("ses-existing")
	tt.Frame()
	if !tt.HasText("Stop the agent") {
		t.Fatalf("the running turn shows no stop control: %v", tt.Texts())
	}
}
