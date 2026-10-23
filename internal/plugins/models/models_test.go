package models

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
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
	a.ModeList = []agent.Mode{{ID: "build", Name: "Build"}, {ID: "plan", Name: "Plan"}}
	c, host := &plugintest.Chat{}, &kittest.Host{}
	var p *Plugin
	h := kerneltest.Start(t, cfg, append([]kernel.Factory{a.New, c.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })}, extra...)...)
	h.Flush()
	return h, p, a, c, host
}

func outgoing(t *testing.T, h *kerneltest.Harness, d chat.Draft) chat.Draft {
	t.Helper()
	out, err := kernel.Run(h, chat.Outgoing, d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNewChatUsesTheChosenModelAndMode(t *testing.T) {
	store := kerneltest.NewStore()
	h, p, _, _, _ := start(t, store)
	tt := ui.NewTester(p.view, 900, 600)
	tt.Frame()
	if !tt.HasText("Alpha") || !tt.HasText("Build") || tt.HasText("Default effort") {
		t.Fatalf("pickers %q", tt.Texts())
	}
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); !d.Model.IsZero() || d.Mode != "build" {
		t.Fatalf("nothing chosen yet, draft %+v", d)
	}
	for _, click := range []string{"Alpha", "Beta", "Build", "Plan"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v in %q", click, err, tt.Texts())
		}
		tt.Frame()
	}
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); d.Model != beta || d.Mode != "plan" {
		t.Fatalf("draft %+v", d)
	}
	if d := outgoing(t, h, chat.Draft{Session: "s1", Text: "hi"}); !d.Model.IsZero() || d.Mode != "plan" {
		t.Fatalf("existing conversation did not receive the mode: %+v", d)
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
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); !d.Model.IsZero() || d.Mode != "plan" {
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
	if !tt.HasText("Build") {
		t.Fatal("the mode picker is missing from an existing conversation")
	}
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

// targets contributes a chat target that keeps what it is given.
type targets struct{ taken *[]chat.Draft }

func (f targets) New() kernel.Plugin      { return f }
func (targets) Manifest() kernel.Manifest { return kernel.Manifest{ID: "test-targets"} }

func (t targets) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, chat.Targets, "test-targets.flow", 0, chat.Target{
		Group: "Workflows", Title: "Plan, code, review", Summary: "Three agents, one after the other.",
		Take: func(d chat.Draft) { *t.taken = append(*t.taken, d) }})
}

func TestChoosingATargetSendsTheNextMessageThereOnly(t *testing.T) {
	var taken []chat.Draft
	h, p, _, _, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if err := tt.Click("Build"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Modes", "Plan", "Workflows", "Plan, code, review", "Three agents, one after the other."} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from the picker: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Plan, code, review"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Build") || !tt.HasText("Plan, code, review") {
		t.Fatalf("the picker does not show the target: %q", tt.Texts())
	}

	_, err := kernel.Run(h, chat.Outgoing, chat.Draft{Directory: "/w", Text: "fix the parser"})
	if !errors.Is(err, chat.ErrTaken) {
		t.Fatalf("error %v", err)
	}
	if len(taken) != 1 || taken[0].Text != "fix the parser" || taken[0].Directory != "/w" || taken[0].Session != "" {
		t.Fatalf("taken %+v", taken)
	}
	// It was for that message only.
	d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "next"})
	if err != nil || d.Text != "next" || len(taken) != 1 {
		t.Fatalf("draft %+v, error %v, taken %d", d, err, len(taken))
	}
	tt.Frame()
	if !tt.HasText("Build") {
		t.Fatalf("the picker did not go back to the mode: %q", tt.Texts())
	}
}

func TestExistingChatCanChooseModeAndWorkflow(t *testing.T) {
	var taken []chat.Draft
	h, p, _, c, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	c.Open(agent.Session{ID: "s1", Directory: "/work"})
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	for _, click := range []string{"Build", "Plan"} {
		if err := tt.Click(click); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	d := outgoing(t, h, chat.Draft{Session: "s1", Directory: "/work", Text: "think first"})
	if d.Mode != "plan" || d.Session != "s1" {
		t.Fatalf("mode was not applied to the existing chat: %+v", d)
	}
	for _, click := range []string{"Plan", "Plan, code, review"} {
		if err := tt.Click(click); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	_, err := kernel.Run(h, chat.Outgoing, chat.Draft{Session: "s1", Directory: "/work", Text: "fix parser"})
	if !errors.Is(err, chat.ErrTaken) || len(taken) != 1 || taken[0].Directory != "/work" || taken[0].Text != "fix parser" {
		t.Fatalf("workflow did not take the brief: taken=%+v err=%v", taken, err)
	}
	if c.Current().Session != "s1" {
		t.Fatal("workflow replaced the existing chat")
	}
	tt.Frame()
	if !tt.HasText("Plan") {
		t.Fatalf("picker did not return to Plan: %q", tt.Texts())
	}
	if d := outgoing(t, h, chat.Draft{Session: "s1", Text: "continue"}); d.Mode != "plan" || len(taken) != 1 {
		t.Fatalf("next message did not return to the chat: %+v", d)
	}
}

func TestChoosingAModeAfterATargetLeavesTheTarget(t *testing.T) {
	var taken []chat.Draft
	h, p, _, _, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	for _, click := range []string{"Build", "Plan, code, review", "Plan, code, review", "Plan"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v in %q", click, err, tt.Texts())
		}
		tt.Frame()
	}
	d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "hi"})
	if err != nil || d.Mode != "plan" || len(taken) != 0 {
		t.Fatalf("draft %+v, error %v, taken %d", d, err, len(taken))
	}
}

