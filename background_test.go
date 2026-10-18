package main

import (
	"context"
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
)

// Interaction tests explicitly finish requests immediately. Ordering tests
// substitute a manual executor to exercise separate work and publication.
func newTestApp(t testing.TB) *app {
	t.Helper()
	a := newApp()
	installCommandFixture(a, 16)
	a.runtime.background = func(work func() func()) { work()() }
	t.Cleanup(func() { a.closeWork(); testCommandQueues.Delete(a) })
	return a
}

func TestBackgroundRejectsPostedResultsAfterSwitchOrClose(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		a := newTestApp(t)
		var task func() func()
		a.runtime.background = func(work func() func()) { task = work }
		applied := false
		inBackground(a, func(ctx context.Context) (int, error) { return 7, nil }, func(int, error) { applied = true })
		publish := task()
		if closeApp {
			a.runtime.closed = true
		} else {
			a.connections.route++
		}
		publish()
		if applied {
			t.Fatal("obsolete result changed state")
		}
	}
}

// apply is a wire fixture adapter. Production publishes decoded updates.
func (a *app) apply(ev opencode.Event) {
	update, known, err := opencode.DecodeEvent(ev)
	if err == nil && known {
		a.applyUpdate(update)
	}
}

func setRowText(a *app, index int, text string) {
	rows := a.conversation.transcript.Snapshot()
	rows[index].Text = text
	seedTranscript(a, rows)
}

// seedTranscript sets row fixtures independently from the simulated turn.
func seedTranscript(a *app, rows []chat.Block) {
	busy := a.conversation.transcript.Busy()
	a.conversation.transcript.Replace(rows)
	if busy != "" {
		a.conversation.transcript.Start(busy)
	}
}
