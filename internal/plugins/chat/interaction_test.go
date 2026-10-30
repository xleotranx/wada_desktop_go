package chat

import (
	"context"
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
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

func TestGroupedQuestionsKeepSelectionsAndSettle(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	q := agent.Interaction{ID: "form", Title: "Choose", Questions: []agent.Question{
		{Key: "q0", Title: "Branch", Options: []agent.QuestionOption{{Value: "main-value", Label: "Main"}}},
		{Key: "q1", Title: "Checks", Multiple: true, Custom: true, Options: []agent.QuestionOption{{Value: "test", Label: "Tests"}}},
	}}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &q}})
	tt := ui.NewTester(p.view, 1000, 1000)
	tt.Frame()
	for _, label := range []string{"Main", "Tests"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	tt.Frame()
	if !tt.HasText("✓ Main") || !tt.HasText("✓ Tests") {
		t.Fatalf("texts=%q", tt.Texts())
	}
	if err := tt.Click("Answer to Checks"); err != nil {
		t.Fatal(err)
	}
	tt.Type("extra")
	tt.Frame()
	tt.Frame()
	state := p.t.rows[0].interaction
	if state.answers["q0"][0] != "main-value" || *state.custom["q1"] != "extra" {
		t.Fatalf("state=%+v", state)
	}
	if err := tt.Click("Submit answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	tt.Frame()
	if !slices.Contains(f.Calls(), "answer form answers=map[q0:[main-value] q1:[test extra]]") {
		t.Fatalf("calls=%q", f.Calls())
	}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &q}})
	q.Settled = true
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.InteractionRequested, Session: "s", Interaction: &q}})
	tt.Frame()
	if tt.HasText("Branch") {
		t.Fatal("settled form remains visible")
	}
}

type pendingAgent struct {
	agent.Service
	questions []agent.Interaction
	err       error
}

func (a pendingAgent) Pending(context.Context, agent.SessionID) ([]agent.Interaction, error) {
	return a.questions, a.err
}

func TestPendingFailureStillLoadsHistory(t *testing.T) {
	h, p, f, _ := start(t)
	f.Stored["s"] = []agent.Message{{Role: agent.RoleUser, Text: "Stored message"}}
	p.agent = pendingAgent{Service: f, err: errors.New("unsupported form field")}
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if !p.t.loaded || !tt.HasText("Stored message") || !tt.HasText("Questions did not load: unsupported form field") {
		t.Fatalf("loaded=%v texts=%q", p.t.loaded, tt.Texts())
	}
	p.agent = pendingAgent{Service: f}
	p.load()
	h.Flush()
	tt.Frame()
	if tt.HasText("Questions did not load: unsupported form field") {
		t.Fatal("form error survived a successful reload")
	}
}

func TestPendingQuestionsKeepAnswersAcrossTabSwitch(t *testing.T) {
	h, p, f, _ := start(t)
	q := agent.Interaction{ID: "form", Questions: []agent.Question{{Key: "q", Title: "Checks", Multiple: true, Custom: true,
		Options: []agent.QuestionOption{{Value: "test", Label: "Tests"}}}}}
	p.agent = pendingAgent{Service: f, questions: []agent.Interaction{q}}
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 1000)
	tt.Frame()
	if err := tt.Click("Tests"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Answer to Checks"); err != nil {
		t.Fatal(err)
	}
	tt.Type("extra")
	tt.Frame()
	p.NewChat("")
	tt.Frame()
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	tt.Frame()
	if !tt.HasText("✓ Tests") {
		t.Fatalf("selection lost: %q", tt.Texts())
	}
	if err := tt.Click("Submit answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "answer form answers=map[q:[test extra]]") {
		t.Fatalf("calls=%q", f.Calls())
	}
}

func TestOpenRestoresPendingQuestions(t *testing.T) {
	h, p, f, _ := start(t)
	p.agent = pendingAgent{Service: f, questions: []agent.Interaction{{ID: "pending", Title: "Unanswered", Kind: agent.Input}}}
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if !tt.HasText("Unanswered") {
		t.Fatalf("texts=%q", tt.Texts())
	}
}

func TestPendingRefreshRetainsQuestionsOnFailureAndRemovesSettled(t *testing.T) {
	h, p, f, _ := start(t)
	q := agent.Interaction{ID: "pending", Title: "Unanswered", Kind: agent.Input}
	p.agent = pendingAgent{Service: f, questions: []agent.Interaction{q}}
	p.Open(agent.Session{ID: "s"})
	h.Flush()
	p.agent = pendingAgent{Service: f, err: errors.New("disconnected")}
	p.load()
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if !tt.HasText("Unanswered") || !tt.HasText("Questions did not load: disconnected") {
		t.Fatalf("texts=%q", tt.Texts())
	}
	p.agent = pendingAgent{Service: f}
	p.load()
	h.Flush()
	tt.Frame()
	if tt.HasText("Unanswered") || tt.HasText("Questions did not load: disconnected") {
		t.Fatalf("settled request or error remains: %q", tt.Texts())
	}
}
