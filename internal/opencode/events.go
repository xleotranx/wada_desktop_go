package opencode

import (
	"context"
	"encoding/json"
	"fmt"

	"wada-desktop-go/internal/agent"
)

// TextData is the payload of the session.text.* events: one running text
// block inside an assistant message, addressed by message and ordinal.
type TextData struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"assistantMessageID"`
	Ordinal   int    `json:"ordinal"`
	Delta     string `json:"delta"`
	Text      string `json:"text"`
}

// ToolData is the payload of the session.tool.* events: one tool call,
// addressed by its id, whose name, input and output fill in progressively.
type ToolData struct {
	Command   string          `json:"-"`
	SessionID string          `json:"sessionID"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   []ToolOutput    `json:"content"`
	Metadata  struct {
		Status    string `json:"status"`
		Truncated bool   `json:"truncated"`
		Exit      int    `json:"exit"`
	} `json:"metadata"`
}

// PermissionData is the payload of permission.asked: one approval the
// agent needs before acting, with what it wants and what "always" saves.
type PermissionData struct {
	SessionID string   `json:"sessionID"`
	ID        string   `json:"id"`
	Action    string   `json:"action"`
	Resources []string `json:"resources"`
	Save      []string `json:"save"`
}

type EventKind string

const (
	TextStarted          EventKind = "session.text.started"
	TextDelta            EventKind = "session.text.delta"
	TextEnded            EventKind = "session.text.ended"
	ToolStarted          EventKind = "session.tool.input.started"
	ToolCalled           EventKind = "session.tool.called"
	ToolSucceeded        EventKind = "session.tool.success"
	PermissionAsked      EventKind = "permission.asked"
	ExecutionStarted     EventKind = "session.execution.started"
	ExecutionSucceeded   EventKind = "session.execution.succeeded"
	ExecutionFailed      EventKind = "session.execution.failed"
	ExecutionInterrupted EventKind = "session.execution.interrupted"
	ModelSelected        EventKind = "session.model.selected"
	SessionRenamed       EventKind = "session.renamed"
)

// Update is a decoded known event. Wire data never enters the chat model.
type decodedUpdate struct {
	Kind       EventKind
	SessionID  string
	Text       *TextData
	Tool       *ToolData
	Permission *PermissionData
	Model      agent.ModelRef
	Title      string
	Bytes      int
}

// DecodeEvent ignores unknown event names, but rejects malformed known events
// without including remote payloads or credentials in the diagnostic.
func DecodeEvent(ev Event) (agent.Update, bool, error) {
	u := decodedUpdate{Kind: EventKind(ev.Type), Bytes: len(ev.Data)}
	var err error
	switch u.Kind {
	case TextStarted, TextDelta, TextEnded:
		payload := new(TextData)
		err = json.Unmarshal(ev.Data, payload)
		u.Text = payload
		u.SessionID = u.Text.SessionID
		if u.Text.MessageID == "" || u.Text.Ordinal < 0 {
			err = fmt.Errorf("invalid text identity")
		}
	case ToolStarted, ToolCalled, ToolSucceeded:
		payload := new(ToolData)
		err = json.Unmarshal(ev.Data, payload)
		u.Tool = payload
		u.SessionID = u.Tool.SessionID
		if u.Tool.ID == "" {
			err = fmt.Errorf("invalid tool identity")
		}
		if err == nil && len(u.Tool.Input) > 0 {
			var input struct {
				Command string `json:"command"`
			}
			err = json.Unmarshal(u.Tool.Input, &input)
			u.Tool.Command = input.Command
			u.Tool.Input = nil
		}
	case PermissionAsked:
		payload := new(PermissionData)
		err = json.Unmarshal(ev.Data, payload)
		u.Permission = payload
		u.SessionID = u.Permission.SessionID
		if u.Permission.ID == "" || u.Permission.Action == "" {
			err = fmt.Errorf("invalid permission identity")
		}
	case ExecutionStarted, ExecutionSucceeded, ExecutionFailed, ExecutionInterrupted, ModelSelected, SessionRenamed:
		var data struct {
			SessionID string         `json:"sessionID"`
			Model     agent.ModelRef `json:"model"`
			Title     string         `json:"title"`
		}
		err = json.Unmarshal(ev.Data, &data)
		u.SessionID = data.SessionID
		u.Model = data.Model
		u.Title = data.Title
		if u.Kind == ModelSelected && (u.Model.ID == "" || u.Model.ProviderID == "") {
			err = fmt.Errorf("invalid model identity")
		}
	default:
		return agent.Update{}, false, nil
	}
	if err != nil || u.SessionID == "" {
		return agent.Update{}, true, fmt.Errorf("malformed %s event", ev.Type)
	}
	return u.normalized(), true, nil
}

var eventKinds = map[EventKind]agent.EventKind{
	TextStarted:          agent.TextStarted,
	TextDelta:            agent.TextDelta,
	TextEnded:            agent.TextEnded,
	ToolStarted:          agent.ToolStarted,
	ToolCalled:           agent.ToolCalled,
	ToolSucceeded:        agent.ToolSucceeded,
	PermissionAsked:      agent.PermissionAsked,
	ExecutionStarted:     agent.ExecutionStarted,
	ExecutionSucceeded:   agent.ExecutionSucceeded,
	ExecutionFailed:      agent.ExecutionFailed,
	ExecutionInterrupted: agent.ExecutionInterrupted,
	ModelSelected:        agent.ModelSelected,
	SessionRenamed:       agent.SessionRenamed,
}

func (u decodedUpdate) normalized() agent.Update {
	out := agent.Update{Kind: eventKinds[u.Kind], SessionID: u.SessionID, Model: u.Model, Title: u.Title, Bytes: u.Bytes}
	if t := u.Text; t != nil {
		// The DTO differs only in JSON tags, so normalization needs no second payload.
		out.Text = (*agent.TextData)(t)
	}
	if t := u.Tool; t != nil {
		out.Tool = &agent.ToolData{SessionID: t.SessionID, ID: t.ID, Name: t.Name, Command: t.Command, Content: normalizeOutput(t.Content), Status: t.Metadata.Status, Exit: t.Metadata.Exit, Truncated: t.Metadata.Truncated}
	}
	if p := u.Permission; p != nil {
		out.Permission = (*agent.PermissionData)(p)
	}
	return out
}

// Updates validates and normalizes wire events before they cross the adapter.
// One diagnostic per connection attempt bounds malformed-stream noise.
func (a *Client) Updates(ctx context.Context, emit func(agent.Update), notice func(string)) error {
	warned := false
	return a.Events(ctx, func(wire Event) {
		update, known, err := DecodeEvent(wire)
		if err != nil {
			if !warned {
				warned = true
				notice(err.Error())
			}
			return
		}
		if known {
			emit(update)
		}
	})
}
