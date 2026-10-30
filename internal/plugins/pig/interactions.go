package pig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"wada-desktop-go/internal/contract/agent"
)

type approvalWait struct {
	session agent.SessionID
	done    chan agent.Decision
}
type interactionWait struct {
	session agent.SessionID
	request agent.Interaction
	done    chan agent.InteractionAnswer
}

func (e *engine) nextID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sequence++
	return fmt.Sprintf("request-%d", e.sequence)
}
func (e *engine) toolHook(c *conversation) pa.BeforeToolCallHook {
	return func(ctx context.Context, id, name string, args json.RawMessage) pa.ToolCallHookResult {
		c.turnMu.Lock()
		turn := c.turn
		submitted := turn != nil && len(turn.result) > 0
		var policy *agent.ToolPolicy
		if turn != nil {
			policy = turn.policy
		}
		structured := turn != nil && len(turn.schema) > 0
		requestID := ""
		if turn != nil {
			requestID = turn.id
		}
		c.turnMu.Unlock()
		if submitted {
			return pa.ToolCallHookResult{Block: true, Reason: "The turn result has already been submitted."}
		}
		if name == resultToolName && structured {
			return pa.ToolCallHookResult{}
		}
		kind := toolKind(name)
		key := actionKey(name, args)
		c.mu.Lock()
		choice := permissionFor(c.rec.Mode, c.rec.Policy, kind)
		if policy != nil {
			restricted := permissionFor(c.rec.Mode, policy, kind)
			if restricted == deny || choice == permit {
				choice = restricted
			}
		}
		allowed := c.allowed[key]
		sid := c.rec.Session.ID
		c.mu.Unlock()
		if choice == deny {
			return pa.ToolCallHookResult{Block: true, Reason: "This tool is denied by the workflow policy."}
		}
		if choice == ask && (!allowed || policy != nil) {
			aid := agent.ApprovalID(e.nextID())
			wait := &approvalWait{session: sid, done: make(chan agent.Decision, 1)}
			e.mu.Lock()
			e.approvals[aid] = wait
			e.mu.Unlock()
			defer func() { e.mu.Lock(); delete(e.approvals, aid); e.mu.Unlock() }()
			e.emit(agent.Event{Kind: agent.ApprovalRequested, Session: sid, RequestID: requestID, Approval: &agent.Approval{ID: aid, Kind: kind, Action: name, Resources: []string{string(args)}, Save: []string{key}}})
			var decision agent.Decision
			select {
			case decision = <-wait.done:
			case <-ctx.Done():
				return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
			case <-e.ctx.Done():
				return pa.ToolCallHookResult{Block: true, Reason: "The agent stopped."}
			}
			if decision == agent.Reject {
				return pa.ToolCallHookResult{Block: true, Reason: "The user rejected this action."}
			}
			if decision == agent.Always {
				c.mu.Lock()
				c.allowed[key] = true
				c.mu.Unlock()
			}
		}
		if err := ctx.Err(); err != nil {
			return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
		}
		if kind == agent.ToolEdit || kind == agent.ToolShell {
			if err := e.beginMutation(ctx, c, id, name, args); err != nil {
				if ctx.Err() != nil || e.ctx.Err() != nil {
					return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
				}
				// Tracking is observational: an unavailable snapshot must not revoke an
				// action the user or fixed workflow policy has already allowed.
				c.mu.Lock()
				c.rec.ChangesError = "Session changes are unavailable: " + err.Error()
				_ = e.saveLocked(c)
				c.mu.Unlock()
			}
		}
		return pa.ToolCallHookResult{}
	}
}
func (p *Plugin) Reply(ctx context.Context, req agent.Reply) error {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	switch req.Decision {
	case agent.Once, agent.Always, agent.Reject:
	default:
		return errors.New("invalid approval decision")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	w := e.approvals[req.Approval]
	if w == nil || w.session != req.Session {
		return errors.New("this permission request is no longer pending")
	}
	select {
	case w.done <- req.Decision:
		delete(e.approvals, req.Approval)
		return nil
	default:
		return errors.New("this permission request was already answered")
	}
}
func (p *Plugin) Pending(ctx context.Context, id agent.SessionID) ([]agent.Interaction, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []agent.Interaction{}
	for _, w := range e.dialogs {
		if w.session == id {
			in := w.request
			in.Options = slices.Clone(in.Options)
			out = append(out, in)
		}
	}
	return out, nil
}
func (p *Plugin) Answer(ctx context.Context, req agent.InteractionAnswer) error {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	w := e.dialogs[req.ID]
	if w == nil || w.session != req.Session {
		return errors.New("this question is no longer pending")
	}
	if w.request.Kind == agent.Select && !req.Cancelled && !slices.Contains(w.request.Options, req.Value) {
		return errors.New("choose one of the offered answers")
	}
	select {
	case w.done <- req:
		delete(e.dialogs, req.ID)
		return nil
	default:
		return errors.New("this question was already answered")
	}
}
func (e *engine) question(ctx context.Context, c *conversation, in agent.Interaction) (agent.InteractionAnswer, error) {
	if err := ctx.Err(); err != nil {
		return agent.InteractionAnswer{}, err
	}
	c.mu.Lock()
	sid := c.rec.Session.ID
	c.pending++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pending--
		c.mu.Unlock()
		// A suspended extension dialog can outlive its service call. Reclaim
		// its idle owner from another worker: SDK Close joins this callback.
		e.spawn(func(context.Context) { e.trimIdle() })
	}()
	c.turnMu.Lock()
	requestID := ""
	if c.turn != nil {
		requestID = c.turn.id
	}
	c.turnMu.Unlock()
	in.ID = agent.InteractionID(e.nextID())
	w := &interactionWait{session: sid, request: in, done: make(chan agent.InteractionAnswer, 1)}
	e.mu.Lock()
	e.dialogs[in.ID] = w
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.dialogs, in.ID)
		e.mu.Unlock()
		settled := in
		settled.Settled = true
		e.emit(agent.Event{Kind: agent.InteractionRequested, Session: sid, RequestID: requestID, Interaction: &settled})
	}()
	e.emit(agent.Event{Kind: agent.InteractionRequested, Session: sid, RequestID: requestID, Interaction: &in})
	select {
	case answer := <-w.done:
		if answer.Cancelled {
			return answer, context.Canceled
		}
		return answer, nil
	case <-ctx.Done():
		return agent.InteractionAnswer{}, ctx.Err()
	case <-e.ctx.Done():
		return agent.InteractionAnswer{}, agent.ErrNotConnected
	}
}
func noopUI() extension.UIContext { return extension.NoopUIContext }

type dialogUI struct {
	extension.UIContext
	engine       *engine
	conversation *conversation
}

func (d *dialogUI) Confirm(ctx context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	a, e := d.engine.question(ctx, d.conversation, agent.Interaction{Kind: agent.Confirm, Title: title, Message: message})
	return a.Confirmed, e
}
func (d *dialogUI) Select(ctx context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, agent.Interaction{Kind: agent.Select, Title: title, Options: slices.Clone(options)})
	return a.Value, e
}
func (d *dialogUI) Input(ctx context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, agent.Interaction{Kind: agent.Input, Title: title, Placeholder: placeholder})
	return a.Value, e
}
func (d *dialogUI) Editor(ctx context.Context, title, prefill string) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, agent.Interaction{Kind: agent.Editor, Title: title, Prefill: prefill})
	return a.Value, e
}
