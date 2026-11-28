package pig

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

// waitFor reads events until one of kind arrives for the session.
func waitFor(t *testing.T, rt *Runtime, session string, kind protocol.EventKind) protocol.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Session == session && ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func TestMessagesReachARunningTurn(t *testing.T) {
	rt, faux := newTestRuntime(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := rt.Create(t.Context(), protocol.CreateSessionRequest{Directory: dir,
		Model: protocol.ModelRef{Provider: "wada-test", ID: "test-model"}})
	if err != nil {
		t.Fatal(err)
	}
	write := ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write",
		map[string]any{"path": "main.go", "content": "package main\n// x\n"}, nil)}, StopReason: "toolUse"}
	faux.SetResponses([]ai.FauxResponseStep{write, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done.")}}})
	if _, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "Edit main.go"}); err != nil {
		t.Fatal(err)
	}
	approval := waitFor(t, rt, s.ID, protocol.EventApprovalRequested)

	// While the turn waits, a plain send is refused; a steer joins the turn.
	if _, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "plain"}); !errors.Is(err, ErrNotSent) {
		t.Fatalf("plain send during a turn = %v", err)
	}
	if _, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "with a file", Deliver: protocol.DeliverSteer,
		Files: []protocol.File{{Name: "a.txt", Data: []byte("a")}}}); !errors.Is(err, ErrNotSent) {
		t.Fatalf("steer with a file = %v", err)
	}
	d, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "Also keep it short", Deliver: protocol.DeliverSteer})
	if err != nil || d != protocol.SendQueued {
		t.Fatalf("steer = %q %v", d, err)
	}
	if err := rt.Reply(t.Context(), protocol.ReplyRequest{Session: s.ID, Approval: approval.Approval.ID, Decision: protocol.DecisionOnce}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, rt, s.ID, protocol.EventTurnSucceeded)
	msgs, err := rt.Messages(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(msgs, func(m protocol.Message) bool { return m.Role == protocol.RoleUser && m.Text == "Also keep it short" }) {
		t.Fatalf("the steered message isn't in the conversation: %+v", msgs)
	}

	// An interrupt stops a waiting turn and starts its own.
	faux.SetResponses([]ai.FauxResponseStep{write, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Stopped instead.")}}})
	if _, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "Edit it again"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, rt, s.ID, protocol.EventApprovalRequested)
	d, err = rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Text: "Never mind, stop", Deliver: protocol.DeliverInterrupt})
	if err != nil || d != protocol.SendAccepted {
		t.Fatalf("interrupt = %q %v", d, err)
	}
	waitFor(t, rt, s.ID, protocol.EventTurnSucceeded)
}
