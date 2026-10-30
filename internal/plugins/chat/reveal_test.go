package chat

import (
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
)

func TestPaceShowsWholeWordsAndCatchesUp(t *testing.T) {
	text := strings.Repeat("the quick brown fox jumps over the moon and repeats ", 40)
	var s pace
	now := time.Unix(0, 0)
	shown, frames := 0, 0
	for more := true; more; frames++ {
		more = s.step(text, now)
		if s.shown < shown {
			t.Fatalf("frame %d went back from %d to %d", frames, shown, s.shown)
		}
		if s.shown > 0 && s.shown < len(text) && text[s.shown-1] != ' ' {
			t.Fatalf("frame %d stops inside a word: %q", frames, text[s.shown-8:s.shown])
		}
		shown = s.shown
		now = now.Add(time.Second / revealFPS)
		if frames > 3*revealFPS {
			t.Fatalf("%d of %d bytes show after three seconds", s.shown, len(text))
		}
	}
	if frames < 5 {
		t.Fatalf("%d bytes showed in %d frames, not a word at a time", len(text), frames)
	}
}

func TestPaceShowsOtherTextAtOnce(t *testing.T) {
	var s pace
	now := time.Unix(0, 0)
	s.step("one two three four five six seven eight nine ten", now)
	if s.shown == 0 {
		t.Fatal("nothing showed")
	}
	if more := s.step("a different answer", now.Add(time.Second/revealFPS)); more || s.shown != len("a different answer") {
		t.Fatalf("an edit showed %d bytes, more %v", s.shown, more)
	}
}

func TestArrivingWordsShowWithThePacedFrame(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	words := strings.Repeat("streamed words arrive here ", 20)
	f.Emit(agent.Event{Kind: agent.TurnStarted, Session: "s1"}, textEvent(agent.TextDelta, "s1", words, ""))
	h.RunPosts()
	tt.Frame()
	r := p.t.rows[0]
	if r.reveal == nil || r.reveal.shown == 0 || r.reveal.shown >= len(words) || !p.frameDue {
		t.Fatalf("the first frame: %d of %d bytes, frame due %v", r.reveal.shown, len(words), p.frameDue)
	}
	frames := h.Frames()
	f.Emit(textEvent(agent.TextDelta, "s1", "more ", ""))
	h.RunPosts()
	if h.Frames() != frames {
		t.Fatalf("words asked for a frame of their own while one was due: %d → %d", frames, h.Frames())
	}
	for range 3 * revealFPS {
		tt.Advance(time.Second / revealFPS)
	}
	// The frames it asked for showed the rest.
	if r.reveal.shown != len(r.text) || p.frameDue {
		t.Fatalf("after catching up: %d of %d bytes, frame due %v", r.reveal.shown, len(r.text), p.frameDue)
	}
	// Caught up, the next words ask for the frame that starts showing them.
	f.Emit(textEvent(agent.TextDelta, "s1", strings.Repeat("and the last words ", 10), ""))
	h.RunPosts()
	if h.Frames() != frames+1 {
		t.Fatalf("frames %d → %d for words with no frame due, want one", frames, h.Frames())
	}
	// A finished answer shows what is left of it as it would have.
	f.Emit(textEvent(agent.TextEnded, "s1", "", r.text), agent.Event{Kind: agent.TurnSucceeded, Session: "s1"})
	h.RunPosts()
	tt.Frame()
	if r.kind != rowText || r.reveal == nil {
		t.Fatalf("the finished answer: kind %v reveal %+v", r.kind, r.reveal)
	}
	for range 3 * revealFPS {
		tt.Advance(time.Second / revealFPS)
	}
	if r.reveal != nil || !tt.HasText("and the last") {
		t.Fatalf("the finished answer did not catch up: %+v", r.reveal)
	}
}
