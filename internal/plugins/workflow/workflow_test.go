package workflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// The engine's goroutines are real; the test goroutine is the UI thread. What
// they post waits until the test lands it, so until polls for the state a test
// expects.

type rig struct {
	t    *testing.T
	h    *kerneltest.Harness
	p    *Plugin
	a    *plugintest.Agent
	host *kittest.Host
	chat *plugintest.Chat
}

func start(t *testing.T) *rig {
	t.Helper()
	a := plugintest.NewAgent()
	a.Numbered = true
	host, c := &kittest.Host{}, &plugintest.Chat{}
	var p *Plugin
	return &rig{t: t, h: kerneltest.New(t, a.New, host.New, c.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })), p: p, a: a, host: host, chat: c}
}

func (r *rig) until(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.h.RunPosts()
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("%s never happened", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// pageOf draws the run's page as the shell would.
func (r *rig) pageOf(run *run) func(*ui.Context) {
	return func(c *ui.Context) {
		for _, e := range kernel.Items(r.h, shell.Pages) {
			if e.ID == pageID(run) {
				e.Value.Render(c)
			}
		}
	}
}

// pageTitle is the title of the run's page; empty without one.
func (r *rig) pageTitle(run *run) string {
	for _, e := range kernel.Items(r.h, shell.Pages) {
		if e.ID == pageID(run) {
			return e.Value.Title()
		}
	}
	return ""
}

// begin starts the flow in /w/app and returns its run.
func (r *rig) begin(f wf.Flow) *run {
	r.t.Helper()
	svc, ok := kernel.Use(r.h, wf.ServiceKey)
	if !ok {
		r.t.Fatal("the engine does not provide the service")
	}
	svc.Start(f, wf.Input{Brief: "add a flag", Directory: "/w/app"})
	return r.p.runs[0]
}

// untilState waits for the run to reach a state.
func (r *rig) untilState(run *run, want runState) {
	r.t.Helper()
	r.until("the run's state", func() bool { return run.state == want })
}

func oneStep(s wf.Step) wf.Flow {
	return wf.Flow{ID: "one", Title: "One step", Run: func(ctx context.Context, r wf.Runner) (string, error) {
		a, err := r.Step(ctx, s)
		return a.Text, err
	}}
}

func approval(session agent.SessionID, id string, kind agent.ToolKind) agent.Event {
	return agent.Event{Kind: agent.ApprovalRequested, Session: session, Approval: &agent.Approval{ID: agent.ApprovalID(id), Kind: kind, Resources: []string{"go test ./..."}}}
}

func TestStepsRunInOrderAndTheFlowsResultIsTheRuns(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "answer to <"+req.Text+">") }
	flow := wf.Flow{ID: "two", Title: "Two steps", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		first, err := rn.Step(ctx, wf.Step{Name: "First", Prompt: "one", Mode: "plan"})
		if err != nil {
			return "", err
		}
		second, err := rn.Step(ctx, wf.Step{Name: "Second", Prompt: "two after " + first.Text, Session: first.Session})
		return "done: " + second.Text, err
	}}
	run := r.begin(flow)
	if r.host.PageID != pageID(run) || r.pageTitle(run) != "add a flag" {
		t.Fatalf("the page on show is %q, titled %q", r.host.PageID, r.pageTitle(run))
	}
	r.untilState(run, runDone)

	if want := "done: answer to <two after answer to <one>>"; run.result != want {
		t.Fatalf("result %q, want %q", run.result, want)
	}
	// What the steps changed is read on the side; leave those reads out.
	got := slices.DeleteFunc(r.a.Calls(), func(c string) bool { return strings.HasPrefix(c, "changes ") })
	if !slices.Equal(got, []string{
		"create /w/app  plan", "send new-1 one", "send new-1 two after answer to <one>",
	}) {
		t.Fatalf("calls %q", got)
	}
	if len(run.steps) != 2 || run.steps[0].state != stepDone || run.steps[1].answer != "answer to <two after answer to <one>>" {
		t.Fatalf("steps %+v", run.steps)
	}
	if run.steps[0].session != "new-1" || run.steps[1].session != "new-1" {
		t.Fatalf("the second step opened another conversation: %q %q", run.steps[0].session, run.steps[1].session)
	}
}

