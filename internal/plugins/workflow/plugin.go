// Package workflow is the workflow engine. A flow is Go code that drives the
// agent backend through conversations the user does not see: each step sends a
// prompt, waits for the agent's turn to end and hands the final answer back to
// the flow, which decides what to ask next. The engine runs the flow, answers
// the agent's permission requests by the step's policy or by asking the user,
// and shows the run, step by step, on its own page with the flow's result at
// the end. It knows no flow; other plugins bring them and start them through
// workflow.Service. Each run kept has its own page, which the tabs show as a
// tab while it is open.
package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

const (
	maxRuns    = 8 // runs kept; the oldest finished ones go first
	noticeID   = "workflow"
	pagePrefix = "workflow.run/" // then the run's number: its page's ID
)

// Plugin is the workflow engine. The zero value is ready to install.
//
// The conversations of a flow's steps are opened with the backend but kept out
// of the app's record, so the tabs and All chats do not list them; a step
// is recorded when the user opens it in the chat.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	host  shell.Host

	runs     []*run // newest first
	numbered int    // runs started, which numbers their pages
	watching map[agent.SessionID]*watch

	ask prompt
}

// prompt is the dialog that asks for a flow's brief and folder.
type prompt struct {
	open   bool
	flow   wf.Flow
	brief  string
	folder string
}

