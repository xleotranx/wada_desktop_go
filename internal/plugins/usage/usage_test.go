package usage

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
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
	p := &Plugin{}
	h := kerneltest.New(t, a, chat, p)
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
	if p.sum.context != 44_000 || p.window() != 200_000 {
		t.Fatalf("context %d of %d", p.sum.context, p.window())
	}
	if _, ok := tt.Find("Context window"); !ok {
		t.Fatal("no context meter")
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
	if a.Called("messages") != reads+1 || p.sum.cost != 1.42 || p.sum.context != 100 {
		t.Fatalf("reads %d, usage %+v", a.Called("messages")-reads, p.sum)
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
	for v, want := range map[float64]string{0.001: "<$0.01", 0.42: "$0.42", 12.345: "$12.35"} {
		if got := dollars(v); got != want {
			t.Errorf("dollars(%v) = %q, want %q", v, got, want)
		}
	}
}
