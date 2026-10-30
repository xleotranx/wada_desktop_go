package pig

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

func TestExtensionsStartOncePerSession(t *testing.T) {
	h, p, faux := startPig(t)
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("First")}},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Second")}},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Other session")}},
	})
	var starts atomic.Int32
	finished := 0
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				finished++
			}
		}
	})
	var s agent.Session
	for i, want := range []int32{1, 1, 2} {
		if i != 1 {
			s = createChat(t, h, p, t.TempDir())
			e, _ := p.current()
			c, _ := e.get(s.ID)
			c.session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) {
				starts.Add(1)
				return nil, nil
			}}}}}, s.Directory))
		}
		call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
			return p.Send(ctx, agent.Send{Session: s.ID, Text: "Hello"})
		})
		waitFor(t, h, "turn completion", func() bool { return finished == i+1 })
		if got := starts.Load(); got != want {
			t.Fatalf("after prompt %d: session_start fired %d times, want %d", i+1, got, want)
		}
	}
}

func TestModelChangeExtensionDialogs(t *testing.T) {
	for _, event := range []string{"model_select", "thinking_level_select"} {
		t.Run(event, func(t *testing.T) {
			h, p, faux := startPig(t)
			s := createChat(t, h, p, t.TempDir())
			e, _ := p.current()
			c, _ := e.get(s.ID)
			next := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "next-test", Models: []ai.FauxModelDefinition{{ID: "reasoning-model", Reasoning: true}}})
			if err := e.models.RegisterNativeProvider(next.Provider()); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			var runner *inproc.Runner
			runner = inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{event: {func(args ...any) (any, error) {
				// Fail promptly instead of deadlocking the harness when a callback
				// would enter dialogUI.question with the metadata mutex held.
				if !c.mu.TryLock() {
					result <- errors.New("extension dialog called with the metadata mutex held")
					return nil, nil
				}
				c.mu.Unlock()
				confirmed, err := runner.GetUIContext().Confirm(args[1].(context.Context), "Change model?", event, extension.ExtensionUIDialogOptions{})
				if err == nil && !confirmed {
					err = errors.New("extension did not receive the confirmation")
				}
				result <- err
				return nil, nil
			}}}}}, s.Directory)
			c.session.ReplaceRunner(runner)
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Ready")}}})
			call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
				return p.Send(ctx, agent.Send{Session: s.ID, Text: "Initialize extensions"})
			})
			waitFor(t, h, "initial turn", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.busy })
			answered := 0
			kernel.On(h, agent.Events, func(batch []agent.Event) {
				for _, ev := range batch {
					if ev.Kind == agent.InteractionRequested && !ev.Interaction.Settled {
						if err := p.Answer(t.Context(), agent.InteractionAnswer{Session: s.ID, ID: ev.Interaction.ID, Confirmed: true}); err != nil {
							t.Error(err)
						}
						answered++
					}
				}
			})
			ref := agent.ModelRef{Provider: "next-test", ID: "reasoning-model", Variant: "high"}
			call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.SetModel(ctx, s.ID, ref) })
			var dialogErr error
			waitFor(t, h, "model change dialog", func() bool {
				select {
				case dialogErr = <-result:
					return true
				default:
					return false
				}
			})
			if dialogErr != nil {
				t.Fatal(dialogErr)
			}
			if answered != 1 {
				t.Fatalf("answered %d dialogs, want 1", answered)
			}
			page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
			if len(page.Sessions) != 1 || page.Sessions[0].Model != ref {
				t.Fatalf("model change was not saved: %+v", page)
			}
		})
	}
}
