package modes

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func start(t *testing.T, store *kerneltest.Store, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat) {
	t.Helper()
	return startWith(t, kerneltest.Config{Store: store}, extra...)
}

func startWith(t *testing.T, cfg kerneltest.Config, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat) {
	t.Helper()
	a := plugintest.NewAgent()
	a.ModeList = []agent.Mode{{ID: "build", Name: "Build"}, {ID: "plan", Name: "Plan"}}
	c := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.Start(t, cfg, append([]kernel.Factory{a.New, c.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })}, extra...)...)
	h.Flush()
	return h, p, a, c
}

// picker draws the mode picker; click chooses in it, which applies once the
// frame is done.
func picker(p *Plugin) *ui.Tester {
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	return tt
}

func click(t *testing.T, h *kerneltest.Harness, tt *ui.Tester, labels ...string) {
	t.Helper()
	for _, label := range labels {
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v in %q", label, err, tt.Texts())
		}
		tt.Frame()
		h.Flush()
		tt.Frame()
	}
}

func TestTheChosenModeGoesWithEveryMessage(t *testing.T) {
	store := kerneltest.NewStore()
	h, p, _, _ := start(t, store)
	tt := picker(p)
	if !tt.HasText("Build") {
		t.Fatalf("picker %q", tt.Texts())
	}
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "build" {
		t.Fatalf("nothing chosen yet, draft %+v", d)
	}
	click(t, h, tt, "Build", "Plan")
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "plan" {
		t.Fatalf("draft %+v", d)
	}
	if d := plugintest.Outgoing(t, h, chat.Draft{Session: "s1", Text: "hi"}); d.Mode != "plan" {
		t.Fatalf("existing conversation did not receive the mode: %+v", d)
	}
	h.Close()

	// Remembered across launches.
	h, _, _, _ = start(t, store)
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "plan" {
		t.Fatalf("after restart: %+v", d)
	}
}

func TestThePickerSitsWithTheSecondaryControls(t *testing.T) {
	h, _, _, _ := start(t, kerneltest.NewStore())
	if len(kernel.Items(h, chat.ComposerPrimary)) != 0 || len(kernel.Items(h, chat.ComposerAccessory)) != 1 {
		t.Fatal("the mode picker is not with the composer's secondary controls")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		for _, e := range kernel.Items(h, chat.ComposerAccessory) {
			kernel.Invoke(h, e, func(v shell.View) { v.Render(c) })
		}
	}, 900, 600)
	tt.Frame()
	if !tt.HasText("Build") {
		t.Fatalf("accessory %q", tt.Texts())
	}
}

func TestOneModeShowsNoPicker(t *testing.T) {
	h, p, a, _ := start(t, kerneltest.NewStore())
	a.ModeList = a.ModeList[:1]
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	if tt := picker(p); tt.HasText("Build") {
		t.Fatalf("a picker for one mode: %q", tt.Texts())
	}
}

// modesOnly provides the backend's modes apart from the rest of it.
type modesOnly struct{ backend *plugintest.Agent }

func (*modesOnly) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "modes-only", Provides: []kernel.Named{agent.ModesKey}}
}
func (m *modesOnly) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Modes](c, agent.ModesKey, m.backend)
}

// serviceOnly provides the agent service without modes.
type serviceOnly struct{ backend *plugintest.Agent }

func (*serviceOnly) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "service-only", Provides: []kernel.Named{agent.ServiceKey}}
}
func (s *serviceOnly) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Service](c, agent.ServiceKey, s.backend)
}

func TestModesFollowAProviderThatComesAndGoes(t *testing.T) {
	a := plugintest.NewAgent()
	a.ModeList = []agent.Mode{{ID: "build", Name: "Build"}, {ID: "plan", Name: "Plan"}}
	c := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.New(t,
		func() kernel.Plugin { return &serviceOnly{backend: a} },
		func() kernel.Plugin { return &modesOnly{backend: a} },
		c.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	h.Flush()
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "build" {
		t.Fatalf("with modes: %+v", d)
	}
	h.Disable("modes-only")
	h.Flush()
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "" {
		t.Fatalf("modes outlived their provider: %+v", d)
	}
	h.Enable("modes-only")
	h.Flush()
	if d := plugintest.Outgoing(t, h, chat.Draft{Text: "hi"}); d.Mode != "build" {
		t.Fatalf("a provider enabled later was not noticed: %+v", d)
	}
}