func TestAStepWithoutASessionOpensItsOwn(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	flow := wf.Flow{ID: "fresh", Title: "Fresh", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		for _, name := range []string{"A", "B"} {
			if _, err := rn.Step(ctx, wf.Step{Name: name, Prompt: name}); err != nil {
				return "", err
			}
		}
		return "", nil
	}}
	run := r.begin(flow)
	r.untilState(run, runDone)
	if r.a.Called("create") != 2 || run.steps[0].session == run.steps[1].session {
		t.Fatalf("calls %q", r.a.Calls())
	}
}

func TestPermissionsFollowThePolicyThenTheUser(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		r.a.Emit(approval(req.Session, "read", agent.ToolRead), approval(req.Session, "edit", agent.ToolEdit),
			approval(req.Session, "shell", agent.ToolShell))
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go",
		Policy: wf.Policy{Allow: []agent.ToolKind{agent.ToolRead, agent.ToolEdit}, Deny: []agent.ToolKind{agent.ToolEdit}}}))
	st := func() *step { return run.steps[0] }
	r.until("the policy's answers and the user's turn", func() bool {
		return len(r.a.Replies()) == 2 && len(run.steps) == 1 && len(st().pending) == 1
	})
	decisions := map[agent.ApprovalID]agent.Decision{}
	for _, rp := range r.a.Replies() {
		decisions[rp.Approval] = rp.Decision
	}
	if decisions["read"] != agent.Once || decisions["edit"] != agent.Reject {
		t.Fatalf("decisions %v: Allow must give once, and Deny must win over Allow", decisions)
	}
	if st().pending[0].ID != "shell" || !run.steps[0].needsYou() {
		t.Fatalf("pending %+v", st().pending)
	}

	// Allowing a kind for the run answers what waits and what comes later.
	r.a.Emit(approval(st().session, "shell2", agent.ToolShell))
	r.until("a second shell request waiting", func() bool { return len(st().pending) == 2 })
	r.p.decide(st(), st().pending[0], agent.Once, true)
	r.until("both shell requests answered", func() bool { return len(r.a.Replies()) == 4 && len(st().pending) == 0 })
	r.a.Emit(approval(st().session, "shell3", agent.ToolShell))
	r.until("a later shell request answered at once", func() bool { return len(r.a.Replies()) == 5 })
	if st().needsYou() {
		t.Fatalf("pending %+v", st().pending)
	}
	for _, rp := range r.a.Replies()[2:] {
		if rp.Decision != agent.Once {
			t.Fatalf("reply %+v", rp)
		}
	}
	r.a.Respond(st().session, "finished")
	r.untilState(run, runDone)
}

func TestDenyingAPermissionRejectsIt(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Emit(approval(req.Session, "shell", agent.ToolShell)) }
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the request waiting", func() bool { return len(run.steps) == 1 && len(run.steps[0].pending) == 1 })
	st := run.steps[0]
	r.p.decide(st, st.pending[0], agent.Reject, false)
	r.until("the rejection delivered", func() bool { return len(r.a.Replies()) == 1 })
	if got := r.a.Replies()[0]; got.Decision != agent.Reject || got.Approval != "shell" || got.Session != "new-1" {
		t.Fatalf("reply %+v", got)
	}
	if run.allowed[allowKey{kind: agent.ToolShell}] {
		t.Fatal("a denial allowed the kind")
	}
}

