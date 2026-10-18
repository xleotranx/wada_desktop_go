package main

import (
	"context"
	"testing"
)

// Interaction tests explicitly finish requests immediately. Ordering tests
// substitute a manual executor to exercise separate work and publication.
func newTestApp(t testing.TB) *app {
	t.Helper()
	a := newApp()
	a.background = func(work func() func()) { work()() }
	t.Cleanup(func() { a.closeWork() })
	return a
}

func TestBackgroundRejectsPostedResultsAfterSwitchOrClose(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		a := newTestApp(t)
		var task func() func()
		a.background = func(work func() func()) { task = work }
		applied := false
		inBackground(a, func(ctx context.Context) (int, error) { return 7, nil }, func(int, error) { applied = true })
		publish := task()
		if closeApp {
			a.closed = true
		} else {
			a.route++
		}
		publish()
		if applied {
			t.Fatal("obsolete result changed state")
		}
	}
}
