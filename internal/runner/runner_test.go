package runner_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/runner"
)

type fakeClient struct {
	agent.Client
	address       string
	eventsStarted chan struct{}
	eventsEnd     <-chan struct{}
	prompt        func(context.Context, string, string) error
	interrupt     func(context.Context, string) error
	projects      func() ([]string, error)
	oneMessage    func(context.Context, string, string) (agent.Message, error)
	opened        chan string
	created       chan [2]string
	stopped       atomic.Bool
}

func client(address string) *fakeClient {
	return &fakeClient{address: address, eventsStarted: make(chan struct{}), opened: make(chan string, 8), created: make(chan [2]string, 8)}
}
func (c *fakeClient) Stop()             { c.stopped.Store(true) }
func (c *fakeClient) Info() agent.Facts { return agent.Facts{} }
func (c *fakeClient) Endpoint() string  { return c.address }
func (c *fakeClient) Updates(ctx context.Context, _ func(agent.Update), _ func(string)) error {
	close(c.eventsStarted)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.eventsEnd:
		return errors.New("event stream disconnected")
	}
}
func (c *fakeClient) ListSessions(context.Context, string, string, string) ([]agent.Session, string, error) {
	return []agent.Session{{ID: c.address}}, "", nil
}
func (c *fakeClient) ListModels(context.Context) ([]agent.Model, error) { return nil, nil }
func (c *fakeClient) DefaultModel(context.Context) (agent.ModelRef, error) {
	return agent.ModelRef{}, nil
}
func (c *fakeClient) ListProjects(context.Context) ([]string, error) {
	if c.projects != nil {
		return c.projects()
	}
	return []string{c.address}, nil
}
func (c *fakeClient) ListAgents(context.Context) ([]agent.Persona, error) {
	return []agent.Persona{{ID: "build", Name: "Build"}}, nil
}
func (c *fakeClient) CreateSession(_ context.Context, dir, persona string) (string, agent.ModelRef, error) {
	c.created <- [2]string{dir, persona}
	return "ses_born", agent.ModelRef{ProviderID: "prov", ID: "free-model"}, nil
}
func (c *fakeClient) SetModel(context.Context, string, string, string, string) error { return nil }
func (c *fakeClient) Prompt(ctx context.Context, session, text string) error {
	if c.prompt != nil {
		return c.prompt(ctx, session, text)
	}
	return nil
}
func (c *fakeClient) Interrupt(ctx context.Context, session string) error {
	if c.interrupt != nil {
		return c.interrupt(ctx, session)
	}
	return nil
}
func (c *fakeClient) SessionMessages(_ context.Context, session string) ([]agent.Message, error) {
	c.opened <- session
	return nil, nil
}
func (c *fakeClient) SessionMessage(ctx context.Context, session, message string) (agent.Message, error) {
	if c.oneMessage != nil {
		return c.oneMessage(ctx, session, message)
	}
	return agent.Message{}, errors.New("not captured")
}

type recordingSink struct {
	statuses     []runner.Status
	pages        []runner.Page
	notices      []string
	projectLists int
}

func (s *recordingSink) Status(v runner.Status) { s.statuses = append(s.statuses, v) }
func (s *recordingSink) Sessions(v runner.Page) { s.pages = append(s.pages, v) }
func (*recordingSink) Catalog(runner.Catalog)   {}
func (s *recordingSink) Projects([]string)      { s.projectLists++ }
func (*recordingSink) Updates([]agent.Update)   {}
func (*recordingSink) Opened(runner.Opened)     {}
func (*recordingSink) Created(runner.Created)   {}
func (*recordingSink) Answered(string)          {}
func (*recordingSink) Deleted(string)           {}
func (*recordingSink) Usage(agent.Message)      {}
func (s *recordingSink) Notice(v string)        { s.notices = append(s.notices, v) }

type dispatcher struct{ callbacks chan func() }

func newDispatcher() *dispatcher     { return &dispatcher{callbacks: make(chan func(), 128)} }
func (d *dispatcher) post(fn func()) { d.callbacks <- fn }
func (d *dispatcher) drain() {
	for {
		select {
		case fn := <-d.callbacks:
			fn()
		default:
			return
		}
	}
}
func (d *dispatcher) until(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case fn := <-d.callbacks:
			fn()
		case <-deadline.C:
			t.Fatal("runner did not publish the expected result")
		}
	}
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not reach the expected barrier")
		var zero T
		return zero
	}
}
func await(t *testing.T, ch <-chan struct{}) { t.Helper(); receive(t, ch) }
func newRunner(t *testing.T, d *dispatcher, sink *recordingSink, options runner.Options) *runner.Runner {
	t.Helper()
	r := runner.New(context.Background(), d.post, sink, options)
	t.Cleanup(func() { r.Close(); await(t, r.Done()) })
	return r
}
func connection(id string) runner.Connection {
	return runner.Connection{Projects: true, ID: id, Address: id}
}

