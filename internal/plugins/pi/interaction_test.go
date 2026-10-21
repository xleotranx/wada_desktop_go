package pi

import (
	"context"
	"encoding/json"
	"testing"

	"wada-desktop-go/internal/contract/agent"
)

func TestAnswersPreserveSelectionsAndText(t *testing.T) {
	for _, method := range []string{"select", "input", "editor"} {
		t.Run(method, func(t *testing.T) {
			h, p, _ := startPi(t)
			got := events(h)
			call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
				return p.Send(ctx, agent.Send{Session: "old-1", Text: method + " please"})
			})
			waitFor(t, h, "question", func() bool {
				for _, e := range *got {
					if e.Kind == agent.InteractionRequested {
						return true
					}
				}
				return false
			})
			answer := agent.InteractionAnswer{Session: "old-1", ID: "dlg1", Value: "second"}
			if method == "select" {
				invalid := answer
				invalid.Value = "not offered"
				if p.Answer(context.Background(), invalid) == nil {
					t.Fatal("accepted an unoffered option")
				}
			}
			wrong := answer
			wrong.Session = "another-session"
			if p.Answer(context.Background(), wrong) == nil {
				t.Fatal("answered another session's question")
			}
			call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Answer(ctx, answer) })
			if p.Answer(context.Background(), answer) == nil {
				t.Fatal("answered twice")
			}
			waitFor(t, h, "settled", func() bool {
				for _, e := range *got {
					if e.Kind == agent.TurnSucceeded {
						return true
					}
				}
				return false
			})
			for _, e := range *got {
				if e.Kind == agent.TextEnded && e.Text.Text != "value=second cancelled=<nil>" {
					t.Fatalf("answer = %q", e.Text.Text)
				}
			}
		})
	}
}

func TestDialogCancellationIsDistinctFromEmptyText(t *testing.T) {
	empty := dialogReply("q", "input", agent.InteractionAnswer{})
	cancel := dialogReply("q", "input", agent.InteractionAnswer{Cancelled: true})
	if empty["value"] != "" || empty["cancelled"] != nil || cancel["cancelled"] != true || cancel["value"] != nil {
		t.Fatalf("empty=%v cancel=%v", empty, cancel)
	}
}

func TestDialogsPreserveTheirInteractionKind(t *testing.T) {
	for method, want := range map[string]agent.InteractionKind{
		"confirm": agent.Confirm, "select": agent.Select, "input": agent.Input, "editor": agent.Editor,
	} {
		raw, _ := json.Marshal(map[string]any{"id": "question", "method": method,
			"title": "Choose", "options": []string{"first", "second"}, "placeholder": "Your name", "prefill": "Initial text"})
		tr := translator{session: "s"}
		events := tr.translate("extension_ui_request", raw)
		if len(events) != 1 || events[0].Kind != agent.InteractionRequested || events[0].Interaction.Kind != want {
			t.Fatalf("%s: events %+v", method, events)
		}
		i := events[0].Interaction
		if i.ID != "question" || i.Placeholder != "Your name" || i.Prefill != "Initial text" || i.Options[1] != "second" {
			t.Fatalf("%s: interaction %+v", method, i)
		}
	}
}
