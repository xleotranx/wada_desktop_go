package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	once    sync.Once
}

func (s *scriptedSession) Send(context.Context, string) error { return s.sendErr }
func (*scriptedSession) Abort(context.Context) error          { return nil }
func (*scriptedSession) Reply(context.Context, string, protocol.Decision) error {
	return nil
}
func (s *scriptedSession) Events() <-chan protocol.Event { return s.events }
func (s *scriptedSession) Close() error {
	s.once.Do(func() { close(s.events) })
	return nil
}

// exit ends the event stream as if the CLI process had exited.
func (s *scriptedSession) exit() { s.once.Do(func() { close(s.events) }) }

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
	// A worker's pending approval is listed in its parent conversation until
	// it is answered.
	sess.events <- protocol.Event{Kind: protocol.EventApprovalRequested, Approval: &protocol.Approval{ID: "ap1", Action: "Bash"}}
	waitStatus(protocol.WorkerWaitingApproval)
	pending, err := o.PendingApprovals(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ShownIn != "parent" || pending[0].Approval.Session != info.ID || pending[0].Approval.ID != "ap1" {
		t.Fatalf("pending approvals = %+v, err = %v", pending, err)
	}
	if err := o.Reply(t.Context(), protocol.ReplyRequest{Session: info.ID, Approval: "ap1", Decision: protocol.DecisionOnce}); err != nil {
		t.Fatal(err)
	}
	if pending, _ := o.PendingApprovals(t.Context()); len(pending) != 0 {
		t.Fatalf("pending approvals after the reply = %+v", pending)
	}
	sess.events <- protocol.Event{Kind: protocol.EventTurnSucceeded}
	waitStatus(protocol.WorkerCompleted)

	// What a CLI harness cannot take is refused before anything is recorded.
	for _, req := range []protocol.SendRequest{
		{Files: []protocol.File{{Name: "a.png"}}},
		{Skills: []protocol.SkillRef{{ID: "review"}}},
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

func TestWorkerExitDuringTurnFailsTheTurn(t *testing.T) {
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
	failed := make(chan protocol.Event, 1)
	go func() {
		for ev := range o.Events() {
			if ev.Kind == protocol.EventTurnFailed {
				failed <- ev
			}
		}
	}()

	info, err := o.spawnWorker(t.Context(), spawnWorkerArgs{ParentSession: "parent", Provider: "scripted", Directory: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Send(t.Context(), protocol.SendRequest{Session: info.ID, Text: "go"}); err != nil {
		t.Fatal(err)
	}
	sess.events <- protocol.Event{Kind: protocol.EventTurnStarted}
	sess.events <- protocol.Event{Kind: protocol.EventApprovalRequested, Approval: &protocol.Approval{ID: "ap1", Action: "Bash"}}
	sess.exit()

	select {
	case ev := <-failed:
		if ev.Session != info.ID || ev.Error == "" {
			t.Fatalf("turn end = %+v, want a failure for %s", ev, info.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no turn failure after the worker exited mid-turn")
	}
	if st := o.getWorker(info.ID).snapshot().Status; st != protocol.WorkerFailed {
		t.Fatalf("status = %s, want failed", st)
	}
	if pending, _ := o.PendingApprovals(t.Context()); len(pending) != 0 {
		t.Fatalf("pending approvals after exit = %+v", pending)
	}
}

func TestSpawnAgentStaysWithinTheConversation(t *testing.T) {
	dataDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	reg := harness.NewRegistry()
	var started []harness.SessionOptions
	reg.Register(recordingProvider{start: func(opts harness.SessionOptions) { started = append(started, opts) }})
	o, err := New(t.Context(), Options{DataDir: dataDir, PiG: rt, Worktrees: worktree.New(dataDir), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	go func() {
		for range o.Events() {
		}
	}()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "escape")); err != nil {
		t.Fatal(err)
	}
	spawn := func(full bool, args string) error {
		for _, tool := range o.ChiefTools(func() pig.ChiefContext {
			return pig.ChiefContext{SessionID: "parent", Dir: folder, FullAccess: full}
		}) {
			if tool.Name == "spawn_agent" {
				_, err := tool.Execute(t.Context(), []byte(args))
				return err
			}
		}
		t.Fatal("no spawn_agent tool")
		return nil
	}

	if err := spawn(false, `{"provider":"scripted","name":"a","mode":"full"}`); err != nil {
		t.Fatal(err)
	}
	if err := spawn(true, `{"provider":"scripted","name":"b","mode":"full"}`); err != nil {
		t.Fatal(err)
	}
	if err := spawn(false, `{"provider":"scripted","name":"c","directory":"sub"}`); err != nil {
		t.Fatal(err)
	}
	if len(started) != 3 || started[0].Mode != "ask" || started[1].Mode != "full" || started[2].Directory != filepath.Join(folder, "sub") {
		t.Fatalf("started = %+v", started)
	}
	if err := spawn(false, `{"provider":"scripted","name":"f","directory":"escape"}`); err == nil {
		t.Fatal("a worker was spawned through a symlink out of the conversation's folder")
	}
	if err := spawn(false, `{"provider":"scripted","name":"d","directory":"`+outside+`"}`); err == nil {
		t.Fatal("a worker was spawned outside the conversation's folder")
	}
	if err := spawn(false, `{"provider":"scripted","name":"e","directory":"../elsewhere"}`); err == nil {
		t.Fatal("a worker was spawned above the conversation's folder")
	}
}

type recordingProvider struct{ start func(harness.SessionOptions) }

func (recordingProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{ID: "scripted", Name: "Scripted"}
}

func (p recordingProvider) Start(_ context.Context, opts harness.SessionOptions) (harness.Session, error) {
	p.start(opts)
	return &scriptedSession{events: make(chan protocol.Event, 1)}, nil
}

func TestDeletingAConversationStopsItsWorkers(t *testing.T) {
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
	parent, err := o.Create(t.Context(), protocol.CreateSessionRequest{Directory: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	info, err := o.spawnWorker(t.Context(), spawnWorkerArgs{ParentSession: parent.ID, Provider: "scripted", Directory: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Delete(t.Context(), parent.ID); err != nil {
		t.Fatal(err)
	}
	if o.getWorker(info.ID) != nil {
		t.Fatal("the deleted conversation's worker is still registered")
	}
	if _, open := <-sess.events; open {
		t.Fatal("the worker's session was not closed")
	}
}
