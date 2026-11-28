package pig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"wada-desktop-go/internal/protocol"
)

type approvalWait struct {
	session string
	request protocol.Approval
	done    chan protocol.Decision
}

type interactionWait struct {
	session string
	request protocol.Interaction
	done    chan protocol.InteractionAnswer
}

func (e *engine) nextID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sequence++
	return fmt.Sprintf("request-%d", e.sequence)
}

func (e *engine) toolHook(c *conversation) pa.BeforeToolCallHook {
	return func(ctx context.Context, id, name string, args json.RawMessage) pa.ToolCallHookResult {
		kind := toolKind(name)
		key := actionKey(name, args)
		c.mu.Lock()
		choice := permissionFor(c.rec.Mode, c.rec.Policy, kind)
		allowed := c.allowed[key]
		sid := c.rec.Session.ID
		c.mu.Unlock()
		if choice == deny {
			return pa.ToolCallHookResult{Block: true, Reason: "This tool is denied by the conversation policy."}
		}
		if choice == ask && !allowed {
			aid := e.nextID()
			approval := protocol.Approval{Session: sid, ID: aid, Kind: kind, Action: name, Resources: []string{string(args)}, Save: []string{key}}
			wait := &approvalWait{session: sid, request: approval, done: make(chan protocol.Decision, 1)}
			e.mu.Lock()
			e.approvals[aid] = wait
			e.mu.Unlock()
			defer func() { e.mu.Lock(); delete(e.approvals, aid); e.mu.Unlock() }()
			e.emit(protocol.Event{
				Kind:     protocol.EventApprovalRequested,
				Session:  sid,
				Approval: &approval,
			})
			var decision protocol.Decision
			select {
			case decision = <-wait.done:
			case <-ctx.Done():
				return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
			case <-e.ctx.Done():
				return pa.ToolCallHookResult{Block: true, Reason: "The agent stopped."}
			}
			if decision == protocol.DecisionReject {
				return pa.ToolCallHookResult{Block: true, Reason: "The user rejected this action."}
			}
			if decision == protocol.DecisionAlways {
				c.mu.Lock()
				c.allowed[key] = true
				c.mu.Unlock()
			}
		}
		if err := ctx.Err(); err != nil {
			return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
		}
		if kind == protocol.ToolEdit || kind == protocol.ToolShell {
			if err := e.beginMutation(ctx, c, id, name, args); err != nil {
				if ctx.Err() != nil || e.ctx.Err() != nil {
					return pa.ToolCallHookResult{Block: true, Reason: "The turn was stopped."}
				}
				c.mu.Lock()
				c.rec.ChangesError = "Session changes are unavailable: " + err.Error()
				_ = e.saveLocked(c)
				c.mu.Unlock()
			}
		}
		return pa.ToolCallHookResult{}
	}
}

func (r *Runtime) Reply(ctx context.Context, req protocol.ReplyRequest) error {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	switch req.Decision {
	case protocol.DecisionOnce, protocol.DecisionAlways, protocol.DecisionReject:
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

func (r *Runtime) PendingApprovals(ctx context.Context) ([]protocol.PendingApproval, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []protocol.PendingApproval{}
	for _, w := range e.approvals {
		ap := w.request
		ap.Resources = slices.Clone(ap.Resources)
		ap.Save = slices.Clone(ap.Save)
		out = append(out, protocol.PendingApproval{ShownIn: w.session, Approval: ap})
	}
	slices.SortFunc(out, func(a, b protocol.PendingApproval) int { return strings.Compare(a.Approval.ID, b.Approval.ID) })
	return out, nil
}

func (r *Runtime) PendingInteractions(ctx context.Context, id string) ([]protocol.Interaction, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []protocol.Interaction{}
	for _, w := range e.dialogs {
		if w.session == id {
			in := w.request
			in.Options = slices.Clone(in.Options)
			out = append(out, in)
		}
	}
	return out, nil
}

func (r *Runtime) AnswerInteraction(ctx context.Context, req protocol.InteractionAnswer) error {
	e, release, err := r.acquire()
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
	if w.request.Kind == protocol.InteractionSelect && !req.Cancelled && !slices.Contains(w.request.Options, req.Value) {
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

func (e *engine) question(ctx context.Context, c *conversation, in protocol.Interaction) (protocol.InteractionAnswer, error) {
	if err := ctx.Err(); err != nil {
		return protocol.InteractionAnswer{}, err
	}
	c.mu.Lock()
	sid := c.rec.Session.ID
	in.Session = sid
	c.pending++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pending--
		c.mu.Unlock()
		e.spawn(func(context.Context) { e.trimIdle() })
	}()
	in.ID = e.nextID()
	w := &interactionWait{session: sid, request: in, done: make(chan protocol.InteractionAnswer, 1)}
	e.mu.Lock()
	e.dialogs[in.ID] = w
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.dialogs, in.ID)
		e.mu.Unlock()
		settled := in
		settled.Settled = true
		e.emit(protocol.Event{Kind: protocol.EventInteractionRequested, Session: sid, Interaction: &settled})
	}()
	e.emit(protocol.Event{Kind: protocol.EventInteractionRequested, Session: sid, Interaction: &in})
	select {
	case answer := <-w.done:
		if answer.Cancelled {
			return answer, context.Canceled
		}
		return answer, nil
	case <-ctx.Done():
		return protocol.InteractionAnswer{}, ctx.Err()
	case <-e.ctx.Done():
		return protocol.InteractionAnswer{}, ErrNotConnected
	}
}

func noopUI() extension.UIContext { return extension.NoopUIContext }

type dialogUI struct {
	extension.UIContext
	engine       *engine
	conversation *conversation
}

func (d *dialogUI) Confirm(ctx context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	a, e := d.engine.question(ctx, d.conversation, protocol.Interaction{Kind: protocol.InteractionConfirm, Title: title, Message: message})
	return a.Confirmed, e
}
func (d *dialogUI) Select(ctx context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, protocol.Interaction{Kind: protocol.InteractionSelect, Title: title, Options: slices.Clone(options)})
	return a.Value, e
}
func (d *dialogUI) Input(ctx context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, protocol.Interaction{Kind: protocol.InteractionInput, Title: title, Placeholder: placeholder})
	return a.Value, e
}
func (d *dialogUI) Editor(ctx context.Context, title, prefill string) (string, error) {
	a, e := d.engine.question(ctx, d.conversation, protocol.Interaction{Kind: protocol.InteractionEditor, Title: title, Prefill: prefill})
	return a.Value, e
}