func TestAFailedTurnWaitsForTheUserAndTryAgainGoesOnInTheSameConversation(t *testing.T) {
	r := start(t)
	turns := 0
	r.a.OnSend = func(req agent.Send) {
		turns++
		if turns == 1 {
			r.a.Emit(agent.Event{Kind: agent.TurnStarted, Session: req.Session},
				agent.Event{Kind: agent.TurnFailed, Session: req.Session, Error: "model overloaded"})
			return
		}
		r.a.Respond(req.Session, "second time lucky")
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the failure waiting for a decision", func() bool { return len(run.steps) == 1 && run.steps[0].retry != nil })
	st := run.steps[0]
	if st.state != stepFailed || !strings.Contains(st.err, "model overloaded") || run.state != runRunning {
		t.Fatalf("step %+v, run state %d", st, run.state)
	}
	if r.a.Called("abort") != 0 {
		t.Fatalf("aborted a turn that was already over: %q", r.a.Calls())
	}
	time.Sleep(20 * time.Millisecond)
	if r.a.Called("send") != 1 {
		t.Fatalf("the app retried on its own: %q", r.a.Calls())
	}

	r.p.retry(st)
	r.untilState(run, runDone)
	if run.result != "second time lucky" || st.state != stepDone || st.err != "" || st.retry != nil {
		t.Fatalf("result %q, step %+v", run.result, st)
	}
	if r.a.Called("create") != 1 || r.a.Called("send new-1 go") != 2 {
		t.Fatalf("calls %q", r.a.Calls())
	}
}

func TestASendThatFailsIsNotRetriedOnItsOwn(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "sent") }
	r.a.Fail["Send"] = errors.New("connection reset")
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the failure waiting for a decision", func() bool { return len(run.steps) == 1 && run.steps[0].retry != nil })
	st := run.steps[0]
	if !strings.Contains(st.err, "could not send the prompt: connection reset") {
		t.Fatalf("error %q", st.err)
	}
	time.Sleep(20 * time.Millisecond)
	if r.a.Called("send") != 1 {
		t.Fatalf("calls %q", r.a.Calls())
	}
	delete(r.a.Fail, "Send")
	r.p.retry(st)
	r.untilState(run, runDone)
	if r.a.Called("send") != 2 || r.a.Called("create") != 1 {
		t.Fatalf("calls %q", r.a.Calls())
	}
}

func TestStoppingAFailedStepStopsTheRun(t *testing.T) {
	r := start(t)
	r.a.Fail["Create"] = errors.New("backend down")
	reached := false
	flow := wf.Flow{ID: "two", Title: "Two", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		if _, err := rn.Step(ctx, wf.Step{Name: "First", Prompt: "one"}); err != nil {
			return "", err
		}
		reached = true
		return "", nil
	}}
	run := r.begin(flow)
	r.until("the failure waiting for a decision", func() bool { return len(run.steps) == 1 && run.steps[0].retry != nil })
	if !strings.Contains(run.steps[0].err, "could not open a conversation: backend down") {
		t.Fatalf("error %q", run.steps[0].err)
	}
	r.p.stop(run)
	r.untilState(run, runStopped)
	if reached || run.steps[0].retry != nil {
		t.Fatalf("reached %v, step %+v", reached, run.steps[0])
	}
	if r.a.Called("send") != 0 {
		t.Fatalf("calls %q", r.a.Calls())
	}
}

func TestStoppingARunningStepAbortsTheAgent(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(agent.Send) {} // the turn never ends
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the prompt sent", func() bool { return r.a.Called("send") == 1 })
	r.p.stop(run)
	if !run.stopping {
		t.Fatal("the run does not say it is stopping")
	}
	r.untilState(run, runStopped)
	if r.a.Called("abort new-1") != 1 {
		t.Fatalf("calls %q", r.a.Calls())
	}
	if st := run.steps[0]; st.state != stepStopped || st.watch != nil || len(r.p.watching) != 0 {
		t.Fatalf("step %+v, watching %d", st, len(r.p.watching))
	}
	if run.unseen || len(r.host.Notices) != 0 || len(r.host.Toasts) != 0 {
		t.Fatalf("a stop the user asked for was announced: %v %v", r.host.Toasts, r.host.Notices)
	}
}

func TestAFailedAbortIsReportedOnTheStep(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(agent.Send) {}
	r.a.Fail["Abort"] = errors.New("no route")
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the prompt sent", func() bool { return r.a.Called("send") == 1 })
	r.p.stop(run)
	r.untilState(run, runStopped)
	r.until("the note", func() bool { return strings.Contains(run.steps[0].note, "Could not stop the agent: no route") })
}

