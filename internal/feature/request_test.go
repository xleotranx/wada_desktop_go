package feature

import (
	"context"
	"testing"
)

func TestModuleLifetimeRejectsPendingWorkAndPublication(t *testing.T) {
	for _, finishWork := range []bool{false, true} {
		t.Run(map[bool]string{false: "before work", true: "before publication"}[finishWork], func(t *testing.T) {
			lifetime, stop := context.WithCancel(context.Background())
			defer stop()
			var worker func() func()
			host := Host{Lifetime: lifetime, Context: func() Context { return Context{Work: context.Background(), Revision: 1} }, Background: func(work func() func()) { worker = work }}
			worked, applied := false, false
			Run(host, new(Request), func(context.Context) (int, error) { worked = true; return 1, nil }, func(int, error) { applied = true })
			var publish func()
			if finishWork {
				publish = worker()
			}
			stop()
			if !finishWork {
				publish = worker()
			}
			publish()
			if applied || worked != finishWork {
				t.Fatalf("closed module: worked=%v applied=%v", worked, applied)
			}
		})
	}
}
