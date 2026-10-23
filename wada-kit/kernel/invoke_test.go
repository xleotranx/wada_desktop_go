package kernel_test

import (
	"testing"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func TestInvokeDropsDisposedContributions(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p.New)
	scope := p.ctx.NewScope()
	if err := kernel.Contribute(scope, sidebar, "p.item", 0, "old"); err != nil {
		t.Fatal(err)
	}
	old := kernel.Items(h, sidebar)[0]
	called := 0
	invoke := func(e kernel.Entry[string]) { kernel.Invoke(h, e, func(string) { called++ }) }
	invoke(old)
	scope.Dispose()
	if err := kernel.Contribute(p.ctx, sidebar, "p.item", 0, "new"); err != nil {
		t.Fatal(err)
	}
	invoke(old) // Reusing an ID must not revive the old contribution.
	invoke(kernel.Items(h, sidebar)[0])
	if called != 2 {
		t.Fatalf("invoked %d callbacks; want only the two live contributions", called)
	}
}

func TestInvokeDropsPreviousActivation(t *testing.T) {
	p := plug("p", func(c *kernel.Context) error {
		return kernel.Contribute(c, sidebar, "p.item", 0, "item")
	})
	h := kerneltest.New(t, p.New)
	old := kernel.Items(h, sidebar)[0]
	if err := h.Disable("p"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("p"); err != nil {
		t.Fatal(err)
	}
	called := false
	kernel.Invoke(h, old, func(string) { called = true })
	if called {
		t.Fatal("invoked a contribution from the previous activation")
	}
}

func TestInvokeRequiresLiveEntriesFromTheSameKernel(t *testing.T) {
	h := kerneltest.New(t)
	other := kerneltest.New(t)
	if err := kernel.Contribute(h, sidebar, "app.item", 0, "host"); err != nil {
		t.Fatal(err)
	}
	e := kernel.Items(h, sidebar)[0]
	called := 0
	kernel.Invoke(other, e, func(string) { called++ })
	kernel.Invoke(h, kernel.Entry[string]{}, func(string) { called++ })
	kernel.Invoke(h, e, func(string) { called++ })
	h.Close()
	kernel.Invoke(h, e, func(string) { called++ })
	if called != 1 {
		t.Fatalf("invoked %d callbacks; want only the live host contribution", called)
	}
}
