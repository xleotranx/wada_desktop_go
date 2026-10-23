package inspector

import (
	"errors"
	"testing"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"

	"github.com/egoist/mygo/ui"
)

type snapshot struct{ rows []kernel.Diagnostic }

func (*snapshot) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "probe", Provides: []kernel.Named{kernel.InspectorKey}}
}
func (s *snapshot) Setup(c *kernel.Context) error {
	return kernel.Provide[kernel.Inspector](c, kernel.InspectorKey, s)
}
func (s *snapshot) Inspect() []kernel.Diagnostic { return s.rows }
func (s *snapshot) New() kernel.Plugin           { return s }

func TestInspectorShowsDependenciesResourcesAndFailures(t *testing.T) {
	probe := &snapshot{rows: []kernel.Diagnostic{{
		Status:   kernel.Status{Manifest: kernel.Manifest{ID: "chat"}, State: kernel.Blocked, Missing: []string{"agent.service"}, Err: errors.New("startup failed")},
		Requires: []kernel.Dependency{{Key: "agent.service", Provider: "pi"}}, Scopes: 2, Workers: 1,
		Registrations: []kernel.Registration{{Kind: "contribution", Name: "shell.pages", ID: "chat.page"}}, Stack: "panic stack details",
	}}}
	host := &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, probe.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	for _, cmd := range kernel.Items(h, kernel.Commands) {
		if cmd.ID == "inspector.open" {
			kernel.Invoke(h, cmd, func(c kernel.Command) { c.Run() })
		}
	}
	if host.Page() != pageID {
		t.Fatal("command did not open inspector")
	}
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if err := tt.Click("Inspect chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"chat · blocked", "2 scopes · 1 workers · 1 registrations", "Missing: agent.service", "agent.service ← pi", "contribution · shell.pages · chat.page", "startup failed", "panic stack details"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	probe.rows[0].Workers = 0
	tt.Frame()
	if !tt.HasText("2 scopes · 0 workers · 1 registrations") {
		t.Fatal("counts did not refresh")
	}
	if err := h.Disable("inspector"); err != nil {
		t.Fatal(err)
	}
	if len(kernel.Items(h, shell.Pages)) != 0 {
		t.Fatal("disabled inspector left its page")
	}
}
