package usage

import (
	"errors"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

var model = agent.ModelRef{Provider: "p", ID: "m"}

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Catalog = []agent.Model{{Ref: model, Name: "M", Context: 200_000}}
	a.Stored["s1"] = []agent.Message{
		{ID: "u1", Role: agent.RoleUser, Text: "hi"},
		{ID: "a1", Role: agent.RoleAssistant, Model: model, Cost: 0.25,
			Tokens: agent.Tokens{Input: 1000, Output: 200, CacheRead: 8000}},
		{ID: "a2", Role: agent.RoleAssistant, Model: model, Cost: 0.17,
			Tokens: agent.Tokens{Input: 2000, Output: 500, Reasoning: 500, CacheRead: 41_000}},
	}
	chat := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.New(t, a.New, chat.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	h.Flush()
	return h, p, a, chat
}

func TestOpenConversationShowsCostAndContext(t *testing.T) {
	h, p, _, chat := start(t)
	tt := ui.NewTester(p.view, 400, 60)
	tt.Frame()
	if len(tt.Texts()) != 0 {
		t.Fatalf("a new chat shows usage: %q", tt.Texts())
	}
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt.Frame()
	// The cost adds up; the context is the latest answer's: 44k of 200k.
	if !tt.HasText("$0.42") {
		t.Fatalf("cost: %q", tt.Texts())
	}
	if p.sum.last.Context() != 44_000 || p.model().Context != 200_000 || !tt.HasText("22%") {
		t.Fatalf("context %d of %d: %q", p.sum.last.Context(), p.model().Context, tt.Texts())
	}
	chat.NewChat("")
	h.Flush()
	tt.Frame()
	if tt.HasText("$0.42") {
		t.Fatal("usage outlived its conversation")
	}
}

func TestTurnEndReadsUsageAgain(t *testing.T) {
	h, p, a, chat := start(t)
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	reads := a.Called("messages")
	a.Stored["s1"] = append(a.Stored["s1"], agent.Message{ID: "a3", Role: agent.RoleAssistant, Model: model, Cost: 1,
		Tokens: agent.Tokens{Input: 100}})
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "other"}})
	h.Flush()
	if a.Called("messages") != reads {
		t.Fatal("another conversation's turn read this one")
	}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TextDelta, Session: "s1"}, {Kind: agent.TurnSucceeded, Session: "s1"}})
	h.Flush()
	if a.Called("messages") != reads+1 || p.sum.cost != 1.42 || p.sum.last.Context() != 100 || p.sum.replies != 3 {
		t.Fatalf("reads %d, usage %+v", a.Called("messages")-reads, p.sum)
	}
}

func TestPanelBreaksTheContextDown(t *testing.T) {
	h, p, _, chat := start(t)
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 420, 700)
	tt.Frame()
	if err := tt.Click("Context window"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The latest answer: 2k in, 41k cached, 500 out, 500 reasoning, of 200k.
	for _, want := range []string{"44k / 200k", "Cache read", "41k", "20.5%", "Free space", "156k", "78%",
		"Replies", "2", "Total cost", "$0.42", "Latest reply", "$0.17", "Tokens in this conversation", "49k"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestSummaryTotalsTheConversation(t *testing.T) {
	s := summarize([]agent.Message{
		{Role: agent.RoleUser},
		{Role: agent.RoleAssistant, Cost: 0.1, Tokens: agent.Tokens{Input: 10, Output: 2, CacheRead: 5}},
		{Role: agent.RoleAssistant, Cost: 0.2}, // reported nothing: the context stays the previous answer's
	})
	if s.replies != 2 || s.total != (agent.Tokens{Input: 10, Output: 2, CacheRead: 5}) || s.last.Context() != 17 || s.lastCost != 0.1 {
		t.Fatalf("summary %+v", s)
	}
}

func TestLateUsageOfAnotherConversationIsDropped(t *testing.T) {
	h, p, a, chat := start(t)
	a.Stored["s2"] = []agent.Message{{ID: "b1", Role: agent.RoleAssistant, Cost: 9}}
	chat.Open(agent.Session{ID: "s1"})
	chat.Open(agent.Session{ID: "s2"}) // before s1's read returns
	h.Flush()
	if p.session != "s2" || p.sum.cost != 9 {
		t.Fatalf("session %s, usage %+v", p.session, p.sum)
	}
}

func TestSpelling(t *testing.T) {
	for n, want := range map[int]string{0: "0", 812: "812", 1000: "1k", 4210: "4.2k", 9_999: "10k", 45_200: "45k",
		999_499: "999k", 999_600: "1M", 1_260_000: "1.3M"} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[[2]int]string{{0, 100}: "0%", {1, 10_000}: "<0.1%", {74, 1000}: "7.4%", {1, 2}: "50%", {5, 0}: ""} {
		if got := share(n[0], n[1]); got != want {
			t.Errorf("share(%v) = %q, want %q", n, got, want)
		}
	}
	if cost(0, true) != "Free" || cost(0, false) != "$0.00" {
		t.Error("zero cost")
	}
	for v, want := range map[float64]string{0.001: "<$0.01", 0.42: "$0.42", 12.345: "$12.35"} {
		if got := dollars(v); got != want {
			t.Errorf("dollars(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestWorkPanelTakesOverFromTheComposer(t *testing.T) {
	h, p, _, chat := start(t)
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	chat.Work = true
	meter := ui.NewTester(p.view, 400, 60)
	meter.Frame()
	if len(meter.Texts()) != 0 {
		t.Fatalf("the composer shows usage beside the work panel: %q", meter.Texts())
	}
	if !p.measured() {
		t.Fatal("the work sections have nothing to show")
	}
	panel := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(300).Children(func() {
			p.contextSection(c)
			p.costSection(c)
		})
	}, 300, 800)
	panel.Frame()
	for _, want := range []string{"44k / 200k", "Cache read", "Free space", "Total cost", "$0.42", "Tokens in this conversation"} {
		if !panel.HasText(want) {
			t.Fatalf("%q is missing from the work sections: %q", want, panel.Texts())
		}
	}
}

func TestUsageIsReadAgainOnceTheBackendConnects(t *testing.T) {
	h, p, a, chat := start(t)
	a.Fail["Messages"] = errors.New("the agent is not connected")
	chat.Open(agent.Session{ID: "s1"})
	h.Flush()
	if p.known {
		t.Fatal("usage known from a failed read")
	}
	delete(a.Fail, "Messages")
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	if !p.known || p.sum.cost == 0 {
		t.Fatal("usage was not read again after the backend connected")
	}
}