func TestQuestionsAreDismissedAndSaidSo(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		r.a.Emit(agent.Event{Kind: agent.InteractionRequested, Session: req.Session,
			Interaction: &agent.Interaction{ID: "q1", Kind: agent.Confirm, Title: "Overwrite the file?"}})
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the question dismissed", func() bool { return r.a.Called("answer") == 1 })
	if got := r.a.Calls()[2]; got != "answer q1 value= confirmed=false cancelled=true" {
		t.Fatalf("call %q", got)
	}
	r.until("the step saying so", func() bool { return len(run.steps) == 1 && run.steps[0].activity == "Skipped: Overwrite the file?" })
	r.a.Respond(run.steps[0].session, "carried on")
	r.untilState(run, runDone)
}

func TestTheStoredAnswerStandsInWhenTheEventsNameNone(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		r.a.Stored[req.Session] = []agent.Message{
			{Role: agent.RoleUser, Text: "go"},
			{Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartText, Text: "first words"}, {Kind: agent.PartTool, Tool: &agent.Tool{}}}},
			{Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartReasoning, Text: "thinking"}, {Kind: agent.PartText, Text: "the stored answer"}}},
			{Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartTool, Tool: &agent.Tool{}}}},
		}
		r.a.Emit(agent.Event{Kind: agent.TurnStarted, Session: req.Session}, agent.Event{Kind: agent.TurnSucceeded, Session: req.Session})
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(run, runDone)
	if run.result != "the stored answer" {
		t.Fatalf("result %q", run.result)
	}
}

func TestTheLastTextBlockIsTheAnswer(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		text := func(s string) agent.Event {
			return agent.Event{Kind: agent.TextEnded, Session: req.Session, Text: &agent.TextEvent{Text: s}}
		}
		r.a.Emit(agent.Event{Kind: agent.TurnStarted, Session: req.Session}, text("I'll look"),
			agent.Event{Kind: agent.ToolStarted, Session: req.Session, Tool: &agent.Tool{Kind: agent.ToolRead}},
			text("Here is the plan"), agent.Event{Kind: agent.TurnSucceeded, Session: req.Session})
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(run, runDone)
	if run.result != "Here is the plan" || run.steps[0].tools != 1 {
		t.Fatalf("result %q, tools %d", run.result, run.steps[0].tools)
	}
}

func TestAPanickingFlowFailsItsRunOnly(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "fine") }
	bad := r.begin(wf.Flow{ID: "bad", Title: "Bad", Run: func(context.Context, wf.Runner) (string, error) { panic("boom") }})
	r.untilState(bad, runFailed)
	if !strings.Contains(bad.err, "boom") {
		t.Fatalf("error %q", bad.err)
	}
	good := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(good, runDone)
	if got := r.p.runs; len(got) != 2 || got[0] != good || got[1] != bad {
		t.Fatalf("runs %v", got)
	}
}

func TestAFlowErrorFailsTheRunAndATelltaleRunIsKept(t *testing.T) {
	r := start(t)
	run := r.begin(wf.Flow{ID: "e", Title: "E", Run: func(context.Context, wf.Runner) (string, error) {
		return "ignored", errors.New("could not parse the plan")
	}})
	r.untilState(run, runFailed)
	if run.err != "could not parse the plan" || run.result != "" {
		t.Fatalf("run %+v", run)
	}
}

func TestFinishingWhileAwayToastsOrNotifies(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	r.host.PageID = "" // the user went back to the chat
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.host.PageID = ""
	r.untilState(run, runDone)
	if !slices.Equal(r.host.Toasts, []string{"One step"}) || !run.unseen {
		t.Fatalf("toasts %q, unseen %v", r.host.Toasts, run.unseen)
	}
	r.p.show(run)
	if run.unseen || r.host.PageID != pageID(run) {
		t.Fatalf("unseen %v, page %q", run.unseen, r.host.PageID)
	}

	r.host.Background = true
	second := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.host.PageID = ""
	r.untilState(second, runDone)
	if len(r.host.Notices) != 1 || r.host.Notices[0] != (shell.Notice{ID: "workflow", Title: "One step", Body: "Finished."}) {
		t.Fatalf("notices %+v", r.host.Notices)
	}
	// Clicking the notice brings the run up.
	r.p.show(run)
	kernel.Emit(r.h, shell.NoticeClicked, "workflow")
	if r.host.PageID != pageID(second) {
		t.Fatalf("page %q", r.host.PageID)
	}
}