func TestSelectCancelsBlockedStartupAndRejectsOldPublication(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	oldStarted := make(chan context.Context, 1)
	next := client("new")
	r := newRunner(t, d, sink, runner.Options{Start: func(ctx context.Context, cfg runner.Connection) (agent.Client, error) {
		if cfg.Address == "old" {
			oldStarted <- ctx
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return next, nil
	}})
	r.Select(1, connection("old"))
	oldContext := receive(t, oldStarted)
	r.Select(2, connection("new"))
	await(t, next.eventsStarted)
	if oldContext.Err() != context.Canceled {
		t.Fatal("selection did not cancel blocked startup")
	}
	d.drain()
	ready := false
	for _, status := range sink.statuses {
		if status.Address == "old" {
			t.Fatalf("obsolete startup published %+v", status)
		}
		ready = ready || status.State == "ready" && status.Address == "new"
	}
	if !ready {
		t.Fatal("replacement startup did not publish ready")
	}
}

func TestSelectCancelsBlockedPrompt(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	old, next := client("old"), client("new")
	started := make(chan context.Context, 1)
	old.prompt = func(ctx context.Context, _, _ string) error { started <- ctx; <-ctx.Done(); return ctx.Err() }
	r := newRunner(t, d, sink, runner.Options{Start: func(_ context.Context, cfg runner.Connection) (agent.Client, error) {
		if cfg.Address == "old" {
			return old, nil
		}
		return next, nil
	}})
	r.Select(1, connection("old"))
	await(t, old.eventsStarted)
	if r.Submit(1, runner.Prompt{Session: "session", Text: "question"}) != runner.Accepted {
		t.Fatal("prompt rejected")
	}
	ctx := receive(t, started)
	r.Select(2, connection("new"))
	await(t, next.eventsStarted)
	if ctx.Err() != context.Canceled {
		t.Fatal("selection left blocked prompt running")
	}
	d.drain()
	for _, notice := range sink.notices {
		if strings.Contains(notice, "prompt") {
			t.Fatalf("obsolete prompt published %q", notice)
		}
	}
}

func TestStopInterruptsWhilePromptBlocks(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	started, interrupted := make(chan context.Context, 1), make(chan string, 1)
	c.prompt = func(ctx context.Context, _, _ string) error { started <- ctx; <-ctx.Done(); return ctx.Err() }
	c.interrupt = func(_ context.Context, session string) error { interrupted <- session; return nil }
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }})
	r.Select(1, connection("server"))
	await(t, c.eventsStarted)
	if r.Submit(1, runner.Prompt{Session: "session", Text: "question"}) != runner.Accepted {
		t.Fatal("prompt rejected")
	}
	ctx := receive(t, started)
	if r.Submit(1, runner.Stop{Session: "session"}) != runner.Accepted {
		t.Fatal("stop rejected behind a blocked prompt")
	}
	if got := receive(t, interrupted); got != "session" {
		t.Fatalf("interrupted %q", got)
	}
	if ctx.Err() != nil {
		t.Fatal("stop canceled the connection instead of issuing an interrupt")
	}
}

