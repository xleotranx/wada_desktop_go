package desktop

import (
	"strings"
	"testing"

	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestEngineNoteKeepsItsLines keeps engine trouble readable: a note's
// error text wraps instead of collapsing into one clipped line, so the
// server's "at" chains stay in the transcript with no hidden tail.
func TestEngineNoteKeepsItsLines(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses-notes")
	one := strings.Repeat("trouble ", 60) // nearly 500 characters of one line's worth
	seeded := []chat.Block{
		{Kind: chat.KindText, Text: "An answer stands above the note."},
		{Kind: chat.KindNote, Text: "Short trouble."},
		{Kind: chat.KindNote, Text: one},
	}
	seedTranscript(a, seeded)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	button, ok := tt.Find("Short trouble.")
	if !ok {
		t.Fatalf("the short note is not in view: %v", tt.Texts())
	}
	long, ok := tt.Find(one)
	if !ok {
		t.Fatal("the long note is not in view")
	}
	if long.H < button.H+10 {
		t.Fatalf("the long note stayed on one line: short=%.0f tall, long=%.0f tall",
			float64(button.H), float64(long.H))
	}
}
