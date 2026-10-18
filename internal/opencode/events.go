package opencode

import (
	"encoding/json"
	"fmt"
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
type Update struct {
	Kind       EventKind
	SessionID  string
	Text       TextData
	Tool       ToolData
	Permission PermissionData
	Model      ModelRef
	Title      string
	Bytes      int
}

// DecodeEvent ignores unknown event names, but rejects malformed known events
// without including remote payloads or credentials in the diagnostic.
func DecodeEvent(ev Event) (Update, bool, error) {
	u := Update{Kind: EventKind(ev.Type), Bytes: len(ev.Data)}
	var err error
	switch u.Kind {
	case TextStarted, TextDelta, TextEnded:
		err = json.Unmarshal(ev.Data, &u.Text)
		u.SessionID = u.Text.SessionID
		if u.Text.MessageID == "" || u.Text.Ordinal < 0 {
			err = fmt.Errorf("invalid text identity")
		}
	case ToolStarted, ToolCalled, ToolSucceeded:
		err = json.Unmarshal(ev.Data, &u.Tool)
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
		}
	case PermissionAsked:
		err = json.Unmarshal(ev.Data, &u.Permission)
		u.SessionID = u.Permission.SessionID
		if u.Permission.ID == "" || u.Permission.Action == "" {
			err = fmt.Errorf("invalid permission identity")
		}
	case ExecutionStarted, ExecutionSucceeded, ExecutionFailed, ExecutionInterrupted, ModelSelected, SessionRenamed:
		var data struct {
			SessionID string   `json:"sessionID"`
			Model     ModelRef `json:"model"`
			Title     string   `json:"title"`
		}
		err = json.Unmarshal(ev.Data, &data)
		u.SessionID = data.SessionID
		u.Model = data.Model
		u.Title = data.Title
		if u.Kind == ModelSelected && (u.Model.ID == "" || u.Model.ProviderID == "") {
			err = fmt.Errorf("invalid model identity")
		}
	default:
		return Update{}, false, nil
	}
	if err != nil || u.SessionID == "" {
		return Update{}, true, fmt.Errorf("malformed %s event", ev.Type)
	}
	return u, true, nil
}
