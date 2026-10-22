package kernel_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func TestDiagnosticsCountCancelledWorkUntilItExits(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	scope := p.ctx.NewScope()
	var task kernel.Task
	kernel.Go(scope, &task, func(context.Context) (int, error) { return 0, nil }, func(int, error) {})
	scope.Dispose()
	if d := h.Inspect()[0]; d.Workers != 1 || d.Scopes != 1 {
		t.Fatalf("cancelled work disappeared: %+v", d)
	}
	h.RunWork(0)
	if d := h.Inspect()[0]; d.Workers != 0 {
		t.Fatalf("finished work retained: %+v", d)
	}
}

func TestDiagnosticsDescribeDependenciesAndLiveRegistrations(t *testing.T) {
	provider := backend("provider")
	var used agent
	dependent := consumer("consumer", &used)
	h := kerneltest.New(t, provider, dependent)
	scope := dependent.ctx.NewScope()
	kernel.On(scope, ping, func(string) {})
	kernel.Transform(scope, outgoing, 0, func(s string) (string, error) { return s, nil })
	if err := kernel.Contribute(scope, sidebar, "consumer.item", 0, "item"); err != nil {
		t.Fatal(err)
	}
	all := h.Inspect()
	got := all[1]
	if got.Status.Manifest.ID != "consumer" || got.Scopes != 2 || len(got.Registrations) != 3 || len(got.Requires) != 1 || got.Requires[0].Provider != "provider" {
		t.Fatalf("diagnostics %+v", got)
	}
	scope.Dispose()
	got = h.Inspect()[1]
	if got.Scopes != 1 || len(got.Registrations) != 0 {
		t.Fatalf("disposed scope retained: %+v", got)
	}
	if err := h.Disable("provider"); err != nil {
		t.Fatal(err)
	}
	got = h.Inspect()[1]
	if got.Status.State != kernel.Blocked || got.Scopes != 0 || got.Requires[0].Provider != "" {
		t.Fatalf("blocked %+v", got)
	}
}

func TestDiagnosticsRetainPanicStacks(t *testing.T) {
	p := plug("bad", func(*kernel.Context) error { panic("review probe") })
	h := kerneltest.Start(t, kerneltest.Config{Lenient: true}, p)
	got := h.Inspect()[0]
	var panicErr *kernel.PanicError
	if !errors.As(got.Status.Err, &panicErr) || !strings.Contains(got.Stack, "TestDiagnosticsRetainPanicStacks") {
		t.Fatalf("missing panic details: %+v", got)
	}
}
