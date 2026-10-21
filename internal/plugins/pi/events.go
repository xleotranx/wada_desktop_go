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
	failed   bool
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
	ID      string   `json:"id"`
	Method  string   `json:"method"`
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Options []string `json:"options"`
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
		t.running, t.failed = true, false
		return []agent.Event{ev(agent.TurnStarted)}
	case "agent_settled":
		if !t.running {
			return nil
		}
		t.running = false
		switch {
		case w.Aborted:
			return []agent.Event{ev(agent.TurnInterrupted)}
		case t.failed:
			return []agent.Event{ev(agent.TurnFailed)}
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
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
		}
		if json.Unmarshal(w.Message, &m) == nil && m.Role == "assistant" && m.StopReason == "error" {
			t.failed = true
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
			e.Tool = &agent.Tool{ID: w.Update.ID, Name: w.Update.ToolName}
			return []agent.Event{e}
		}
	case "tool_execution_start":
		e := ev(agent.ToolCalled)
		e.Tool = &agent.Tool{ID: w.ToolCallID, Name: w.ToolName, Command: commandOf(w.Args)}
		return []agent.Event{e}
	case "tool_execution_end":
		var result struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(w.Result, &result)
		e := ev(agent.ToolFinished)
		e.Tool = &agent.Tool{ID: w.ToolCallID, Name: w.ToolName, Command: commandOf(w.Args),
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
		if json.Unmarshal(raw, &d) != nil || (d.Method != "confirm" && d.Method != "select") {
			return nil
		}
		e := ev(agent.ApprovalRequested)
		e.Approval = &agent.Approval{ID: agent.ApprovalID(d.ID), Action: cmpOr(d.Title, "confirm")}
		if d.Message != "" {
			e.Approval.Resources = []string{d.Message}
		}
		e.Approval.Resources = append(e.Approval.Resources, d.Options...)
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

// dialogReply answers an extension dialog: confirm with yes or no, select with
// the first option or cancel, and anything else with cancel.
func dialogReply(id, method string, options []string, d agent.Decision) map[string]any {
	out := map[string]any{"type": "extension_ui_response", "id": id}
	switch {
	case method == "confirm" && d != agent.Reject:
		out["confirmed"] = true
	case method == "confirm":
		out["confirmed"] = false
	case method == "select" && d != agent.Reject && len(options) > 0:
		out["value"] = options[0]
	default:
		out["cancelled"] = true
	}
	return out
}
