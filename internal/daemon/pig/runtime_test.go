package pig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

func newTestRuntime(t *testing.T) (*Runtime, *ai.FauxProviderHandle) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	rt, err := New(t.Context(), Options{Root: filepath.Join(t.TempDir(), "pig")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	return rt, faux
}

func TestHeadlessRuntimeApprovalAndChanges(t *testing.T) {
	rt, faux := newTestRuntime(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := rt.Create(t.Context(), protocol.CreateSessionRequest{
		Directory: dir,
		Title:     "Edit main",
		Model:     protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}

	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "main.go", "content": "package main\n// headless\n"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Updated main.go.")}},
	})

	disp, err := rt.Send(t.Context(), protocol.SendRequest{
		ID:      "req-1",
		Session: s.ID,
		Text:    "Update main.go",
	})
	if err != nil || disp != protocol.SendAccepted {
		t.Fatalf("send = %q, err = %v", disp, err)
	}

	deadline := time.After(10 * time.Second)
	var succeeded bool
	for !succeeded {
		select {
		case ev := <-rt.Events():
			if ev.Session != s.ID {
				continue
			}
			switch ev.Kind {
			case protocol.EventApprovalRequested:
				if err := rt.Reply(t.Context(), protocol.ReplyRequest{
					Session:  s.ID,
					Approval: ev.Approval.ID,
					Decision: protocol.DecisionOnce,
				}); err != nil {
					t.Fatal(err)
				}
			case protocol.EventTurnSucceeded:
				succeeded = true
			case protocol.EventTurnFailed:
				t.Fatalf("turn failed: %s", ev.Error)
			case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded,
				protocol.EventToolStarted, protocol.EventToolCalled, protocol.EventToolFinished,
				protocol.EventInteractionRequested, protocol.EventTurnStarted, protocol.EventTurnInterrupted,
				protocol.EventModelChanged, protocol.EventSessionRenamed, protocol.EventSessionDeleted,
				protocol.EventModelsChanged, protocol.EventProviderAuth:
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn completion")
		}
	}

	changes, err := rt.Changes(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "main.go" || !strings.Contains(changes[0].Patch, "+// headless") {
		t.Fatalf("changes = %+v", changes)
	}

	// Ensure cancellation & clean Close.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_ = ctx
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
}
