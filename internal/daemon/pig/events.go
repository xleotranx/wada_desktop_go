package pig

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/protocol"
)

type translator struct {
	mu       sync.Mutex
	session  string
	message  string
	serial   int
	running  bool
	failure  string
	terminal *protocol.Event
	request  string
	managed  bool
}

func (e *engine) onEvent(c *conversation, event pa.AgentEvent) {
	t := &c.translator
	t.mu.Lock()
	defer t.mu.Unlock()
	emit := func(ev protocol.Event) { ev.Session = t.session; ev.RequestID = t.request; e.emit(ev) }
	text := func(kind protocol.EventKind, index int, delta, full string) {
		emit(protocol.Event{Kind: kind, Text: &protocol.TextEvent{Message: t.message, Ordinal: index, Delta: delta, Text: full}})
	}
	switch ev := event.(type) {
	case pa.AgentStartEvent:
		if !t.running {
			t.running = true
			t.failure = ""
			emit(protocol.Event{Kind: protocol.EventTurnStarted})
		}
	case pa.MessageStartEvent:
		if ev.Message.Assistant != nil {
			t.serial++
			t.message = fmt.Sprintf("%s/live-%d", t.session, t.serial)
		}
	case pa.MessageUpdateEvent:
		switch update := ev.AssistantMessageEvent.(type) {
		case ai.TextStartEvent:
			text(protocol.EventTextStarted, update.ContentIndex, "", "")
		case ai.TextDeltaEvent:
			text(protocol.EventTextDelta, update.ContentIndex, update.Delta, "")
		case ai.TextEndEvent:
			text(protocol.EventTextEnded, update.ContentIndex, "", update.Content)
		case ai.ToolCallEndEvent:
			emit(protocol.Event{Kind: protocol.EventToolStarted, Tool: &protocol.Tool{ID: update.ToolCall.ID, Name: update.ToolCall.Name, Kind: toolKind(update.ToolCall.Name)}})
		}
	case pa.MessageEndEvent:
		if m := ev.Message.Assistant; m != nil {
			t.failure = ""
			if string(m.StopReason) == "error" {
				t.failure = m.ErrorMessage
				if t.failure == "" {
					t.failure = "the model returned an error"
				}
			}
		}
	case pa.ToolExecutionStartEvent:
		emit(protocol.Event{Kind: protocol.EventToolCalled, Tool: &protocol.Tool{ID: ev.ToolCallID, Name: ev.ToolName, Kind: toolKind(ev.ToolName), Command: commandOf(ev.Args)}})
	case pa.ToolExecutionEndEvent:
		output := []string{}
		for _, block := range ev.Result.Content {
			if txt, ok := block.(ai.TextContent); ok {
				output = append(output, txt.Text)
			}
		}
		status := "completed"
		if ev.IsError {
			status = "error"
		}
		emit(protocol.Event{Kind: protocol.EventToolFinished, Tool: &protocol.Tool{ID: ev.ToolCallID, Name: ev.ToolName, Kind: toolKind(ev.ToolName), Status: status, Output: output}})
		e.finishMutation(c, ev.ToolCallID)
	case pa.AgentSettledEvent:
		if !t.running {
			return
		}
		t.running = false
		kind := protocol.EventTurnSucceeded
		if ev.Aborted {
			kind = protocol.EventTurnInterrupted
		} else if t.failure != "" {
			kind = protocol.EventTurnFailed
		}
		if t.managed {
			t.terminal = &protocol.Event{Kind: kind, Session: t.session, Error: t.failure}
		} else {
			emit(protocol.Event{Kind: kind, Error: t.failure})
		}
	case pa.SessionInfoChangedEvent:
		emit(protocol.Event{Kind: protocol.EventSessionRenamed, Title: ev.Name})
	}
}

func commandOf(args json.RawMessage) string {
	var v struct {
		Command string `json:"command"`
		Action  string `json:"action"`
		Input   struct {
			Path string `json:"path"`
		} `json:"input"`
	}
	_ = json.Unmarshal(args, &v)
	if v.Command != "" {
		return v.Command
	}
	if v.Input.Path != "" {
		return v.Action + " " + v.Input.Path
	}
	return v.Action
}

func modelRef(m *ai.Model) protocol.ModelRef {
	if m == nil {
		return protocol.ModelRef{}
	}
	return protocol.ModelRef{Provider: m.ProviderMeta.ProviderID, ID: m.ID}
}

func sessionModel(s *coding.Session) protocol.ModelRef {
	r := modelRef(s.Model())
	r.Variant = string(s.ThinkingLevel())
	return r
}

func normalizeModel(m *ai.Model) protocol.Model {
	out := protocol.Model{Ref: modelRef(m), Name: m.DisplayName, Context: m.Capabilities.ContextWindow, Free: m.Capabilities.InputCostPer1M == 0 && m.Capabilities.OutputCostPer1M == 0}
	for _, level := range ai.GetSupportedThinkingLevels(m) {
		out.Variants = append(out.Variants, string(level))
	}
	return out
}

func messageCost(messages []pa.AgentMessage) float64 {
	var sum float64
	for _, m := range messages {
		if m.Assistant != nil && m.Assistant.Usage != nil {
			sum += m.Assistant.Usage.Cost.Total
		}
	}
	return sum
}

