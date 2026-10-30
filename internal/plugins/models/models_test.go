package models

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

var (
	alpha = agent.ModelRef{Provider: "acme", ID: "alpha"}
	beta  = agent.ModelRef{Provider: "acme", ID: "beta"}
)

func start(t *testing.T, store *kerneltest.Store, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	return startWith(t, kerneltest.Config{Store: store}, extra...)
}

func startWith(t *testing.T, cfg kerneltest.Config, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Catalog = []agent.Model{
		{Ref: alpha, Name: "Alpha", Free: true},
		{Ref: beta, Name: "Beta", Variants: []string{"low", "high"}},
	}
	a.Default = alpha
	c, host := &plugintest.Chat{}, &kittest.Host{}
	var p *Plugin
	h := kerneltest.Start(t, cfg, append([]kernel.Factory{a.New, c.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })}, extra...)...)
	h.Flush()
	return h, p, a, c, host
}

func outgoing(t *testing.T, h *kerneltest.Harness, d chat.Draft) chat.Draft {
	t.Helper()
	out, err := send(h, d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNewChatUsesTheChosenModel(t *testing.T) {
	store := kerneltest.NewStore()
	h, p, _, _, _ := start(t, store)
	tt := ui.NewTester(p.view, 900, 600)
	tt.Frame()
	if !tt.HasText("Alpha") || tt.HasText("Default effort") {
		t.Fatalf("pickers %q", tt.Texts())
	}
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); !d.Model.IsZero() {
		t.Fatalf("nothing chosen yet, draft %+v", d)
	}
	for _, click := range []string{"Alpha", "Beta"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v in %q", click, err, tt.Texts())
		}
		tt.Frame()
	}
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); d.Model != beta {
		t.Fatalf("draft %+v", d)
	}
	if d := outgoing(t, h, chat.Draft{Session: "s1", Text: "hi"}); !d.Model.IsZero() {
		t.Fatalf("an existing conversation was given the new chat's model: %+v", d)
	}
	h.Close()

	// Remembered across launches; a model the backend dropped is not sent.
	h, p, a, _, _ := start(t, store)
	if p.current() != beta {
		t.Fatalf("current %+v after restart", p.current())
	}
	a.Catalog = a.Catalog[:1]
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); !d.Model.IsZero() {
		t.Fatalf("draft with a dropped model: %+v", d)
	}
}

func TestThePickerReloadsWhenTheCatalogChanges(t *testing.T) {
	h, p, a, _, _ := start(t, kerneltest.NewStore())
	tt := ui.NewTester(p.view, 900, 600)
	tt.Frame()
	if err := tt.Click("Alpha"); err != nil {
		t.Fatalf("open the picker: %v", err)
	}
	tt.Frame()
	if tt.HasText("Gamma") {
		t.Fatal("Gamma is listed before the backend offers it")
	}
	a.Catalog = append(a.Catalog, agent.Model{Ref: agent.ModelRef{Provider: "acme", ID: "gamma"}, Name: "Gamma"})
	kernel.Emit(h, agent.ModelsChanged, struct{}{})
	h.Flush()
	tt.Frame()
	if !tt.HasText("Gamma") {
		t.Fatalf("the picker did not reload: %q", tt.Texts())
	}
}

func TestOpenConversationSwitchesItsModel(t *testing.T) {
	h, p, a, c, host := start(t, kerneltest.NewStore())
	c.Open(agent.Session{ID: "s1", Model: alpha})
	p.choose(beta)
	if p.current() != beta {
		t.Fatalf("pending choice not shown: %+v", p.current())
	}
	h.Flush()
	if a.Called("set-model s1 acme/beta") != 1 {
		t.Fatalf("calls %q", a.Calls())
	}
	c.Conv.Model = beta
	kernel.Emit(h, chat.Changed, c.Conv)
	if !p.pending.IsZero() || p.current() != beta {
		t.Fatalf("after confirmation: pending %+v current %+v", p.pending, p.current())
	}

	tt := ui.NewTester(p.view, 900, 600)
	tt.Frame()
	for _, click := range []string{"Default effort", "high"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v", click, err)
		}
		tt.Frame()
	}
	h.Flush()
	if a.Called("set-model s1 acme/beta high") != 1 {
		t.Fatalf("calls %q", a.Calls())
	}

	a.Fail["SetModel"] = errors.New("no such model")
	p.choose(alpha)
	h.Flush()
	if !p.pending.IsZero() || len(host.Toasts) != 1 {
		t.Fatalf("failed switch: pending %+v toasts %q", p.pending, host.Toasts)
	}
}

func TestPendingModelBelongsToItsConversation(t *testing.T) {
	h, p, _, c, _ := start(t, kerneltest.NewStore())
	c.Open(agent.Session{ID: "s1", Model: alpha})
	p.choose(beta)
	c.Open(agent.Session{ID: "s2", Model: alpha})
	if got := p.current(); got != alpha {
		t.Fatalf("s2 displays s1's pending model: %+v", got)
	}
	c.Open(agent.Session{ID: "s1", Model: alpha})
	if got := p.current(); got != alpha {
		t.Fatalf("returning to s1 restored an obsolete pending choice: %+v", got)
	}
	h.Flush()
}

