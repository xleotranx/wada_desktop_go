package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	wf "wada-desktop-go/internal/contract/workflow"

	"github.com/xleotranx/wada-kit/kernel"
)

// A run is one execution of a flow. Everything below belongs to the UI thread:
// the flow's goroutine changes it only through onUI, and the views only read it.

type runState uint8

const (
	runRunning runState = iota
	runDone
	runFailed
	runStopped
)

type run struct {
	number   int           // names its page
	scope    *kernel.Scope // holds its page while the run is kept
	flow     wf.Flow
	in       wf.Input // never changes, so the flow's goroutine reads it freely
	state    runState
	stopping bool // Stop was pressed; the flow is winding down
	unseen   bool // it ended while its page was not on show
	cancel   context.CancelFunc
	steps    []*step
	result   string // the flow's markdown result, once done
	err      string // why it failed
	allowed  map[allowKey]bool
}

// allowKey is what "allow for this run" remembers about a permission.
type allowKey struct {
	kind   agent.ToolKind
	action string // only for ToolOther, where the kind says nothing
}

func keyOf(a agent.Approval) allowKey {
	if a.Kind == agent.ToolOther {
		return allowKey{a.Kind, a.Action}
	}
	return allowKey{kind: a.Kind}
}

type stepState uint8

const (
	stepRunning stepState = iota
	stepDone
	stepFailed  // an attempt failed; the user decides while retry is set
	stepStopped // the run ended while it was working
)

type step struct {
	name     string
	session  agent.SessionID // empty until the conversation exists
	state    stepState
	tools    int    // tool calls so far in this attempt
	activity string // what the agent is doing now
	answer   string // the agent's final words, once done
	err      string // why the last attempt failed
	note     string // something to know, such as a failure to stop the agent
	pending  []agent.Approval
	retry    chan struct{} // set while the user chooses between Retry and Stop
	watch    *watch
	open     bool // the answer is shown in full
	adopted  bool // recorded in the workspace, so All chats lists it
	policy   wf.Policy
	changes  []agent.FileChange // what its conversation changed, read when it ended
}

// needsYou reports that the step waits for the user.
func (s *step) needsYou() bool { return len(s.pending) > 0 || s.retry != nil }

// watch ties a conversation's events to the flow's goroutine waiting for its
// turn. The UI thread fills it in and closes done; the goroutine reads it only
// after done is closed.
type watch struct {
	run     *run
	step    *step
	session agent.SessionID
	policy  wf.Policy

	last  string // the latest finished text block: the final answer once the turn ends
	done  chan struct{}
	err   error // why the turn did not succeed
	over  bool  // the turn is known to be over, so there is nothing to abort
	ended bool
}

// runner is the flow's handle on the engine. Its methods run on the flow's
// goroutine.
type runner struct {
	p   *Plugin
	run *run
}

func (r *runner) Input() wf.Input { return r.run.in }

// Step runs one prompt. A failed attempt waits for the user's choice instead
// of ending the step, and nothing here retries a mutation on its own.
func (r *runner) Step(ctx context.Context, s wf.Step) (wf.Answer, error) {
	st := &step{name: s.Name, policy: s.Policy}
	if err := r.p.onUI(ctx, func() { r.run.steps = append(r.run.steps, st) }); err != nil {
		return wf.Answer{}, wf.ErrStopped
	}
	session := s.Session
	for {
		answer, err := r.attempt(ctx, s, st, &session)
		if err == nil {
			return answer, nil
		}
		if ctx.Err() != nil || !r.decide(ctx, st, err) {
			return wf.Answer{}, wf.ErrStopped
		}
	}
}

// attempt sends the prompt and waits for the turn to end. session carries the
// conversation between attempts, so a retry goes on in the one an earlier
// attempt opened.
func (r *runner) attempt(ctx context.Context, s wf.Step, st *step, session *agent.SessionID) (wf.Answer, error) {
	p := r.p
	if *session == "" {
		created, err := p.agent.Create(ctx, agent.CreateSession{Directory: r.run.in.Directory, Mode: s.Mode})
		if created.ID != "" { // opened, though a later part of Create failed
			*session = created.ID
		}
		if err != nil {
			return wf.Answer{}, fmt.Errorf("could not open a conversation: %w", err)
		}
	}
	id := *session
	w := &watch{run: r.run, step: st, session: id, policy: s.Policy, done: make(chan struct{})}
	err := p.onUI(ctx, func() {
		st.session, st.state, st.err, st.retry, st.activity, st.tools = id, stepRunning, "", nil, "Starting", 0
		st.watch = w
		p.watching[id] = w
	})
	if err != nil {
		return wf.Answer{}, err
	}
	disposition, err := p.agent.Send(ctx, agent.Send{Session: id, Text: s.Prompt})
	if err != nil {
		p.drop(w)
		return wf.Answer{}, fmt.Errorf("could not send the prompt: %w", err)
	}
	if disposition == agent.SendHandled {
		p.drop(w)
		return wf.Answer{}, errors.New("the agent took the prompt as a command and did not run it")
	}
	select {
	case <-w.done:
	case <-ctx.Done():
		r.abort(ctx, st, id)
		p.drop(w)
		return wf.Answer{}, ctx.Err()
	}
	if w.err != nil {
		if !w.over {
			r.abort(ctx, st, id)
		}
		return wf.Answer{}, w.err
	}
	text := w.last
	if text == "" { // the events named no answer; the stored messages do
		if msgs, err := p.agent.Messages(ctx, id); err == nil {
			text = finalText(msgs)
		}
	}
	p.ctx.Post(func() {
		st.state, st.answer, st.activity, st.watch = stepDone, text, "", nil
	})
	return wf.Answer{Session: id, Text: text}, nil
}

// decide shows the failure and waits for the user: true to try again, false
// once the run is stopped.
func (r *runner) decide(ctx context.Context, st *step, failure error) bool {
	retry := make(chan struct{}, 1)
	if err := r.p.onUI(ctx, func() { st.state, st.err, st.retry, st.watch = stepFailed, failure.Error(), retry, nil }); err != nil {
		return false
	}
	select {
	case <-retry:
		return true
	case <-ctx.Done():
		return false
	}
}

// abort asks the backend to stop a turn the run no longer waits for. It can
// fail like any mutation; the step says so, since the agent may go on.
func (r *runner) abort(ctx context.Context, st *step, session agent.SessionID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := r.p.agent.Abort(ctx, session); err != nil {
		r.p.ctx.Post(func() { st.note = "Could not stop the agent: " + err.Error() })
	}
}

// finalText is the words of the last assistant message that has any.
func finalText(msgs []agent.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != agent.RoleAssistant {
			continue
		}
		var parts []string
		for _, part := range msgs[i].Parts {
			if part.Kind == agent.PartText && strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}
	return ""
}

// call runs the flow, turning its panic into the run's failure so one broken
// flow does not take the engine down.
func call(ctx context.Context, f wf.Flow, r wf.Runner) (result string, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("the flow panicked: %v", v)
		}
	}()
	if f.Run == nil {
		return "", errors.New("the flow has nothing to run")
	}
	return f.Run(ctx, r)
}

// working words what a tool call is doing while it runs.
func working(kind agent.ToolKind, name string) string {
	switch kind {
	case agent.ToolShell:
		return "Running a command"
	case agent.ToolRead:
		return "Reading files"
	case agent.ToolSearch:
		return "Searching"
	case agent.ToolEdit:
		return "Editing files"
	case agent.ToolFetch:
		return "Fetching from the web"
	case agent.ToolOther:
	}
	return "Using " + cmp.Or(name, "a tool")
}