func storedMessages(c *conversation) []protocol.Message {
	var out []protocol.Message
	user := 0
	tools := map[string]*protocol.Tool{}
	for _, entry := range c.session.SessionManager().GetBranch() {
		var raw struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Message pa.AgentMessage `json:"message"`
		}
		if json.Unmarshal(entry.Raw(), &raw) != nil || raw.Type != "message" {
			continue
		}
		m := protocol.Message{ID: raw.ID}
		if u := raw.Message.User; u != nil {
			m.Role = protocol.RoleUser
			data, _ := json.Marshal(u.Content)
			var plain string
			if json.Unmarshal(data, &plain) == nil {
				m.Text = plain
			} else {
				var blocks []struct{ Type, Text, MimeType string }
				_ = json.Unmarshal(data, &blocks)
				for _, b := range blocks {
					switch b.Type {
					case "text":
						m.Text += b.Text
					case "image":
						m.Files = append(m.Files, protocol.File{Name: "image", MIME: b.MimeType})
					}
				}
			}
			if meta, ok := c.rec.Attachments[strconv.Itoa(user)]; ok {
				m.Text = meta.Text
				m.Files = append([]protocol.File(nil), meta.Files...)
			}
			internal := c.rec.Attachments[strconv.Itoa(user)].Internal
			user++
			if internal {
				m.Role = protocol.RoleOther
				m.Text = ""
				m.Files = nil
			}
			out = append(out, m)
		} else if a := raw.Message.Assistant; a != nil {
			m.Role = protocol.RoleAssistant
			m.Model = protocol.ModelRef{Provider: a.Provider, ID: a.ModelID, Variant: string(a.ThinkingLevel)}
			if u := a.Usage; u != nil {
				m.Cost = u.Cost.Total
				reasoning := 0
				if u.Reasoning != nil {
					reasoning = *u.Reasoning
				}
				m.Tokens = protocol.Tokens{Input: u.Input, Output: max(0, u.Output-reasoning), Reasoning: reasoning, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
			}
			for _, block := range a.Content {
				switch b := block.(type) {
				case ai.TextContent:
					m.Parts = append(m.Parts, protocol.Part{Kind: protocol.PartText, Text: b.Text})
				case ai.ThinkingContent:
					m.Parts = append(m.Parts, protocol.Part{Kind: protocol.PartReasoning, Text: b.Thinking})
				case ai.ToolCall:
					t := &protocol.Tool{ID: b.ID, Name: b.Name, Kind: toolKind(b.Name), Command: toolCommand(b.Arguments), Status: "running"}
					tools[b.ID] = t
					m.Parts = append(m.Parts, protocol.Part{Kind: protocol.PartTool, Tool: t})
				}
			}
			out = append(out, m)
		} else if result := raw.Message.ToolResult; result != nil {
			if t := tools[result.ToolCallID]; t != nil {
				t.Status = "completed"
				if result.IsError {
					t.Status = "error"
				}
				for _, block := range result.Content {
					if b, ok := block.(ai.TextContent); ok {
						t.Output = append(t.Output, b.Text)
					}
				}
			}
		}
	}
	return out
}

func actionKey(name string, args json.RawMessage) string {
	return name + " " + strings.TrimSpace(string(args))
}

func toolCommand(args ai.JsonObject) string { data, _ := json.Marshal(args); return commandOf(data) }

func lastAssistantText(c *conversation) string {
	messages := storedMessages(c)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != protocol.RoleAssistant {
			continue
		}
		var text strings.Builder
		for _, part := range m.Parts {
			if part.Kind == protocol.PartText {
				text.WriteString(part.Text)
			}
		}
		return text.String()
	}
	return ""
}

func (e *engine) finishTurn(c *conversation, started, stopped bool, runErr, saveErr error) {
	names := withoutTurnTools(c.session.ActiveToolNames())
	c.turnMu.Lock()
	var result, schema json.RawMessage
	if c.turn != nil {
		result, schema = c.turn.result, c.turn.schema
		if c.turn.tools != nil {
			names = c.turn.tools
		}
	}
	c.turn = nil
	c.turnMu.Unlock()
	if len(result) == 0 && len(schema) > 0 {
		if text := lastAssistantText(c); text != "" {
			if parsed, ok := resultFromText(text, schema); ok {
				result = parsed
			}
		}
	}
	c.session.SetActiveToolsByName(names)
	t := &c.translator
	t.mu.Lock()
	terminal := t.terminal
	if terminal != nil {
		terminal.RequestID = t.request
	}
	t.terminal = nil
	t.managed = false
	if started || terminal != nil {
		t.running = false
	}
	t.mu.Unlock()
	if terminal == nil && started {
		terminal = &protocol.Event{Session: t.session, Kind: protocol.EventTurnSucceeded, RequestID: t.request}
	}
	if terminal == nil {
		return
	}
	if stopped {
		terminal.Kind = protocol.EventTurnInterrupted
		terminal.Error = ""
	} else if runErr != nil {
		terminal.Kind = protocol.EventTurnFailed
		terminal.Error = runErr.Error()
	} else if saveErr != nil {
		terminal.Kind = protocol.EventTurnFailed
		terminal.Error = "could not save conversation metadata"
	}
	if terminal.Kind == protocol.EventTurnSucceeded {
		terminal.Result = result
	}
	e.emit(*terminal)
}