func TestNavigatingDropsEarlierModelSwitchFailure(t *testing.T) {
	h, p, a, c, host := start(t, kerneltest.NewStore())
	c.Open(agent.Session{ID: "s1", Model: alpha})
	a.Fail["SetModel"] = errors.New("old switch failed")
	p.choose(beta)
	h.RunWork(0) // the failure waits for the UI while navigation happens
	c.Open(agent.Session{ID: "s2", Model: alpha})
	a.Fail["SetModel"] = nil
	h.RunPosts()
	if p.current() != alpha || len(host.Toasts) != 0 {
		t.Fatalf("old completion changed s2: model=%+v toasts=%v", p.current(), host.Toasts)
	}
	p.choose(beta)
	h.Flush()
	c.Conv.Model = beta
	kernel.Emit(h, chat.Changed, c.Conv)
	if !p.pending.IsZero() || p.current() != beta {
		t.Fatalf("s2 confirmation: pending=%+v current=%+v", p.pending, p.current())
	}
}

func TestModelsTurnedOffInSettingsLeaveThePicker(t *testing.T) {
	store := kerneltest.NewStore()
	h, p, _, _, _ := start(t, store)
	section := ui.NewTester(p.section, 900, 600)
	section.Frame()
	if !section.HasText("acme") || !section.HasText("2 of 2 enabled") || !section.HasText("Beta") {
		t.Fatalf("section %q", section.Texts())
	}
	p.show(false, beta)
	h.Flush()
	section.Frame()
	if !section.HasText("1 of 2 enabled") {
		t.Fatalf("section after hiding Beta %q", section.Texts())
	}
	picker := ui.NewTester(p.view, 900, 600)
	picker.Frame()
	if err := picker.Click("Alpha"); err != nil {
		t.Fatalf("open the picker: %v", err)
	}
	picker.Frame()
	if picker.HasText("Beta") {
		t.Fatalf("a hidden model is listed: %q", picker.Texts())
	}
	h.Close()

	// Remembered across launches; Enable all brings every model back.
	h, p, _, _, _ = start(t, store)
	if got := p.hidden.Get(); !slices.Equal(got, []string{"acme/beta"}) {
		t.Fatalf("hidden after restart %q", got)
	}
	section = ui.NewTester(p.section, 900, 600)
	section.Frame()
	if err := section.Click("Enable all"); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	h.Flush()
	section.Frame()
	if len(p.hidden.Get()) != 0 || !section.HasText("2 of 2 enabled") {
		t.Fatalf("hidden %q, section %q", p.hidden.Get(), section.Texts())
	}
}

func TestReasoningStaysBesideModelInPrimaryComposerGroup(t *testing.T) {
	h, p, _, _, _ := start(t, kerneltest.NewStore())
	ref := beta
	ref.Variant = "high"
	p.choose(ref)
	for _, width := range []int{240, 700} {
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).FillWidth().Children(func() {
				for _, entry := range kernel.Items(h, chat.ComposerPrimary) {
					kernel.Invoke(h, entry, func(v shell.View) { v.Render(c) })
				}
			})
		}, width, 400)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()
		model, hasModel := tt.Find("Beta")
		effort, hasEffort := tt.Find("high")
		if !hasModel || !hasEffort || effort.X < model.X+model.W || effort.Y != model.Y || effort.X+effort.W > float32(width) {
			t.Fatalf("model and reasoning must stay together at %d: model=%+v effort=%+v texts=%q", width, model, effort, tt.Texts())
		}
	}
}

func TestModelChecklistSelectionSurvivesCollapseAndSearch(t *testing.T) {
	h, p, _, _, _ := start(t, kerneltest.NewStore())
	tt := ui.NewTester(p.section, 600, 500)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if err := tt.Click("Beta"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !slices.Equal(p.hidden.Get(), []string{"acme/beta"}) {
		t.Fatalf("selection: %v", p.hidden.Get())
	}
	// A focused checkbox also persists keyboard edits.
	tt.Key(0, ui.KeySpace)
	h.Flush()
	tt.Frame()
	if len(p.hidden.Get()) != 0 {
		t.Fatalf("keyboard selection: %v", p.hidden.Get())
	}
	tt.Key(0, ui.KeySpace)
	h.Flush()
	tt.Frame()
	if err := tt.Click("acme"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Beta") {
		t.Fatal("collapsed provider still lists models")
	}
	// Space reopens the focused disclosure without changing selection.
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !tt.HasText("Beta") || !tt.HasText("1 of 2 enabled") {
		t.Fatalf("reopened: %q", tt.Texts())
	}
	if err := tt.Click("acme"); err != nil {
		t.Fatal(err)
	}
	p.query = "beta"
	tt.Frame()
	if !tt.HasText("Beta") || tt.HasText("Alpha") {
		t.Fatalf("search did not reveal the match: %q", tt.Texts())
	}
	if err := tt.Click("Enable all"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if len(p.hidden.Get()) != 0 {
		t.Fatalf("enable matching model: %v", p.hidden.Get())
	}
	if err := tt.Click("Disable all"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !slices.Equal(p.hidden.Get(), []string{"acme/beta"}) {
		t.Fatalf("bulk action changed an unmatched model: %v", p.hidden.Get())
	}
	p.query = ""
	tt.Frame()
	if tt.HasText("Beta") {
		t.Fatal("clearing search lost the collapsed state")
	}
}

// send runs the outgoing pipeline as the chat does, committing what it accepts.
func send(h *kerneltest.Harness, d chat.Draft) (chat.Draft, error) {
	out, commit, err := kernel.Run(h, chat.Outgoing, d)
	if err == nil {
		commit()
	}
	return out, err
}
