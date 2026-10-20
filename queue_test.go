package main

import (
	"slices"
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
	if got := a.queuedPrompts("ses_a"); !slices.Equal(got, []string{"also check the other path"}) {
		t.Fatalf("queued prompts %q", got)
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
	if got := a.queuedPrompts("ses_a"); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("queued prompts %q", got)
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	if got := a.queuedTurns("ses_a"); got != 1 {
		t.Fatalf("one start left %d queued", got)
	}
	if got := a.queuedPrompts("ses_a"); !slices.Equal(got, []string{"second"}) {
		t.Fatalf("the first prompt did not leave the queue: %q", got)
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	if got := a.queuedTurns("ses_a"); got != 0 {
		t.Fatalf("two starts left %d queued", got)
	}
}

// TestQueuedMessagesListWaiting shows the waiting messages in the pill's
// panel, in order, as the turns start and the queue drains.
func TestQueuedMessagesListWaiting(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Wired turn"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Wired turn"
	a.conversation.transcript.Start("ses_a")
	for _, text := range []string{"first follow-up", "second follow-up"} {
		a.conversation.draft = text
		if !a.send() {
			t.Fatalf("%q was not accepted", text)
		}
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("2 messages queued"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.layout.queueOpen || !tt.HasText("Waiting behind this turn") {
		t.Fatalf("the queue panel reads: %v", tt.Texts())
	}
	if _, ok := tt.Find("Queued message: first follow-up"); !ok {
		t.Fatalf("the first message is not listed: %v", tt.Texts())
	}
	if _, ok := tt.Find("Queued message: second follow-up"); !ok {
		t.Fatalf("the second message is not listed: %v", tt.Texts())
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	tt.Frame()
	if !a.layout.queueOpen {
		t.Fatalf("the panel closed with a message left: %v", tt.Texts())
	}
	if _, ok := tt.Find("Queued message: second follow-up"); !ok {
		t.Fatalf("the remaining message left the panel: %v", tt.Texts())
	}
	if _, ok := tt.Find("Queued message: first follow-up"); ok {
		t.Fatalf("the started message stayed in the panel: %v", tt.Texts())
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionStarted, SessionID: "ses_a"})
	tt.Frame()
	if a.layout.queueOpen || tt.HasText("Waiting behind this turn") {
		t.Fatal("the panel outlived its queue")
	}
}

// TestQueuedPanelClosesOnChatSwitch keeps the panel of one chat from
// hanging over another.
func TestQueuedPanelClosesOnChatSwitch(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Wired turn"},
		{ID: "ses_b", Title: "Other turn"},
	}
	a.conversation.target.selectSession("ses_a")
	a.conversation.transcript.Start("ses_a")
	a.conversation.draft = "waiting words"
	if !a.send() {
		t.Fatal("the prompt was not accepted")
	}
	a.layout.queueOpen = true
	a.adoptSession(a.scope().sessions[1])
	if a.layout.queueOpen {
		t.Fatal("the queue panel followed to another chat")
	}
}