func TestATargetIsDroppedWhenAnotherConversationOpens(t *testing.T) {
	var taken []chat.Draft
	h, p, _, c, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	for _, click := range []string{"Build", "Plan, code, review"} {
		if err := tt.Click(click); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	c.Open(agent.Session{ID: "s1"})
	if d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Session: "s1", Text: "hi"}); err != nil || d.Text != "hi" {
		t.Fatalf("a stored conversation: %+v %v", d, err)
	}
	c.NewChat("/w")
	if d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "hi"}); err != nil || d.Text != "hi" || len(taken) != 0 {
		t.Fatalf("a later new chat went to the target: %+v %v, taken %d", d, err, len(taken))
	}
}

// chooseTarget picks the test target in the mode picker.
func chooseTarget(t *testing.T, p *Plugin) {
	t.Helper()
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	for _, click := range []string{"Build", "Plan, code, review"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v in %q", click, err, tt.Texts())
		}
		tt.Frame()
	}
}

func TestATargetIsDroppedWhenANewChatOpensInAnotherFolder(t *testing.T) {
	var taken []chat.Draft
	h, p, _, c, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	c.NewChat("/a")
	chooseTarget(t, p)
	c.NewChat("/b")
	if d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Directory: "/b", Text: "hi"}); err != nil || d.Text != "hi" || len(taken) != 0 {
		t.Fatalf("the new chat went to the target: %+v %v, taken %d", d, err, len(taken))
	}
}

func TestAGoneTargetKeepsTheMessageFromTheAgent(t *testing.T) {
	var taken []chat.Draft
	h, p, _, _, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	chooseTarget(t, p)
	if err := h.Disable("test-targets"); err != nil {
		t.Fatal(err)
	}
	_, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "hi"})
	if err == nil || errors.Is(err, chat.ErrTaken) {
		t.Fatalf("error %v", err)
	}
	if d := outgoing(t, h, chat.Draft{Text: "again"}); d.Text != "again" {
		t.Fatalf("the next message: %+v", d)
	}
}

// panicking contributes a target whose Take fails.
type panicking struct{}

func (f panicking) New() kernel.Plugin      { return f }
func (panicking) Manifest() kernel.Manifest { return kernel.Manifest{ID: "test-targets"} }

func (panicking) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, chat.Targets, "test-targets.flow", 0, chat.Target{
		Title: "Plan, code, review", Take: func(chat.Draft) { panic("broken") }})
}

func TestATargetThatFailsDoesNotTakeTheMessage(t *testing.T) {
	h, p, _, _, _ := startWith(t, kerneltest.Config{Lenient: true}, panicking{}.New)
	chooseTarget(t, p)
	_, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "hi"})
	if err == nil || errors.Is(err, chat.ErrTaken) {
		t.Fatalf("error %v", err)
	}
}

func TestTheModePickerAppearsForATargetWithoutBackendModes(t *testing.T) {
	var taken []chat.Draft
	h, p, a, _, _ := start(t, kerneltest.NewStore(), targets{&taken}.New)
	p.mode.Set("plan") // a choice remembered from a backend with modes
	a.ModeList = nil
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	tt := ui.NewTester(p.view, 900, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if !tt.HasText("Chat") {
		t.Fatalf("the picker is missing: %q", tt.Texts())
	}
	if d := outgoing(t, h, chat.Draft{Session: "s1", Text: "hi"}); d.Mode != "" {
		t.Fatalf("a mode was sent to a backend without modes: %+v", d)
	}
	for _, click := range []string{"Chat", "Plan, code, review"} {
		if err := tt.Click(click); err != nil {
			t.Fatalf("click %q: %v in %q", click, err, tt.Texts())
		}
		tt.Frame()
	}
	if _, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "hi"}); !errors.Is(err, chat.ErrTaken) {
		t.Fatalf("error %v", err)
	}
	if !slices.Equal([]string{taken[0].Text}, []string{"hi"}) {
		t.Fatalf("taken %+v", taken)
	}
}

func TestWithoutTargetsOneModeShowsNoPicker(t *testing.T) {
	h, p, a, _, _ := start(t, kerneltest.NewStore())
	a.ModeList = a.ModeList[:1]
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	tt := ui.NewTester(p.view, 900, 600)
	tt.Frame()
	if tt.HasText("Build") {
		t.Fatalf("a picker for one mode: %q", tt.Texts())
	}
}
