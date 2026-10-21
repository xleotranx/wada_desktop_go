package chat

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

func TestInteractionSelectionAndTextAnswers(t *testing.T) {
	for _, question := range []agent.Interaction{
		{ID: "select", Kind: agent.Select, Title: "Choose a branch", Options: []string{"main", "feature"}},
		{ID: "input", Kind: agent.Input, Title: "Your answer", Placeholder: "Name"},
		{ID: "editor", Kind: agent.Editor, Title: "Edit the plan", Prefill: "Initial plan"},
		{ID: "confirm", Kind: agent.Confirm, Title: "Continue?"},
	} {
		t.Run(string(question.ID), func(t *testing.T) {
			h, p, f, _ := start(t)
			p.Open(agent.Session{ID: "s"})
			h.Flush()
			kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &question}})
			tt := ui.NewTester(p.view, 1000, 800)
			tt.Frame()
			button, want := "Submit answer", "answer "+string(question.ID)+" value= confirmed=false cancelled=false"
			switch question.Kind {
			case agent.Select:
				button, want = "feature", "answer select value=feature confirmed=false cancelled=false"
			case agent.Confirm:
				button, want = "Yes", "answer confirm value= confirmed=true cancelled=false"
			case agent.Input:
				// An empty submitted value is an answer, not a cancellation.
			case agent.Editor:
				want = "answer editor value=Initial plan confirmed=false cancelled=false"
			}
			if tt.HasText("Always") {
				t.Fatalf("interaction controls missing: %q", tt.Texts())
			}
			if err := tt.Click(button); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			h.Flush()
			tt.Frame()
			if !slices.Contains(f.Calls(), want) || len(p.t.rows) != 0 {
				t.Fatalf("answer calls=%q rows=%v", f.Calls(), kinds(p))
			}
		})
	}
}

func TestInteractionFailureRetainsTheQuestionAndSettlementRemovesIt(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	f.Fail["Answer"] = errors.New("disconnected")
	q := agent.Interaction{ID: "q", Kind: agent.Input, Title: "Question"}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &q}})
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if err := tt.Click("Cancel question"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	tt.Frame()
	if !tt.HasText("Question") || f.Called("answer q") != 1 {
		t.Fatal("failed answer was retried or question disappeared")
	}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnInterrupted, Session: "s"}})
	tt.Frame()
	if tt.HasText("Question") {
		t.Fatal("settled question still visible")
	}
}

func TestInteractionInputKeepsEditsAcrossFrames(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	q := agent.Interaction{ID: "q", Kind: agent.Input, Title: "Name"}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &q}})
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if err := tt.Click("Answer to Name"); err != nil {
		t.Fatal(err)
	}
	tt.Type("chosen text")
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Submit answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "answer q value=chosen text confirmed=false cancelled=false") {
		t.Fatalf("calls %q", f.Calls())
	}
}
