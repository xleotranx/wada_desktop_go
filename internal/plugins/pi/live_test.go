package pi

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
)

// TestLivePiAnswers sends one short prompt through the real pi CLI. It runs a
// model turn, so it only runs with WADA_LIVE_PI=1.
func TestLivePiAnswers(t *testing.T) {
	if os.Getenv("WADA_LIVE_PI") != "1" {
		t.Skip("set WADA_LIVE_PI=1 to run against the installed pi")
	}
	p := &Plugin{}
	h := kerneltest.Start(t, kerneltest.Config{}, p)
	if err := h.Enable("pi"); err != nil {
		t.Fatal(err)
	}
	var got []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, e := range batch {
			t.Logf("event %v session=%s text=%+v tool=%+v", e.Kind, e.Session, e.Text, e.Tool)
		}
		got = append(got, batch...)
	})
	deadline := time.Now().Add(60 * time.Second)
	wait := func(what string, cond func() bool) {
		t.Helper()
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; status %+v", what, p.Status())
			}
			h.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("ready", func() bool { return p.Status().State == agent.Ready })
	run := func(fn func(context.Context) error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- fn(context.Background()) }()
		for {
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
				return
			case <-time.After(10 * time.Millisecond):
				h.Flush()
			}
		}
	}
	var s agent.Session
	run(func(ctx context.Context) (err error) {
		var model agent.ModelRef // WADA_LIVE_PI_MODEL=provider/id picks one
		if provider, id, ok := strings.Cut(os.Getenv("WADA_LIVE_PI_MODEL"), "/"); ok {
			model = agent.ModelRef{Provider: provider, ID: id}
		}
		s, err = p.Create(ctx, agent.CreateSession{Directory: t.TempDir(), Model: model})
		return err
	})
	t.Logf("created %+v", s)
	run(func(ctx context.Context) error {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Reply with exactly: pong"})
	})
	wait("the turn to end", func() bool {
		return slices.ContainsFunc(got, func(e agent.Event) bool {
			return e.Kind == agent.TurnSucceeded || e.Kind == agent.TurnFailed || e.Kind == agent.TurnInterrupted
		})
	})
	for _, e := range got {
		if e.Kind == agent.TurnFailed {
			t.Fatalf("pi failed the turn: %s", e.Error)
		}
	}
	if !slices.ContainsFunc(got, func(e agent.Event) bool { return e.Kind == agent.TextEnded && e.Session == s.ID }) {
		t.Fatalf("no answer text for %s", s.ID)
	}
}
