package main

import (
	"context"
	"testing"
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
