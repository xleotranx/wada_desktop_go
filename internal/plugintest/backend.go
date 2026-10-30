package plugintest

import (
	"context"
	"errors"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

// BackendCase adapts a protocol fixture to shared behavioral contract checks.
// RejectSend configures a deterministic send rejection and returns an attempt
// counter. The backend is the harness's provider of agent.ServiceKey.
type BackendCase struct {
	Harness    *kerneltest.Harness
	PluginID   string
	Session    agent.SessionID
	RejectSend func() (text string, attempts func() int)
}

// CheckBackend checks mutation failure, withdrawal, reactivation, and connection
// generations through the same public contract for every backend adapter.
func CheckBackend(t *testing.T, b BackendCase) {
	t.Helper()
	service := waitReady(t, b.Harness)
	before := service.Status().Attempt
	text, attempts := b.RejectSend()
	result := make(chan error, 1)
	go func() {
		_, err := service.Send(context.Background(), agent.Send{Session: b.Session, Text: text})
		result <- err
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	waiting := true
	for waiting {
		select {
		case err := <-result:
			if err == nil || attempts() != 1 {
				t.Fatalf("send rejection: err=%v attempts=%d; want one failed attempt", err, attempts())
			}
			waiting = false
		case <-deadline.C:
			t.Fatal("send did not return")
		case <-time.After(time.Millisecond):
			b.Harness.Flush()
		}
	}
	if err := b.Harness.Disable(b.PluginID); err != nil {
		t.Fatal(err)
	}
	// A consumer that kept the service past its provider's end hears that
	// nothing is connected.
	ctx := context.Background()
	checks := map[string]func() error{
		"sessions": func() error { _, err := service.Sessions(ctx, agent.SessionQuery{}); return err },
		"messages": func() error { _, err := service.Messages(ctx, b.Session); return err },
		"create":   func() error { _, err := service.Create(ctx, agent.CreateSession{}); return err },
		"send":     func() error { _, err := service.Send(ctx, agent.Send{Session: b.Session}); return err },
		"abort":    func() error { return service.Abort(ctx, b.Session) },
		"reply":    func() error { return service.Reply(ctx, agent.Reply{Session: b.Session}) },
		"rename":   func() error { return service.Rename(ctx, b.Session, "new") },
		"delete":   func() error { return service.Delete(ctx, b.Session) },
	}
	if children, ok := service.(agent.Children); ok {
		checks["spawn"] = func() error { _, err := children.Spawn(ctx, agent.Spawn{Parent: b.Session, Name: "Coder"}); return err }
	}
	if models, ok := service.(agent.Models); ok {
		checks["models"] = func() error { _, err := models.Models(ctx); return err }
		checks["default model"] = func() error { _, err := models.DefaultModel(ctx); return err }
		checks["set model"] = func() error { return models.SetModel(ctx, b.Session, agent.ModelRef{Provider: "p", ID: "m"}) }
	}
	if modes, ok := service.(agent.Modes); ok {
		checks["modes"] = func() error { _, err := modes.Modes(ctx); return err }
	}
	if files, ok := service.(agent.Files); ok {
		checks["find files"] = func() error { _, err := files.Find(ctx, "", "a", 5); return err }
		checks["read file"] = func() error { _, err := files.Read(ctx, "", "a"); return err }
	}
	if skills, ok := service.(agent.Skills); ok {
		checks["skills"] = func() error { _, err := skills.Skills(ctx, ""); return err }
	}
	if interactions, ok := service.(agent.Interactions); ok {
		checks["answer"] = func() error { return interactions.Answer(ctx, agent.InteractionAnswer{Session: b.Session}) }
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, agent.ErrNotConnected) {
			t.Errorf("%s after disable: %v", name, err)
		}
	}
	if err := b.Harness.Enable(b.PluginID); err != nil {
		t.Fatal(err)
	}
	if got := waitReady(t, b.Harness).Status().Attempt; got <= before {
		t.Errorf("connection generation did not advance: %d -> %d", before, got)
	}
}

// waitReady runs the harness's work and posts until its backend is Ready, and
// returns that backend.
func waitReady(t *testing.T, h *kerneltest.Harness) agent.Service {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		h.Flush()
		if s, ok := kernel.Use(h, agent.ServiceKey); ok && s.Status().State == agent.Ready {
			return s
		}
	}
	t.Fatal("the backend did not become ready")
	return nil
}