func TestFullQueueReportsBusyAndReservesControl(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	started := make(chan struct{})
	r := newRunner(t, d, sink, runner.Options{Capacity: 1, Start: func(ctx context.Context, _ runner.Connection) (agent.Client, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	r.Select(1, connection("server"))
	await(t, started)
	if got := r.Submit(1, runner.Prompt{Session: "session", Text: "first"}); got != runner.Accepted {
		t.Fatalf("first admission=%v", got)
	}
	if got := r.Submit(1, runner.Prompt{Session: "session", Text: "second"}); got != runner.Busy {
		t.Fatalf("full admission=%v, want busy", got)
	}
	if got := r.Submit(1, runner.Stop{Session: "session"}); got != runner.Accepted {
		t.Fatalf("control admission=%v", got)
	}
	if got := r.Submit(0, runner.Refresh{}); got != runner.Invalid {
		t.Fatalf("obsolete generation admission=%v", got)
	}
	r.Close()
	await(t, r.Done())
	if got := r.Submit(1, runner.Refresh{}); got != runner.Closed {
		t.Fatalf("closed admission=%v", got)
	}
}

func TestRetryRejectsPostedCallbacksFromPreviousAttempt(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	old, next := client("attempt-1"), client("attempt-2")
	end := make(chan struct{})
	old.eventsEnd = end
	tick := make(chan time.Time)
	after := make(chan time.Duration, 1)
	var starts atomic.Int32
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) {
		if starts.Add(1) == 1 {
			return old, nil
		}
		return next, nil
	}, After: func(delay time.Duration) <-chan time.Time { after <- delay; return tick }})
	r.Select(1, connection("server"))
	await(t, old.eventsStarted)
	close(end)
	if delay := receive(t, after); delay != time.Second {
		t.Fatalf("retry delay=%s", delay)
	}
	tick <- time.Time{}
	await(t, next.eventsStarted)
	d.drain()
	for _, status := range sink.statuses {
		if status.Address == "attempt-1" {
			t.Fatalf("old attempt published %+v", status)
		}
	}
	for _, page := range sink.pages {
		for _, session := range page.Sessions {
			if session.ID == "attempt-1" {
				t.Fatal("old bootstrap published after a new attempt started")
			}
		}
	}
	ready := false
	for _, status := range sink.statuses {
		ready = ready || status.State == "ready" && status.Address == "attempt-2"
	}
	if !ready {
		t.Fatal("new attempt did not publish ready")
	}
}

func TestUncertainPromptOutcomeIsNeverReplayed(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	first, next := client("attempt-1"), client("attempt-2")
	var prompts atomic.Int32
	prompt := func(context.Context, string, string) error { prompts.Add(1); return errors.New("response lost") }
	first.prompt, next.prompt = prompt, prompt
	end := make(chan struct{})
	first.eventsEnd = end
	tick := make(chan time.Time)
	after := make(chan time.Duration, 1)
	var starts atomic.Int32
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) {
		if starts.Add(1) == 1 {
			return first, nil
		}
		return next, nil
	}, After: func(delay time.Duration) <-chan time.Time { after <- delay; return tick }})
	r.Select(1, connection("server"))
	await(t, first.eventsStarted)
	if r.Submit(1, runner.Prompt{Session: "session", Text: "mutating prompt"}) != runner.Accepted {
		t.Fatal("prompt rejected")
	}
	d.until(t, func() bool {
		for _, notice := range sink.notices {
			if strings.Contains(notice, "uncertain") {
				return true
			}
		}
		return false
	})
	close(end)
	receive(t, after)
	tick <- time.Time{}
	await(t, next.eventsStarted)
	if r.Submit(1, runner.Open{Session: "barrier"}) != runner.Accepted {
		t.Fatal("read barrier rejected")
	}
	if receive(t, next.opened) != "barrier" {
		t.Fatal("read barrier did not run")
	}
	if got := prompts.Load(); got != 1 {
		t.Fatalf("uncertain mutation was sent %d times", got)
	}
}

// TestChatBirthCarriesThePersona keeps a persona pick from the composer
// landing in the create call: a draft's first prompt asks the server for
// a conversation born under that persona.
func TestChatBirthCarriesThePersona(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }})
	r.Select(1, connection("server"))
	await(t, c.eventsStarted)
	if r.Submit(1, runner.Prompt{Session: "", Text: "plan this", Directory: "/srv/app", Agent: "plan"}) != runner.Accepted {
		t.Fatal("the persona prompt was rejected")
	}
	if got := receive(t, c.created); got != [2]string{"/srv/app", "plan"} {
		t.Fatalf("the persona did not reach the create call: %v", got)
	}
}

// TestPlainDropSaysItIsTryingAgain keeps an idle connection's drop from
// passing quietly: the note names the drop, and the health dot names the
// down state while the retry gives it another try.
func TestPlainDropSaysItIsTryingAgain(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	end := make(chan struct{})
	c.eventsEnd = end
	after := make(chan time.Time) // the retry holds off, so one attempt runs
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }, After: func(time.Duration) <-chan time.Time { return after }})
	r.Select(1, connection("server"))
	await(t, c.eventsStarted)
	close(end)
	var dropped bool
	d.until(t, func() bool {
		for _, notice := range sink.notices {
			if strings.Contains(notice, "connection dropped") {
				dropped = true
				return true
			}
		}
		return false
	})
	if !dropped {
		t.Fatalf("the drop went unreported: %v", sink.notices)
	}
	down := false
	for _, status := range sink.statuses {
		if status.State == "down" && status.Address == "server" {
			down = true
		}
	}
	if !down {
		t.Fatal("the drop did not put the server down")
	}
}