var _ wf.Service = (*Plugin)(nil)

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "workflow", Title: "Workflows",
		Description: "Run flows written in Go that drive the agent through several conversations, such as plan, code and review.",
		Requires:    []kernel.Named{agent.ServiceKey, shell.HostKey},
		Provides:    []kernel.Named{wf.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.runs, p.numbered, p.watching, p.ask = nil, 0, map[agent.SessionID]*watch{}, prompt{}
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, shell.PageChanged, func(id string) {
		for _, r := range p.runs {
			if pageID(r) == id {
				r.unseen = false
			}
		}
	})
	kernel.On(c, shell.NoticeClicked, func(id string) {
		if id == noticeID {
			p.showLatest()
		}
	})
	for _, err := range []error{
		kernel.Provide[wf.Service](c, wf.ServiceKey, p),
		kernel.Contribute(c, shell.Overlays, "workflow.prompt", 20, shell.View{Render: p.promptView}),
		kernel.Contribute(c, kernel.Commands, "workflow.show", 0, kernel.Command{
			Title: "Show Workflow Run", Menu: "View",
			Enabled: func() bool { return len(p.runs) > 0 }, Run: p.showLatest}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// ---- Starting runs

// Prompt opens the dialog for a flow's brief, in the folder of the open chat.
func (p *Plugin) Prompt(f wf.Flow) {
	folder := ""
	if ctl, ok := kernel.Use(p.ctx, chat.ControllerKey); ok {
		folder = ctl.Current().Directory
	}
	p.ask = prompt{open: true, flow: f, folder: folder}
}

// chooseFolder lets the user pick another folder for the brief on show.
func (p *Plugin) chooseFolder() {
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
		ws.ChooseFolder(func(path string) { p.ask.folder = path })
	}
}

// submit starts the flow from the dialog, once there is a brief.
func (p *Plugin) submit() {
	brief := strings.TrimSpace(p.ask.brief)
	if brief == "" || !p.ask.open {
		return
	}
	f, folder := p.ask.flow, p.ask.folder
	p.ask = prompt{}
	p.Start(f, wf.Input{Brief: brief, Directory: folder})
}

// Start runs the flow on a worker and shows its page.
func (p *Plugin) Start(f wf.Flow, in wf.Input) {
	ctx, cancel := context.WithCancel(p.ctx.Lifetime())
	p.numbered++
	r := &run{number: p.numbered, flow: f, in: in, cancel: cancel, allowed: map[allowKey]bool{}, scope: p.ctx.NewScope()}
	if err := kernel.Contribute(r.scope, shell.Pages, pageID(r), 50, shell.Page{
		Title: func() string { return runTitle(r) }, Render: func(c *ui.Context) { p.page(c, r) }}); err != nil {
		cancel()
		r.scope.Dispose()
		p.host.Toast(ui.Toast{Title: "The workflow did not start", Description: err.Error(), Type: components.ToastError})
		return
	}
	p.runs = slices.Insert(p.runs, 0, r)
	for len(p.runs) > maxRuns {
		i := len(p.runs) - 1
		for i >= 0 && p.runs[i].state == runRunning {
			i--
		}
		if i < 0 {
			break
		}
		p.runs[i].scope.Dispose() // its page, and so its tab, goes
		p.runs = slices.Delete(p.runs, i, i+1)
	}
	p.show(r)
	runner := &runner{p: p, run: r}
	p.ctx.Spawn(func(context.Context) {
		result, err := call(ctx, f, runner)
		p.ctx.Post(func() { p.finish(r, result, err) })
	})
}

// onUI runs fn on the UI thread and waits for it, so the flow's goroutine can
// hand state to the engine in order. It gives up when ctx ends.
func (p *Plugin) onUI(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	p.ctx.Post(func() {
		defer close(done)
		fn()
	})
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drop stops watching a conversation whose step has ended or never began.
func (p *Plugin) drop(w *watch) {
	p.ctx.Post(func() {
		if p.watching[w.session] == w {
			delete(p.watching, w.session)
		}
	})
}

// stop asks a run to end. The flow's goroutine notices through its context,
// aborts the agent's turn and returns.
func (p *Plugin) stop(r *run) {
	if r.state != runRunning || r.stopping {
		return
	}
	r.stopping = true
	r.cancel()
}

// finish records how the flow ended and tells the user if they were away.
func (p *Plugin) finish(r *run, result string, err error) {
	r.cancel()
	switch {
	case r.stopping || errors.Is(err, wf.ErrStopped):
		r.state = runStopped
	case err != nil:
		r.state, r.err = runFailed, err.Error()
	default:
		r.state, r.result = runDone, result
	}
	for _, st := range r.steps {
		st.pending, st.retry = nil, nil
		if st.state == stepRunning {
			st.state = stepStopped
		}
		if w := st.watch; w != nil && p.watching[w.session] == w {
			delete(p.watching, w.session)
		}
		st.watch = nil
	}
	if r.state == runStopped || p.host.Page() == pageID(r) {
		return
	}
	r.unseen = true
	title, body, kind := r.flow.Title, "Finished.", components.ToastSuccess
	if r.state == runFailed {
		body, kind = "Failed: "+r.err, components.ToastError
	}
	if p.host.Foreground() {
		p.host.Toast(ui.Toast{Title: title, Description: body, Type: kind})
		return
	}
	p.host.Notify(shell.Notice{ID: noticeID, Title: title, Body: body})
}

// show opens a run's page.
func (p *Plugin) show(r *run) {
	r.unseen = false
	p.host.ShowPage(pageID(r))
}

func pageID(r *run) string { return pagePrefix + strconv.Itoa(r.number) }

// runTitle names a run's page and tab: its brief's first line, shortened.
func runTitle(r *run) string { return cmp.Or(clip(r.in.Brief, 40), r.flow.Title) }

// showLatest opens the page of the run that most needs the user, else of
// the newest.
func (p *Plugin) showLatest() {
	if len(p.runs) == 0 {
		return
	}
	pick := p.runs[0]
	for _, r := range p.runs {
		if attention(r) {
			pick = r
			break
		}
	}
	p.show(pick)
}

// attention reports a run that waits for the user, or ended unseen.
func attention(r *run) bool {
	return r.unseen || (r.state == runRunning && slices.ContainsFunc(r.steps, (*step).needsYou))
}

// ---- The agent's events

// events follows the turns of the steps' conversations. Conversations the
// engine does not watch are none of its business.
func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		w := p.watching[ev.Session]
		if w == nil {
			continue
		}
		switch ev.Kind {
		case agent.TextEnded:
			if ev.Text != nil {
				w.last = ev.Text.Text
			}
		case agent.ToolStarted:
			w.step.tools++
			if ev.Tool != nil {
				w.step.activity = working(ev.Tool.Kind, ev.Tool.Name)
			}
		case agent.ApprovalRequested:
			if ev.Approval != nil {
				p.approval(w, *ev.Approval)
			}
		case agent.InteractionRequested:
			if ev.Interaction != nil {
				p.question(w, *ev.Interaction)
			}
		case agent.TurnSucceeded:
			p.end(w, nil, true)
		case agent.TurnFailed:
			p.end(w, errors.New(cmp.Or(ev.Error, "the agent's turn failed")), true)
		case agent.TurnInterrupted:
			p.end(w, errors.New("the agent's turn was interrupted"), true)
		case agent.TextStarted, agent.TextDelta, agent.ToolCalled, agent.ToolFinished,
			agent.TurnStarted, agent.ModelChanged, agent.SessionRenamed:
		}
	}
}

// end wakes the flow's goroutine: the step's attempt is over, with err when it
// did not succeed. over says the backend's turn is over too.
func (p *Plugin) end(w *watch, err error, over bool) {
	if w.ended {
		return
	}
	w.ended, w.err, w.over = true, err, over
	if p.watching[w.session] == w {
		delete(p.watching, w.session)
	}
	w.step.pending = nil
	close(w.done)
	if err == nil {
		p.readChanges(w.step, w.session)
	}
}

