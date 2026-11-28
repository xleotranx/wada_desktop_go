package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

// scriptedSession is a harness session whose Send returns sendErr, and which
// otherwise leaves the turn's events to the test.
type scriptedSession struct {
	events  chan protocol.Event
	sendErr error
}

func (s *scriptedSession) Send(context.Context, string) error { return s.sendErr }
func (*scriptedSession) Abort(context.Context) error          { return nil }
func (*scriptedSession) Reply(context.Context, string, protocol.Decision) error {
	return nil
}
func (s *scriptedSession) Events() <-chan protocol.Event { return s.events }
func (s *scriptedSession) Close() error {
	close(s.events)
	return nil
}

type scriptedProvider struct{ sess *scriptedSession }

func (*scriptedProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{ID: "scripted", Name: "Scripted"}
}

func (p *scriptedProvider) Start(context.Context, harness.SessionOptions) (harness.Session, error) {
	return p.sess, nil
}

func TestWorkerSendStatusAndOutcome(t *testing.T) {
	dataDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	sess := &scriptedSession{events: make(chan protocol.Event, 16)}
	reg := harness.NewRegistry()
	reg.Register(&scriptedProvider{sess: sess})
	o, err := New(t.Context(), Options{DataDir: dataDir, PiG: rt, Worktrees: worktree.New(dataDir), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	go func() {
		for range o.Events() {
		}
	}()

	info, err := o.spawnWorker(t.Context(), spawnWorkerArgs{ParentSession: "parent", Provider: "scripted", Directory: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	w := o.getWorker(info.ID)
	waitStatus := func(want protocol.WorkerStatus) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for w.snapshot().Status != want {
			if time.Now().After(deadline) {
				t.Fatalf("status = %s, want %s", w.snapshot().Status, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	send := func(text string) error {
		_, err := o.Send(t.Context(), protocol.SendRequest{Session: info.ID, Text: text})
		return err
	}
	userTexts := func() []string {
		msgs, _ := o.Messages(t.Context(), info.ID)
		var out []string
		for _, m := range msgs {
			if m.Role == protocol.RoleUser {
				out = append(out, m.Text)
			}
		}
		return out
	}

	if err := send("first"); err != nil {
		t.Fatal(err)
	}
	sess.events <- protocol.Event{Kind: protocol.EventTurnStarted}
	sess.events <- protocol.Event{Kind: protocol.EventTurnSucceeded}
	waitStatus(protocol.WorkerCompleted)

	// A wait right after the next send must not see the first turn's result.
	if err := send("second"); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	got, err := o.waitOneWorker(wctx, "parent", info.ID)
	cancel()
	if err == nil {
		t.Fatalf("wait after send returned %s before the turn started", got.Status)
	}
	sess.events <- protocol.Event{Kind: protocol.EventTurnStarted}
	sess.events <- protocol.Event{Kind: protocol.EventTurnSucceeded}
	waitStatus(protocol.WorkerCompleted)

	// What a CLI harness cannot take is refused before anything is recorded.
	for _, req := range []protocol.SendRequest{
		{Files: []protocol.File{{Name: "a.png"}}},
		{Skills: []protocol.SkillRef{{ID: "review"}}},
		{Instructions: "plan first"},
		{Policy: &protocol.ToolPolicy{}},
		{ResultSchema: []byte(`{"type":"object"}`)},
	} {
		req.Session, req.Text = info.ID, "unsupported"
		if _, err := o.Send(t.Context(), req); !errors.Is(err, pig.ErrNotSent) {
			t.Fatalf("send %+v = %v, want ErrNotSent", req, err)
		}
	}
	if st := w.snapshot().Status; st != protocol.WorkerCompleted {
		t.Fatalf("status after unsupported sends = %s, want completed", st)
	}
	if err := o.SetModel(t.Context(), info.ID, protocol.ModelRef{Provider: "openai", ID: "gpt"}); err == nil {
		t.Fatal("SetModel on a harness session succeeded")
	}
	if _, err := o.Spawn(t.Context(), protocol.SpawnRequest{Parent: info.ID, Name: "reviewer"}); err == nil {
		t.Fatal("Spawn under a harness session succeeded")
	}

	// Refused before reaching the harness: safe to retry, nothing recorded.
	sess.sendErr = fmt.Errorf("%w: closed", harness.ErrNotSent)
	if err := send("refused"); !errors.Is(err, pig.ErrNotSent) {
		t.Fatalf("refused send = %v, want ErrNotSent", err)
	}
	if st := w.snapshot().Status; st != protocol.WorkerCompleted {
		t.Fatalf("status after refused send = %s, want completed", st)
	}

	// Failed mid-write: the outcome is unknown, so it is not reported as unsent.
	sess.sendErr = errors.New("broken pipe")
	if err := send("unknown"); err == nil || errors.Is(err, pig.ErrNotSent) {
		t.Fatalf("failed write = %v, want an outcome-unknown error", err)
	}
	if got, want := fmt.Sprint(userTexts()), "[first second unknown]"; got != want {
		t.Fatalf("user messages = %s, want %s", got, want)
	}
}

func TestSpawnAfterCloseIsRefused(t *testing.T) {
	dataDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	sess := &scriptedSession{events: make(chan protocol.Event)}
	reg := harness.NewRegistry()
	reg.Register(&scriptedProvider{sess: sess})
	o, err := New(t.Context(), Options{DataDir: dataDir, PiG: rt, Worktrees: worktree.New(dataDir), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := o.spawnWorker(t.Context(), spawnWorkerArgs{Provider: "scripted", Directory: dataDir}); err == nil {
		t.Fatal("spawn after Close succeeded")
	}
	if _, open := <-sess.events; open {
		t.Fatal("the refused worker's session was left open")
	}
}