func TestShutdownWaitsForClientAndWorkers(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }})
	r.Select(1, connection("server"))
	await(t, c.eventsStarted)
	r.Close()
	await(t, r.Done())
	if !c.stopped.Load() {
		t.Fatal("Done closed before stopping the client")
	}
	d.drain()
	if len(sink.statuses) != 0 || len(sink.pages) != 0 {
		t.Fatal("shutdown accepted already-posted callbacks")
	}
}

func TestAdmissionRejectsUnsupportedPointerFormsWithoutPanicking(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }})
	r.Select(1, connection("server"))
	for _, command := range []runner.Command{nil, (*runner.Prompt)(nil), &runner.Prompt{Text: "hello"}, runner.Answer{Session: "s", Request: "p", Decision: "invalid"}} {
		if r.Submit(1, command) != runner.Invalid {
			t.Fatalf("unsupported command was accepted: %T", command)
		}
	}
}

// TestProjectLoadFailureIsNotSilent keeps the workspace list's failures
// from reading as an empty server: the folders already known stand, and
// the load's failure is named.
func TestProjectLoadFailureIsNotSilent(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	bad, good := client("bad"), client("good")
	bad.projects = func() ([]string, error) { return nil, errors.New("store unavailable") }
	good.projects = func() ([]string, error) { return []string{good.address}, nil }
	r := newRunner(t, d, sink, runner.Options{Start: func(_ context.Context, cfg runner.Connection) (agent.Client, error) {
		if cfg.Address == "bad" {
			return bad, nil
		}
		return good, nil
	}})
	r.Select(1, connection("bad"))
	await(t, bad.eventsStarted)
	d.until(t, func() bool { return len(sink.notices) > 0 })
	var named bool
	for _, notice := range sink.notices {
		if strings.Contains(notice, "workspace list did not load") && strings.Contains(notice, "store unavailable") {
			named = true
		}
	}
	if !named || sink.projectLists != 0 {
		t.Fatalf("the project failure went unreported or published anyway: notices=%v lists=%d",
			sink.notices, sink.projectLists)
	}

	// A good load publishes the list and says nothing.
	r.Select(2, connection("good"))
	await(t, good.eventsStarted)
	d.until(t, func() bool { return sink.projectLists > 0 })
}

// TestUsageFetchFailureReportsItsLine keeps a turn's costing fetch from
// failing quietly: the failure names what did not load, and a good fetch
// publishes the stored message.
func TestUsageFetchFailureReportsItsLine(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("server")
	var calls atomic.Int32
	c.oneMessage = func(_ context.Context, session, message string) (agent.Message, error) {
		calls.Add(1)
		if session == "session" && message == "msg_1" {
			return agent.Message{}, errors.New("the store blinked")
		}
		return agent.Message{}, errors.New("unexpected ask")
	}
	r := newRunner(t, d, sink, runner.Options{Start: func(context.Context, runner.Connection) (agent.Client, error) { return c, nil }})
	r.Select(1, connection("server"))
	await(t, c.eventsStarted)
	if r.Submit(1, runner.Usage{Session: "session", Message: "msg_1"}) != runner.Accepted {
		t.Fatal("usage request rejected")
	}
	var named bool
	d.until(t, func() bool {
		for _, notice := range sink.notices {
			if strings.Contains(notice, "What this turn spent did not load") {
				named = true
				return true
			}
		}
		return false
	})
	if !named {
		t.Fatalf("the usage failure went unreported: %v", sink.notices)
	}
}

func (s *recordingSink) Failed(failure runner.Failure) { s.notices = append(s.notices, failure.Text) }

// Embedding the core interface deliberately removes the fake's optional
// workspace and persona capabilities from the runner's available contract.
type coreOnlyClient struct{ agent.Client }

func TestInjectedChatBackendNeedsNoWorkspaceOrWireProtocol(t *testing.T) {
	d, sink := newDispatcher(), &recordingSink{}
	c := client("independent-backend")
	prompts := make(chan string, 1)
	c.prompt = func(_ context.Context, _, text string) error { prompts <- text; return nil }
	r := newRunner(t, d, sink, runner.Options{})
	r.Select(1, runner.Connection{ID: "chat", Open: func(context.Context) (agent.Client, error) { return coreOnlyClient{c}, nil }})
	await(t, c.eventsStarted)
	if got := r.Submit(1, runner.Prompt{Text: "hello"}); got != runner.Accepted {
		t.Fatalf("admission=%v", got)
	}
	if text := receive(t, prompts); text != "hello" {
		t.Fatalf("prompt=%q", text)
	}
	r.Close()
	await(t, r.Done())
	d.drain()
	if sink.projectLists != 0 {
		t.Fatal("chat-only backend published workspace state")
	}
}
