package models

import (
	"errors"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

var (
	alpha = agent.ModelRef{Provider: "acme", ID: "alpha"}
	beta  = agent.ModelRef{Provider: "acme", ID: "beta"}
)

func start(t *testing.T, store *kerneltest.Store) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat, *plugintest.Host) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Catalog = []agent.Model{
		{Ref: alpha, Name: "Alpha", Free: true},
		{Ref: beta, Name: "Beta", Variants: []string{"low", "high"}},
	}
	a.Default = alpha
	a.ModeList = []agent.Mode{{ID: "build", Name: "Build"}, {ID: "plan", Name: "Plan"}}
	c, host, p := &plugintest.Chat{}, &plugintest.Host{}, &Plugin{}
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, a, c, host, p)
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
	if d := outgoing(t, h, chat.Draft{Text: "hi"}); !d.Model.IsZero() || d.Mode != "" {
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
	if d := outgoing(t, h, chat.Draft{Session: "s1", Text: "hi"}); !d.Model.IsZero() || d.Mode != "" {
		t.Fatalf("an existing conversation was changed: %+v", d)
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
	if tt.HasText("Build") {
		t.Fatal("the mode picker shows for an existing conversation")
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