func TestNoAnnouncementWhileTheRunIsOnShow(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(run, runDone)
	if len(r.host.Toasts) != 0 || len(r.host.Notices) != 0 || run.unseen {
		t.Fatalf("toasts %q notices %v unseen %v", r.host.Toasts, r.host.Notices, run.unseen)
	}
}

func TestOpeningAStepInTheChatRecordsItOnce(t *testing.T) {
	r := start(t)
	var started []chat.Conversation
	kernel.On(r.h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(run, runDone)
	r.host.PageID = pageID(run)

	r.p.openStep(run, run.steps[0])
	r.p.openStep(run, run.steps[0])
	want := chat.Conversation{Session: "new-1", Title: "One step · Work", Directory: "/w/app"}
	if len(started) != 1 || started[0] != want {
		t.Fatalf("started %+v", started)
	}
	if r.chat.Conv != want || r.host.PageID != chat.PageID {
		t.Fatalf("chat on %+v, page %q", r.chat.Conv, r.host.PageID)
	}
}

func TestStepConversationsStayOutOfTheRecordUntilOpened(t *testing.T) {
	r := start(t)
	var started int
	kernel.On(r.h, chat.Started, func(chat.Conversation) { started++ })
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(run, runDone)
	if started != 0 {
		t.Fatal("a hidden step was recorded as a conversation")
	}
}

func TestOnlyWatchedConversationsMatter(t *testing.T) {
	r := start(t)
	r.a.Emit(agent.Event{Kind: agent.TurnFailed, Session: "elsewhere", Error: "x"}, approval("elsewhere", "a", agent.ToolShell))
	r.h.Flush()
	r.h.RunPosts()
	if len(r.a.Replies()) != 0 || len(r.p.runs) != 0 {
		t.Fatal("the engine reacted to a conversation it does not run")
	}
}

func TestOldFinishedRunsAreDropped(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	for range maxRuns + 3 {
		run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
		r.untilState(run, runDone)
	}
	if len(r.p.runs) != maxRuns {
		t.Fatalf("kept %d runs", len(r.p.runs))
	}
}

func TestACommandShowsTheRunThatNeedsTheUser(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		if req.Text == "ask" {
			r.a.Emit(approval(req.Session, "shell", agent.ToolShell))
			return
		}
		r.a.Respond(req.Session, "ok")
	}
	waiting := r.begin(oneStep(wf.Step{Name: "Ask", Prompt: "ask"}))
	r.until("the request waiting", func() bool { return len(waiting.steps) == 1 && waiting.steps[0].needsYou() })
	other := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.untilState(other, runDone)
	r.host.PageID = ""

	show := commandRun(t, r.h, "workflow.show")
	show()
	if r.host.PageID != pageID(waiting) {
		t.Fatalf("the command showed another run")
	}
}

func commandRun(t *testing.T, h *kerneltest.Harness, id string) func() {
	t.Helper()
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == id {
			return func() { kernel.Invoke(h, e, func(c kernel.Command) { c.Run() }) }
		}
	}
	t.Fatalf("no command %q", id)
	return nil
}

func TestShowCommandIsDisabledWithoutRuns(t *testing.T) {
	r := start(t)
	for _, e := range kernel.Items(r.h, kernel.Commands) {
		if e.ID == "workflow.show" {
			enabled := true
			kernel.Invoke(r.h, e, func(c kernel.Command) { enabled = c.Enabled() })
			if enabled {
				t.Fatal("enabled with nothing to show")
			}
			return
		}
	}
	t.Fatal("no command")
}

