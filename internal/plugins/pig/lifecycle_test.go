package pig

import (
	"testing"
	"time"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
)

func TestFailureDoesNotAutomaticallyRecreateBackend(t *testing.T) {
	host := &kittest.Host{Data: t.TempDir()}
	var p *Plugin
	h := kerneltest.Start(t, kerneltest.Config{Lenient: true,
		Restart: kernel.Restart{Max: 3, Within: time.Minute}}, host.New,
		kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	waitFor(t, h, "backend ready", func() bool { return p.Status().State == agent.Ready })
	first := p
	commands := kernel.Items(h, kernel.Commands)
	for _, command := range commands {
		if command.Plugin != "pig" {
			continue
		}
		kernel.Invoke(h, command, func(kernel.Command) { panic("backend callback failed") })
		h.Flush()
		for _, st := range h.Plugins() {
			if st.Manifest.ID == "pig" {
				if st.State != kernel.Failed || st.Restarting || p != first {
					t.Fatalf("failure recreated mutation-capable backend: %+v", st)
				}
				return
			}
		}
		t.Fatal("backend plugin missing")
	}
	t.Fatal("backend command missing")
}
