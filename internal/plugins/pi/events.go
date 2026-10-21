package pi

import (
	"encoding/json"
	"strconv"
	"sync"

	"wada-desktop-go/internal/contract/agent"
)

// translator turns one session process's records into contract events. It
// keeps the little state Pi's stream needs: the assistant message being
// written and whether the run failed. It runs on that process's reader.
type translator struct {
	mu       sync.Mutex // the session is set by Create while the reader runs
	session  agent.SessionID
	messages int
	message  agent.MessageID
	running  bool
	failed   string // the last error of this turn, if it failed
}

type wireEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
	Update  struct {
		Type         string `json:"type"`
		ContentIndex int    `json:"contentIndex"`
		Delta        string `json:"delta"`
		Content      string `json:"content"`
		ID           string `json:"id"`
		ToolName     string `json:"toolName"`
	} `json:"assistantMessageEvent"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       map[string]any  `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
	Aborted    bool            `json:"aborted"`
	Name       *string         `json:"name"`
	Level      string          `json:"level"`
}

// dialogRequest is an extension_ui_request record.
type dialogRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Options     []string `json:"options"`
	Placeholder string   `json:"placeholder"`
	Prefill     string   `json:"prefill"`
}

// translate returns the events one record stands for. Records it does not
// show are ignored.
func (t *translator) translate(kind string, raw []byte) []agent.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	var w wireEvent
	if json.Unmarshal(raw, &w) != nil {
		return nil
	}
	ev := func(k agent.EventKind) agent.Event {
		return agent.Event{Kind: k, Session: t.session, Bytes: len(raw)}
	}
	switch kind {
	case "agent_start":
		if t.running {
			return nil // a retry or follow-up inside the same turn
		}
		t.running, t.failed = true, ""
		return []agent.Event{ev(agent.TurnStarted)}
	case "agent_settled":
		if !t.running {
			return nil
		}
		t.running = false
		switch {
		case w.Aborted:
			return []agent.Event{ev(agent.TurnInterrupted)}
		case t.failed != "":
			e := ev(agent.TurnFailed)
			e.Error = t.failed
			return []agent.Event{e}
		}
		return []agent.Event{ev(agent.TurnSucceeded)}
	case "message_start":
		var m message
		if json.Unmarshal(w.Message, &m) == nil && m.Role == "assistant" {
			t.messages++
			t.message = agent.MessageID(string(t.session) + "/live-" + strconv.Itoa(t.messages))
		}
	case "message_end":
		var m struct {
			Role         string `json:"role"`
			StopReason   string `json:"stopReason"`
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(w.Message, &m) == nil && m.Role == "assistant" {
			t.failed = "" // a later answer (after a retry) supersedes an earlier error
			if m.StopReason == "error" {
				t.failed = cmpOr(m.ErrorMessage, "the model returned an error")
			}
		}
	case "auto_retry_end":
		var r struct {
			Success    bool   `json:"success"`
			FinalError string `json:"finalError"`
		}
		if json.Unmarshal(raw, &r) == nil && !r.Success && r.FinalError != "" {
			t.failed = r.FinalError
		}
	case "message_update":
		text := func(k agent.EventKind) []agent.Event {
			e := ev(k)
			e.Text = &agent.TextEvent{Message: t.message, Ordinal: w.Update.ContentIndex, Delta: w.Update.Delta, Text: w.Update.Content}
			return []agent.Event{e}
		}
		switch w.Update.Type {
		case "text_start":
			return text(agent.TextStarted)
		case "text_delta":
			return text(agent.TextDelta)
		case "text_end":
			return text(agent.TextEnded)
		case "toolcall_start":
			e := ev(agent.ToolStarted)
			e.Tool = &agent.Tool{ID: w.Update.ID, Name: w.Update.ToolName, Kind: toolKind(w.Update.ToolName)}
			return []agent.Event{e}
		}
	case "tool_execution_start":
		e := ev(agent.ToolCalled)
		e.Tool = &agent.Tool{ID: w.ToolCallID, Name: w.ToolName, Kind: toolKind(w.ToolName), Command: commandOf(w.Args)}
		return []agent.Event{e}
	case "tool_execution_end":
		var result struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(w.Result, &result)
		e := ev(agent.ToolFinished)
		e.Tool = &agent.Tool{ID: w.ToolCallID, Name: w.ToolName, Kind: toolKind(w.ToolName), Command: commandOf(w.Args),
			Output: textPieces(result.Content), Status: "completed"}
		if w.IsError {
			e.Tool.Status, e.Tool.Exit = "error", 1
		}
		return []agent.Event{e}
	case "session_info_changed":
		e := ev(agent.SessionRenamed)
		if w.Name != nil {
			e.Title = *w.Name
		}
		return []agent.Event{e}
	case "extension_ui_request":
		var d dialogRequest
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		question, ok := d.interaction()
		if !ok {
			return nil
		}
		e := ev(agent.InteractionRequested)
		e.Interaction = &question
		return []agent.Event{e}
	}
	return nil
}

func (t *translator) setSession(id agent.SessionID) {
	t.mu.Lock()
	t.session = id
	t.mu.Unlock()
}

// sessionID is the conversation this process belongs to.
func (t *translator) sessionID() agent.SessionID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session
}

// isRunning reports a turn in progress.
func (t *translator) isRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

func (d dialogRequest) interaction() (agent.Interaction, bool) {
	i := agent.Interaction{ID: agent.InteractionID(d.ID), Title: d.Title, Message: d.Message,
		Options: d.Options, Placeholder: d.Placeholder, Prefill: d.Prefill}
	switch d.Method {
	case "confirm":
		i.Kind = agent.Confirm
	case "select":
		i.Kind = agent.Select
	case "input":
		i.Kind = agent.Input
	case "editor":
		i.Kind = agent.Editor
	default:
		return i, false
	}
	return i, true
}

func dialogReply(id, method string, answer agent.InteractionAnswer) map[string]any {
	out := map[string]any{"type": "extension_ui_response", "id": id}
	switch {
	case answer.Cancelled:
		out["cancelled"] = true
	case method == "confirm":
		out["confirmed"] = answer.Confirmed
	default:
		out["value"] = answer.Value
	}
	return out
}

// toolKind maps Pi's built-in tool names onto the shared kinds; extension
// tools are ToolOther.
func toolKind(name string) agent.ToolKind {
	switch name {
	case "bash":
		return agent.ToolShell
	case "read":
		return agent.ToolRead
	case "grep", "find", "ls":
		return agent.ToolSearch
	case "edit", "write":
		return agent.ToolEdit
	}
	return agent.ToolOther
}