// ---- Views

func TestThePageShowsStepsApprovalsAndTheResult(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		if req.Text == "first" {
			r.a.Emit(approval(req.Session, "shell", agent.ToolShell))
			return
		}
		r.a.Respond(req.Session, "# Summary\n\nAll **done**.")
	}
	flow := wf.Flow{ID: "f", Title: "Two steps", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		a, err := rn.Step(ctx, wf.Step{Name: "Plan", Prompt: "first"})
		if err != nil {
			return "", err
		}
		b, err := rn.Step(ctx, wf.Step{Name: "Code", Prompt: "second", Session: a.Session})
		return "Final: " + b.Text, err
	}}
	run := r.begin(flow)
	tt := ui.NewTester(r.pageOf(run), 900, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	r.until("the request waiting", func() bool { return len(run.steps) == 1 && run.steps[0].needsYou() })
	tt.Frame()
	for _, want := range []string{"Two steps", "add a flag", "Working in /w/app", "Plan", "Needs you", "Stop",
		"The agent wants to run a shell command", "go test ./...", "Allow once", "Allow for this run", "Deny"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Allow for this run"); err != nil {
		t.Fatal(err)
	}
	r.until("the answer delivered", func() bool { return len(r.a.Replies()) == 1 })
	r.a.Respond(run.steps[0].session, "the plan")
	r.untilState(run, runDone)
	tt.Frame()
	if tt.HasText("Allow once") || tt.HasText("Stop") || tt.HasText("Needs you") {
		t.Fatalf("the finished run still asks for the user: %q", tt.Texts())
	}
	for _, want := range []string{"Done", "Code", "Result", "Final:", "Copy", "Show answer"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Copy result"); err != nil {
		t.Fatal(err)
	}
	if r.host.Copied != run.result {
		t.Fatalf("copied %q", r.host.Copied)
	}
}