// readChanges reads what a step's conversation changed, when the backend can
// tell, for the run's page.
func (p *Plugin) readChanges(st *step, session agent.SessionID) {
	source, ok := kernel.Use(p.ctx, agent.ChangesKey)
	if !ok {
		return
	}
	p.ctx.Spawn(func(ctx context.Context) {
		if changes, err := source.Changes(ctx, session); err == nil {
			p.ctx.Post(func() { st.changes = changes })
		}
	})
}

// approval answers a permission request by the step's policy, then by what the
// user allowed for the whole run; otherwise it waits on the page.
func (p *Plugin) approval(w *watch, a agent.Approval) {
	switch {
	case slices.Contains(w.policy.Deny, a.Kind):
		p.reply(w, a, agent.Reject)
	case slices.Contains(w.policy.Allow, a.Kind), w.run.allowed[keyOf(a)]:
		p.reply(w, a, agent.Once)
	default:
		w.step.pending = append(w.step.pending, a)
	}
}

// decide is the user's answer to a waiting permission request. forRun also
// allows every request of its kind for the rest of the run, those waiting now
// included.
func (p *Plugin) decide(st *step, a agent.Approval, d agent.Decision, forRun bool) {
	w := st.watch
	if w == nil {
		return
	}
	st.pending = slices.DeleteFunc(st.pending, func(x agent.Approval) bool { return x.ID == a.ID })
	p.reply(w, a, d)
	if !forRun {
		return
	}
	w.run.allowed[keyOf(a)] = true
	for _, other := range w.run.steps {
		if other.watch == nil {
			continue
		}
		waiting := slices.DeleteFunc(slices.Clone(other.pending), func(x agent.Approval) bool { return keyOf(x) != keyOf(a) })
		for _, x := range waiting {
			other.pending = slices.DeleteFunc(other.pending, func(y agent.Approval) bool { return y.ID == x.ID })
			p.reply(other.watch, x, agent.Once)
		}
	}
}

// reply delivers an answer to the backend. If it can't be delivered the agent
// may be waiting still, so the attempt fails and the user decides.
func (p *Plugin) reply(w *watch, a agent.Approval, d agent.Decision) {
	p.ctx.Spawn(func(ctx context.Context) {
		err := p.agent.Reply(ctx, agent.Reply{Session: w.session, Approval: a.ID, Decision: d})
		if err != nil {
			p.ctx.Post(func() { p.end(w, fmt.Errorf("could not answer the agent's permission request: %w", err), false) })
		}
	})
}

// question dismisses a question the agent asks in a conversation nobody is
// reading, and says so on the step. A backend that can't take the dismissal
// fails the attempt instead of leaving the agent waiting.
func (p *Plugin) question(w *watch, in agent.Interaction) {
	asked := cmp.Or(in.Title, in.Message, "a question")
	answers, ok := kernel.Use(p.ctx, agent.InteractionsKey)
	if !ok {
		p.end(w, fmt.Errorf("the agent asked %q, and this backend can't take an answer", asked), false)
		return
	}
	w.step.activity = "Skipped: " + asked
	p.ctx.Spawn(func(ctx context.Context) {
		err := answers.Answer(ctx, agent.InteractionAnswer{Session: w.session, ID: in.ID, Cancelled: true})
		if err != nil {
			p.ctx.Post(func() { p.end(w, fmt.Errorf("could not dismiss the agent's question: %w", err), false) })
		}
	})
}

// ---- Steps in the chat

// openStep shows a step's conversation in the chat. The first time, it also
// enters the conversation in the app's record, so All chats lists it from
// then on.
func (p *Plugin) openStep(r *run, st *step) {
	ctl, ok := kernel.Use(p.ctx, chat.ControllerKey)
	if !ok || st.session == "" {
		return
	}
	title := r.flow.Title + " · " + st.name
	if !st.adopted {
		st.adopted = true
		kernel.Emit(p.ctx, chat.Started, chat.Conversation{Session: st.session, Title: title, Directory: r.in.Directory})
	}
	ctl.Open(agent.Session{ID: st.session, Title: title, Directory: r.in.Directory})
	p.host.ShowPage(chat.PageID)
}

func (p *Plugin) retry(st *step) {
	if st.retry != nil {
		st.retry <- struct{}{}
		st.retry = nil
	}
}

// folderName words a run's folder for the page.
func folderName(dir string) string {
	if dir == "" {
		return "the agent's default folder"
	}
	return filepath.Clean(dir)
}
