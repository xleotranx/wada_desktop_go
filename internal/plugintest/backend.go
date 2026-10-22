package plugintest

import (
	"context"
	"errors"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

// BackendCase adapts a protocol fixture to shared behavioral contract checks.
// RejectSend configures a deterministic send rejection and returns an attempt
// counter. WaitReady pumps the fixture until its current connection is Ready.
type BackendCase struct {
	Harness    *kerneltest.Harness
	Service    agent.Service
	PluginID   string
	Session    agent.SessionID
	RejectSend func() (text string, attempts func() int)
	WaitReady  func()
}

// CheckBackend checks mutation failure, withdrawal, reactivation, and connection
// generations through the same public contract for every backend adapter.
func CheckBackend(t *testing.T, b BackendCase) {
	t.Helper()
	b.WaitReady()
	before := b.Service.Status().Attempt
	text, attempts := b.RejectSend()
	result := make(chan error, 1)
	go func() {
		_, err := b.Service.Send(context.Background(), agent.Send{Session: b.Session, Text: text})
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
	ctx := context.Background()
	checks := map[string]func() error{
		"sessions": func() error { _, err := b.Service.Sessions(ctx, agent.SessionQuery{}); return err },
		"messages": func() error { _, err := b.Service.Messages(ctx, b.Session); return err },
		"create":   func() error { _, err := b.Service.Create(ctx, agent.CreateSession{}); return err },
		"send":     func() error { _, err := b.Service.Send(ctx, agent.Send{Session: b.Session}); return err },
		"abort":    func() error { return b.Service.Abort(ctx, b.Session) },
		"reply":    func() error { return b.Service.Reply(ctx, agent.Reply{Session: b.Session}) },
		"rename":   func() error { return b.Service.Rename(ctx, b.Session, "new") },
		"delete":   func() error { return b.Service.Delete(ctx, b.Session) },
	}
	if models, ok := b.Service.(agent.Models); ok {
		checks["models"] = func() error { _, err := models.Models(ctx); return err }
		checks["default model"] = func() error { _, err := models.DefaultModel(ctx); return err }
		checks["set model"] = func() error { return models.SetModel(ctx, b.Session, agent.ModelRef{Provider: "p", ID: "m"}) }
	}
	if modes, ok := b.Service.(agent.Modes); ok {
		checks["modes"] = func() error { _, err := modes.Modes(ctx); return err }
	}
	if files, ok := b.Service.(agent.Files); ok {
		checks["find files"] = func() error { _, err := files.Find(ctx, "", "a", 5); return err }
		checks["read file"] = func() error { _, err := files.Read(ctx, "", "a"); return err }
	}
	if interactions, ok := b.Service.(agent.Interactions); ok {
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
	b.WaitReady()
	if got := b.Service.Status().Attempt; got <= before {
		t.Errorf("connection generation did not advance: %d -> %d", before, got)
	}
}
