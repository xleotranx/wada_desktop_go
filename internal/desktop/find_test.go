package desktop

import (
	"fmt"
	"testing"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestChatFindMovesThroughMatches opens the transcript's find bar with
// Cmd+F, counts case-insensitive matches, and lets Enter, Shift+Enter and
// Escape drive it as MyGo's find bar documents.
func TestChatFindMovesThroughMatches(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_find")
	a.conversation.selected = "Find"
	seedTranscript(a, []chat.Block{
		{Kind: chat.KindText, Text: "The Alpha build"},
		{Kind: chat.KindUser, Text: "beta"},
		{Kind: chat.KindText, Text: "alpha again"},
	})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !a.layout.findOpen {
		t.Fatalf("Cmd+F did not open find: open=%v", a.layout.findOpen)
	}
	tt.Type("alpha")
	tt.Frame()
	if !tt.HasText("1 of 2") {
		t.Fatalf("find did not count both matches: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("2 of 2") {
		t.Fatalf("Enter did not move to the next match: %v", tt.Texts())
	}
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("1 of 2") {
		t.Fatalf("Shift+Enter did not move to the previous match: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.layout.findOpen {
		t.Fatal("Escape did not close find")
	}
}

// TestChatFindOpensInAnEmptyConversation keeps Cmd+F answering before the
// first message: the bar appears with nothing to count rather than the key
// looking dead.
func TestChatFindOpensInAnEmptyConversation(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_empty")
	a.conversation.selected = "Empty"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !a.layout.findOpen || !tt.HasText("Done") {
		t.Fatalf("find did not open over an empty conversation: open=%v", a.layout.findOpen)
	}
	tt.Type("anything")
	tt.Frame()
	if !tt.HasText("No matches") {
		t.Fatalf("find did not say it found nothing: %v", tt.Texts())
	}
}

// TestChatFindBringsTheMatchIntoView keeps find useful in a long
// transcript: choosing a match scrolls the virtual list to the row that
// holds it instead of only changing the count.
func TestChatFindBringsTheMatchIntoView(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_find")
	a.conversation.selected = "Find"
	rows := make([]chat.Block, 0, 80)
	for i := range 80 {
		text := fmt.Sprintf("filler row %d", i)
		if i == 40 {
			text = "the needle is here"
		}
		rows = append(rows, chat.Block{Kind: chat.KindText, Text: text})
	}
	seedTranscript(a, rows)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tt.Type("needle")
	tt.Frame()
	tt.Frame()
	first, last := a.layout.feed.Visible()
	if 40 < first || 40 > last {
		t.Fatalf("the matching row 40 is outside the feed's visible rows %d..%d", first, last)
	}
}
