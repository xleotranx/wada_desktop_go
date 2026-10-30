package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
)

type finding struct {
	File     string `json:"file"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
}
type phaseResult struct {
	Status   string    `json:"status"`
	Action   string    `json:"action"`
	Summary  string    `json:"summary"`
	Findings []finding `json:"findings"`
}
type task struct {
	Initial                                                     chat.Draft
	References                                                  []agent.File
	ID, Flow, Brief, Plan, Code, Phase, State, Outcome, Summary string
	Model                                                       agent.ModelRef
	Mode                                                        string
	Anchor, Round                                               int
	Baseline                                                    agent.Snapshot
	Findings                                                    []finding
	Reports                                                     []phaseResult
}
type operation struct {
	TaskID, Phase string
	Request       agent.Send
	Draft         chat.Draft
}
type savedLane struct {
	Origin                     string
	Session                    agent.SessionID
	Directory                  string
	Current, Associated, Error string
	Halted                     bool
	Queue                      []chat.Draft
	Tasks                      []*task
	Pending                    *operation
}
type lane struct {
	abort *atomic.Pointer[agent.SessionID]
	savedLane
	scope             *kernel.Scope
	job               kernel.Task
	Working, stopping bool
	requests          []agent.Event
}

func (l *lane) task() *task { return l.find(l.Current) }
func (l *lane) find(id string) *task {
	for _, t := range l.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
func (l *lane) runningTask() bool { t := l.task(); return t != nil && t.State == "running" }

// saved copies mutable container headers. Large baseline/file buffers are immutable.
func (l *lane) saved() savedLane {
	s := l.savedLane
	s.Queue = slices.Clone(l.Queue)
	s.Tasks = nil
	for _, t := range l.Tasks {
		copy := *t
		copy.Findings = slices.Clone(t.Findings)
		copy.Reports = slices.Clone(t.Reports)
		s.Tasks = append(s.Tasks, &copy)
	}
	if l.Pending != nil {
		copy := *l.Pending
		s.Pending = &copy
	}
	return s
}

var readOnly = agent.ToolPolicy{Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch}, Deny: []agent.ToolKind{agent.ToolEdit}}
var readySchema = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["ready","blocked"]},"summary":{"type":"string"}},"required":["status","summary"],"additionalProperties":false}`)
var routeSchema = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["answer","continue","new_task","resume"]},"summary":{"type":"string"}},"required":["action","summary"],"additionalProperties":false}`)
var reviewSchema = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["pass","issues","incomplete"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"severity":{"type":"string","enum":["blocker","major"]},"evidence":{"type":"string"}},"required":["file","severity","evidence"],"additionalProperties":false}}},"required":["status","summary","findings"],"additionalProperties":false}`)