func TestAnAnswerOpensAndClosesOnItsStep(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "Step one:\n\n- first point\n- second point") }
	run := r.begin(wf.Flow{ID: "f", Title: "F", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		_, err := rn.Step(ctx, wf.Step{Name: "Plan", Prompt: "go"})
		return "All done.", err
	}})
	r.untilState(run, runDone)
	tt := ui.NewTester(r.pageOf(run), 900, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if tt.HasText("second point") {
		t.Fatal("the answer is open before it was asked for")
	}
	if !tt.HasText("Step one:") { // the step's quiet line
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Show answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Hide answer") || !tt.HasText("second point") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestAFailedStepOffersTryAgainAndStop(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) {
		r.a.Emit(agent.Event{Kind: agent.TurnFailed, Session: req.Session, Error: "rate limited"})
	}
	run := r.begin(oneStep(wf.Step{Name: "Work", Prompt: "go"}))
	r.until("the failure waiting for a decision", func() bool { return len(run.steps) == 1 && run.steps[0].retry != nil })
	tt := ui.NewTester(r.pageOf(run), 900, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	for _, want := range []string{"Needs your decision", "rate limited", "Try again", "Stop run"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Stop run"); err != nil {
		t.Fatal(err)
	}
	r.untilState(run, runStopped)
	tt.Frame()
	if tt.HasText("Try again") || !tt.HasText("Stopped") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestAFailedRunShowsWhy(t *testing.T) {
	r := start(t)
	run := r.begin(wf.Flow{ID: "e", Title: "E", Run: func(context.Context, wf.Runner) (string, error) { return "", errors.New("the plan was empty") }})
	r.untilState(run, runFailed)
	tt := ui.NewTester(r.pageOf(run), 900, 600)
	tt.Frame()
	for _, want := range []string{"Failed", "The run failed", "the plan was empty"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
}

func TestEachRunHasItsOwnPageWhileItIsKept(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	svc, _ := kernel.Use(r.h, wf.ServiceKey)
	svc.Start(wf.Flow{ID: "a", Title: "Alpha", Run: func(context.Context, wf.Runner) (string, error) { return "alpha result", nil }}, wf.Input{Brief: "tidy the alphabet"})
	first := r.p.runs[0]
	r.untilState(first, runDone)
	svc.Start(wf.Flow{ID: "b", Title: "Beta", Run: func(context.Context, wf.Runner) (string, error) { return "beta result", nil }}, wf.Input{Brief: "bake a beta"})
	second := r.p.runs[0]
	r.untilState(second, runDone)
	if r.pageTitle(first) != "tidy the alphabet" || r.pageTitle(second) != "bake a beta" || r.host.PageID != pageID(second) {
		t.Fatalf("pages %q and %q, on show %q", r.pageTitle(first), r.pageTitle(second), r.host.PageID)
	}
	tt := ui.NewTester(r.pageOf(first), 900, 600)
	tt.Frame()
	if !tt.HasText("alpha result") || tt.HasText("beta result") {
		t.Fatalf("texts %q", tt.Texts())
	}
	for range maxRuns {
		svc.Start(wf.Flow{ID: "c", Title: "C", Run: func(context.Context, wf.Runner) (string, error) { return "", nil }}, wf.Input{Brief: "more"})
		r.untilState(r.p.runs[0], runDone)
	}
	if r.pageTitle(first) != "" || len(r.p.runs) != maxRuns {
		t.Fatalf("the oldest run kept its page: %d runs", len(r.p.runs))
	}
}

func TestRunDetailsShowChangesAndPermissions(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	r.a.Changed["new-2"] = []agent.FileChange{{Path: "cmd/main.go", Added: 7, Removed: 2}}
	run := r.begin(wf.Flow{ID: "f", Title: "F", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		if _, err := rn.Step(ctx, wf.Step{Name: "Plan", Prompt: "go", Policy: wf.Policy{Deny: []agent.ToolKind{agent.ToolEdit}}}); err != nil {
			return "", err
		}
		_, err := rn.Step(ctx, wf.Step{Name: "Code", Prompt: "do", Policy: wf.Policy{Allow: []agent.ToolKind{agent.ToolEdit, agent.ToolRead}}})
		return "done", err
	}})
	r.untilState(run, runDone)
	r.until("the changes", func() bool { return len(run.steps) == 2 && len(run.steps[1].changes) == 1 })
	run.allowed[allowKey{kind: agent.ToolShell}] = true
	tt := ui.NewTester(r.pageOf(run), 1100, 800)
	tt.Frame()
	for _, want := range []string{"Changes", "cmd/main.go", "+7", "−2", "Allowed without asking", "Edit files · Code",
		"Read files · Code", "Run commands · this run", "Never allowed", "Edit files · Plan"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
}

func TestTheBriefDialogStartsTheFlowInTheChatsFolder(t *testing.T) {
	r := start(t)
	r.a.OnSend = func(req agent.Send) { r.a.Respond(req.Session, "ok") }
	r.chat.Conv = chat.Conversation{Session: "s1", Directory: "/w/docs"}
	var got wf.Input
	flow := wf.Flow{ID: "f", Title: "My flow", Summary: "Does the thing.", Run: func(ctx context.Context, rn wf.Runner) (string, error) {
		got = rn.Input()
		return "", nil
	}}
	svc, _ := kernel.Use(r.h, wf.ServiceKey)
	svc.Prompt(flow)
	tt := ui.NewTester(r.p.promptView, 900, 700)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	tt.Advance(time.Second)
	for _, want := range []string{"My flow", "Does the thing.", "In /w/docs", "Start", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if len(r.p.runs) != 0 {
		t.Fatal("started without a brief")
	}
	tt.Type("  tidy the docs  ")
	tt.Frame()
	if err := tt.Click("Start"); err != nil {
		t.Fatal(err)
	}
	if len(r.p.runs) != 1 || r.p.ask.open {
		t.Fatalf("runs %d, dialog open %v", len(r.p.runs), r.p.ask.open)
	}
	r.untilState(r.p.runs[0], runDone)
	if got != (wf.Input{Brief: "tidy the docs", Directory: "/w/docs"}) {
		t.Fatalf("input %+v", got)
	}
}
