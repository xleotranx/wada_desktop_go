package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestQueuedTurnShowsAndStarts sends while a turn runs: the prompt is
// accepted, the composer says it waits, and the next start releases it.
func TestQueuedTurnShowsAndStarts(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Wired turn"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Wired turn"
	a.conversation.transcript.Start("ses_a")
	a.conversation.draft = "also check the other path"
	if !a.send() {
		t.Fatal("the queued prompt was not accepted")
	}
	if got := a.queuedTurns("ses_a"); got != 1 {
		t.Fatalf("queued turns %d", got)
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("1 message queued") || !tt.HasText("runs after this turn") {
		t.Fatalf("the composer did not say it queued: %v", tt.Texts())
	}

	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	if got := a.queuedTurns("ses_a"); got != 0 {
		t.Fatalf("starting the next turn left %d queued", got)
	}
	tt.Frame()
	if tt.HasText("1 message queued") {
		t.Fatal("the queued pill outlived its turn")
	}
}

// TestQueuedTurnIdleIsNotCounted keeps idle prompts out of the queue: they
// run at once, however the runner serializes its own work.
func TestQueuedTurnIdleIsNotCounted(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "start here"
	if !a.send() {
		t.Fatal("the prompt was not accepted")
	}
	if got := a.queuedTurns("ses_a"); got != 0 {
		t.Fatalf("an idle prompt counted %d queued", got)
	}
}

// TestQueuedTurnsStack counts two waiting prompts and releases one per
// start.
func TestQueuedTurnsStack(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.transcript.Start("ses_a")
	for _, text := range []string{"first", "second"} {
		a.conversation.draft = text
		if !a.send() {
			t.Fatalf("%q was not accepted", text)
		}
	}
	if got := a.queuedTurns("ses_a"); got != 2 {
		t.Fatalf("two prompts queued %d", got)
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	if got := a.queuedTurns("ses_a"); got != 1 {
		t.Fatalf("one start left %d queued", got)
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	if got := a.queuedTurns("ses_a"); got != 0 {
		t.Fatalf("two starts left %d queued", got)
	}
}