func (p *Plugin) advance(l *lane) {
	if l.Working || l.Halted || l.stopping {
		return
	}
	if len(l.Queue) > 0 {
		d := l.Queue[0]
		l.Queue = slices.Clone(l.Queue[1:])
		if d.Workflow != "" {
			if old := l.task(); old != nil && old.State == "running" {
				old.State = "paused"
			}
			t := &task{ID: p.newID(), Flow: d.Workflow, Brief: d.Text, Model: d.Model, Mode: d.Mode, Anchor: d.Anchor, Phase: "plan", State: "running", Initial: d, References: d.Files}
			l.Tasks = append(slices.Clone(l.Tasks), t)
			l.Current = t.ID
			l.Associated = t.ID
			p.execute(l, &operation{TaskID: t.ID, Phase: "plan", Draft: d})
			return
		}
		if t := l.find(l.Associated); t != nil {
			f, ok := p.flow(t.Flow)
			if !ok {
				p.fail(l, errors.New("the workflow definition is unavailable"))
				return
			}
			req := agent.Send{Session: l.Session, Mode: d.Mode, Text: d.Text, Files: d.Files, Skills: d.Skills, Instructions: f.Route + "\nReviewed task:\n" + t.Brief, Policy: &readOnly, ResultSchema: routeSchema}
			p.execute(l, &operation{TaskID: t.ID, Phase: "route", Draft: d, Request: req})
			return
		}
		p.execute(l, &operation{Phase: "ordinary", Draft: d, Request: agent.Send{Session: l.Session, Mode: d.Mode, Text: d.Text, Files: d.Files, Skills: d.Skills}})
		return
	}
	t := l.task()
	if t == nil || t.State != "running" {
		p.save(l)
		return
	}
	p.execute(l, &operation{TaskID: t.ID, Phase: t.Phase})
}
func (p *Plugin) prepare(t *task, op *operation, f wf.Flow, l *lane) {
	if op.Request.Session != "" {
		return
	}
	req := agent.Send{Session: l.Session, Mode: t.Mode, Internal: true, ResultSchema: readySchema}
	req.Text = "Continue the task:\n" + t.Brief
	switch op.Phase {
	case "plan":
		if op.Draft.Text == "" {
			op.Draft = t.Initial
		}
		req.Text = op.Draft.Text
		req.Files = op.Draft.Files
		req.Skills = op.Draft.Skills
		req.Internal = false
		req.Policy = &readOnly
		req.Instructions = f.Plan
	case "code":
		req.Instructions = f.Code
		req.Text += "\nPlan:\n" + t.Plan
	case "fix":
		req.Instructions = f.Fix
		data, _ := json.Marshal(t.Findings)
		req.Text += "\nReview findings (assess independently):\n" + string(data)
	case "review":
		req.Session = ""
		req.Files = t.References
		req.Internal = false
		req.Policy = &readOnly
		req.ResultSchema = reviewSchema
		req.Instructions = f.Review
		req.Text = "Requirements:\n" + t.Brief + "\nPlan:\n" + t.Plan + "\nImplementation and checks:\n" + t.Code
		data, _ := json.Marshal(t.Findings)
		req.Text += "\nPrevious findings:\n" + string(data)
	case "final":
		req.Policy = &readOnly
		req.Instructions = f.Final
		req.Text += "\nOutcome: " + t.Outcome + "\nImplementation and checks:\n" + t.Code + "\nReview:\n" + t.Summary
		data, _ := json.Marshal(t.Findings)
		req.Text += "\nUnresolved findings:\n" + string(data)
	}
	op.Request = req
}
func (p *Plugin) execute(l *lane, op *operation) {
	t := l.find(op.TaskID)
	if t != nil {
		f, ok := p.flow(t.Flow)
		if !ok {
			p.fail(l, errors.New("the workflow definition is unavailable"))
			return
		}
		p.prepare(t, op, f, l)
	}
	op.Request.ID = p.newID()
	l.Pending = op
	l.Working = true
	l.Error = ""
	if t != nil && op.Phase != "route" {
		t.State = "running"
	}
	p.save(l)
	// Capture/spawn/diff are completed and saved before the send is admitted.
	type preparation struct {
		Baseline agent.Snapshot
		Child    agent.Session
		Changes  []agent.FileChange
	}
	baselineNeeded := t != nil && t.Baseline == nil && op.Phase != "route"
	baseline := agent.Snapshot(nil)
	model := agent.ModelRef{}
	round := 0
	if t != nil {
		baseline = t.Baseline
		model = t.Model
		round = t.Round
	}
	request := op.Request
	snapshots, children := p.snapshots, p.children
	dir, parent := l.Directory, l.Session
	kernel.Go(l.scope, &l.job, func(ctx context.Context) (preparation, error) {
		var out preparation
		if baselineNeeded {
			var err error
			out.Baseline, err = snapshots.Snapshot(ctx, parent)
			if err != nil {
				return out, err
			}
		}
		if op.Phase == "review" {
			var err error
			out.Changes, err = snapshots.Diff(ctx, parent, baseline)
			if err != nil {
				return out, err
			}
			if request.Session == "" {
				out.Child, err = children.Spawn(ctx, agent.Spawn{Parent: parent, Name: fmt.Sprintf("Reviewer %d", round+1), Directory: dir, Model: model, Policy: readOnly})
				if err != nil {
					return out, err
				}
			}
		}
		return out, nil
	}, func(out preparation, err error) {
		if l.Pending != op || !l.Working {
			return
		}
		if out.Baseline != nil && t != nil {
			t.Baseline = out.Baseline
		}
		if out.Child.ID != "" {
			op.Request.Session = out.Child.ID
		}
		if err != nil {
			p.fail(l, fmt.Errorf("could not prepare %s: %w", op.Phase, err))
			return
		}
		if op.Phase == "review" {
			if op.Request.Session == "" {
				p.fail(l, errors.New("the reviewer was created without an ID"))
				return
			}
			data, _ := json.Marshal(out.Changes)
			// Rebuild this part on explicit retry rather than appending duplicate patches.
			op.Request.Text = strings.Split(op.Request.Text, "\nTask-start changes:\n")[0] + "\nTask-start changes:\n" + string(data)
		}
		p.save(l)
		p.send(l, op)
	})
}
func (p *Plugin) send(l *lane, op *operation) {
	req := op.Request
	l.abort.Store(&req.Session)
	p.watching[req.Session] = l
	if !req.Internal && req.Session == l.Session && op.Draft.Text != "" {
		d := op.Draft
		d.Session = l.Session
		kernel.Emit(p.ctx, chat.UserAdmitted, d)
	}
	backend := p.agent
	kernel.Go(l.scope, &l.job, func(ctx context.Context) (agent.SendDisposition, error) { return backend.Send(ctx, req) }, func(disposition agent.SendDisposition, err error) {
		if l.Pending != op || !l.Working {
			return
		}
		if err != nil {
			if !errors.Is(err, agent.ErrNotSent) {
				p.abortUnknown(l, op, err)
				return
			}
			p.fail(l, err)
			return
		}
		if disposition == agent.SendHandled {
			if op.Phase == "ordinary" {
				l.Working = false
				l.Pending = nil
				p.save(l)
				p.advance(l)
			} else {
				p.fail(l, errors.New("the workflow prompt was consumed as a command"))
			}
		}
	})
}
func (p *Plugin) abortUnknown(l *lane, op *operation, cause error) {
	backend, id := p.agent, op.Request.Session
	kernel.Go(l.scope, &l.job, func(ctx context.Context) (struct{}, error) { return struct{}{}, backend.Abort(ctx, id) }, func(_ struct{}, err error) {
		if l.Pending != op {
			return
		}
		message := "The prompt outcome is unknown: " + cause.Error() + ". Check the conversation before Retry."
		if err != nil {
			message += " The agent may still be running: " + err.Error()
		}
		l.abort.Store(nil)
		p.fail(l, errors.New(message))
	})
}
func decodeResult(op *operation, raw json.RawMessage) (phaseResult, error) {
	var r phaseResult
	if len(raw) == 0 {
		return r, errors.New("the agent returned no structured result")
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("invalid structured result: %w", err)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return r, errors.New("the structured result has no summary")
	}
	switch op.Phase {
	case "route":
		if r.Action != "answer" && r.Action != "continue" && r.Action != "new_task" && r.Action != "resume" {
			return r, errors.New("invalid routing action")
		}
	case "review":
		if r.Status != "pass" && r.Status != "issues" && r.Status != "incomplete" {
			return r, errors.New("invalid review status")
		}
		if r.Status == "pass" && len(r.Findings) > 0 || r.Status == "issues" && len(r.Findings) == 0 {
			return r, errors.New("review status contradicts its findings")
		}
		for _, f := range r.Findings {
			if f.File == "" || f.Evidence == "" || (f.Severity != "major" && f.Severity != "blocker") {
				return r, errors.New("invalid review finding")
			}
		}
	default:
		if r.Status != "ready" && r.Status != "blocked" {
			return r, errors.New("invalid phase status")
		}
	}
	return r, nil
}
func (p *Plugin) completed(l *lane, op *operation, result phaseResult) {
	t := l.find(op.TaskID)
	l.Working = false
	if p.watching[op.Request.Session] == l {
		delete(p.watching, op.Request.Session)
	}
	l.abort.Store(nil)
	l.Pending = nil
	l.requests = nil
	if op.Phase == "ordinary" {
		p.save(l)
		p.advance(l)
		return
	}
	if op.Phase == "route" {
		switch result.Action {
		case "answer":
		case "continue":
			t.Brief += "\nFollow-up:\n" + op.Draft.Text
			t.References = append(slices.Clone(t.References), op.Draft.Files...)
			if !op.Draft.Model.IsZero() {
				t.Model = op.Draft.Model
			}
			t.Round = 0
			t.Outcome = ""
			t.Findings = nil
			if t.Phase != "plan" {
				t.Phase = "code"
			}
			t.State = "running"
			if op.Draft.Mode != "" {
				t.Mode = op.Draft.Mode
			}
		case "resume":
			t.State = "running"
			l.Associated = t.ID
		case "new_task":
			if t.State == "running" {
				t.State = "paused"
			}
			l.Associated = ""
			req := agent.Send{Session: l.Session, Mode: op.Draft.Mode, Text: op.Draft.Text, Files: op.Draft.Files, Skills: op.Draft.Skills, Internal: true, Instructions: "Handle this as an ordinary task. Earlier workflow instructions do not apply."}
			p.execute(l, &operation{Phase: "ordinary", Request: req, Draft: op.Draft})
			return
		}
		p.save(l)
		p.advance(l)
		return
	}
	t.Reports = append(slices.Clone(t.Reports), result)
	if result.Status == "blocked" {
		t.State = "paused"
		t.Summary = result.Summary
		p.save(l)
		p.advance(l)
		return
	}
	switch op.Phase {
	case "plan":
		t.Plan = result.Summary
		t.Phase = "code"
	case "code", "fix":
		t.Code = result.Summary
		t.Phase = "review"
	case "review":
		t.Round++
		t.Summary = result.Summary
		t.Findings = result.Findings
		f, _ := p.flow(t.Flow)
		switch {
		case result.Status == "pass":
			t.Outcome = "Passed"
			t.Phase = "final"
		case result.Status == "incomplete":
			t.Outcome = "Review incomplete"
			t.Phase = "final"
		case t.Round >= max(1, f.MaxReviews):
			t.Outcome = "Issues remain"
			t.Phase = "final"
		default:
			t.Phase = "fix"
		}
	case "final":
		t.State = "done"
	}
	p.save(l)
	p.advance(l)
}
func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		{
			l := p.watching[ev.Session]
			if l == nil {
				continue
			}
			op := l.Pending
			if !l.Working || op == nil || ev.Session != op.Request.Session || ev.RequestID != op.Request.ID {
				continue
			}
			switch ev.Kind {
			case agent.ApprovalRequested, agent.InteractionRequested:
				if ev.Session != l.Session {
					copy := ev
					if ev.Approval != nil {
						a := *ev.Approval
						a.Session = ev.Session
						copy.Approval = &a
					}
					if ev.Interaction != nil {
						q := *ev.Interaction
						q.Session = ev.Session
						copy.Interaction = &q
					}
					if ev.Interaction != nil && ev.Interaction.Settled {
						l.requests = slices.DeleteFunc(l.requests, func(e agent.Event) bool { return e.Interaction != nil && e.Interaction.ID == ev.Interaction.ID })
					} else {
						l.requests = append(l.requests, copy)
					}
					copy.Session = l.Session
					kernel.Emit(p.ctx, chat.Incoming, []agent.Event{copy})
				}
			case agent.TurnSucceeded:
				if ev.Session != l.Session {
					kernel.Emit(p.ctx, chat.Incoming, []agent.Event{{Kind: agent.TurnSucceeded, Session: l.Session}})
				}
				var r phaseResult
				var err error
				if op.Phase != "ordinary" {
					r, err = decodeResult(op, ev.Result)
				}
				if err != nil {
					p.fail(l, err)
				} else {
					p.completed(l, op, r)
				}
			case agent.TurnFailed, agent.TurnInterrupted:
				message := ev.Error
				if message == "" {
					message = "The turn was interrupted"
				}
				p.fail(l, errors.New(message))
			case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled, agent.ToolFinished, agent.TurnStarted, agent.ModelChanged, agent.SessionRenamed:
			}
		}
	}
}
