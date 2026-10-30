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
	"wada-desktop-go/internal/contract/agent"
)

type translator struct {
	mu       sync.Mutex
	session  agent.SessionID
	message  agent.MessageID
	serial   int
	running  bool
	failure  string
	terminal *agent.Event
	request  string
	managed  bool
}

func (e *engine) onEvent(c *conversation, event pa.AgentEvent) {
	t := &c.translator
	t.mu.Lock()
	defer t.mu.Unlock()
	emit := func(ev agent.Event) { ev.Session = t.session; ev.RequestID = t.request; e.emit(ev) }
	text := func(kind agent.EventKind, index int, delta, full string) {
		emit(agent.Event{Kind: kind, Text: &agent.TextEvent{Message: t.message, Ordinal: index, Delta: delta, Text: full}})
	}
	switch ev := event.(type) {
	case pa.AgentStartEvent:
		if !t.running {
			t.running = true
			t.failure = ""
			emit(agent.Event{Kind: agent.TurnStarted})
		}
	case pa.MessageStartEvent:
		if ev.Message.Assistant != nil {
			t.serial++
			t.message = agent.MessageID(fmt.Sprintf("%s/live-%d", t.session, t.serial))
		}
	case pa.MessageUpdateEvent:
		switch update := ev.AssistantMessageEvent.(type) {
		case ai.TextStartEvent:
			text(agent.TextStarted, update.ContentIndex, "", "")
		case ai.TextDeltaEvent:
			text(agent.TextDelta, update.ContentIndex, update.Delta, "")
		case ai.TextEndEvent:
			text(agent.TextEnded, update.ContentIndex, "", update.Content)
		case ai.ToolCallEndEvent:
			emit(agent.Event{Kind: agent.ToolStarted, Tool: &agent.Tool{ID: update.ToolCall.ID, Name: update.ToolCall.Name, Kind: toolKind(update.ToolCall.Name)}})
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
		emit(agent.Event{Kind: agent.ToolCalled, Tool: &agent.Tool{ID: ev.ToolCallID, Name: ev.ToolName, Kind: toolKind(ev.ToolName), Command: commandOf(ev.Args)}})
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
		emit(agent.Event{Kind: agent.ToolFinished, Tool: &agent.Tool{ID: ev.ToolCallID, Name: ev.ToolName, Kind: toolKind(ev.ToolName), Status: status, Output: output}})
		e.finishMutation(c, ev.ToolCallID)
	case pa.AgentSettledEvent:
		if !t.running {
			return
		}
		t.running = false
		kind := agent.TurnSucceeded
		if ev.Aborted {
			kind = agent.TurnInterrupted
		} else if t.failure != "" {
			kind = agent.TurnFailed
		}
		if t.managed {
			t.terminal = &agent.Event{Kind: kind, Session: t.session, Error: t.failure}
		} else {
			emit(agent.Event{Kind: kind, Error: t.failure})
		}
	case pa.SessionInfoChangedEvent:
		emit(agent.Event{Kind: agent.SessionRenamed, Title: ev.Name})
	}
}
func commandOf(args json.RawMessage) string {
	var v struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &v)
	return v.Command
}
func modelRef(m *ai.Model) agent.ModelRef {
	if m == nil {
		return agent.ModelRef{}
	}
	return agent.ModelRef{Provider: m.ProviderMeta.ProviderID, ID: m.ID}
}
func sessionModel(s *coding.Session) agent.ModelRef {
	r := modelRef(s.Model())
	r.Variant = string(s.ThinkingLevel())
	return r
}
func normalizeModel(m *ai.Model) agent.Model {
	out := agent.Model{Ref: modelRef(m), Name: m.DisplayName, Context: m.Capabilities.ContextWindow, Free: m.Capabilities.InputCostPer1M == 0 && m.Capabilities.OutputCostPer1M == 0}
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

func storedMessages(c *conversation) []agent.Message {
	var out []agent.Message
	user := 0
	tools := map[string]*agent.Tool{}
	for _, entry := range c.session.SessionManager().GetBranch() {
		var raw struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Message pa.AgentMessage `json:"message"`
		}
		if json.Unmarshal(entry.Raw(), &raw) != nil || raw.Type != "message" {
			continue
		}
		m := agent.Message{ID: agent.MessageID(raw.ID)}
		if u := raw.Message.User; u != nil {
			m.Role = agent.RoleUser
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
						m.Files = append(m.Files, agent.File{Name: "image", MIME: b.MimeType})
					}
				}
			}
			if meta, ok := c.rec.Attachments[strconv.Itoa(user)]; ok {
				m.Text = meta.Text
				m.Files = append([]agent.File(nil), meta.Files...)
			}
			internal := c.rec.Attachments[strconv.Itoa(user)].Internal
			user++
			if internal {
				m.Role = agent.RoleOther
				m.Text = ""
				m.Files = nil
			}
			out = append(out, m)
		} else if a := raw.Message.Assistant; a != nil {
			m.Role = agent.RoleAssistant
			m.Model = agent.ModelRef{Provider: a.Provider, ID: a.ModelID, Variant: string(a.ThinkingLevel)}
			if u := a.Usage; u != nil {
				m.Cost = u.Cost.Total
				reasoning := 0
				if u.Reasoning != nil {
					reasoning = *u.Reasoning
				}
				m.Tokens = agent.Tokens{Input: u.Input, Output: max(0, u.Output-reasoning), Reasoning: reasoning, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
			}
			for _, block := range a.Content {
				switch b := block.(type) {
				case ai.TextContent:
					m.Parts = append(m.Parts, agent.Part{Kind: agent.PartText, Text: b.Text})
				case ai.ThinkingContent:
					m.Parts = append(m.Parts, agent.Part{Kind: agent.PartReasoning, Text: b.Thinking})
				case ai.ToolCall:
					t := &agent.Tool{ID: b.ID, Name: b.Name, Kind: toolKind(b.Name), Command: toolCommand(b.Arguments), Status: "running"}
					tools[b.ID] = t
					m.Parts = append(m.Parts, agent.Part{Kind: agent.PartTool, Tool: t})
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

// finishTurn publishes settlement only after the worker has saved metadata and
// released its admission slot, so an immediate next prompt can be admitted.
func (e *engine) finishTurn(c *conversation, started, stopped bool, runErr, saveErr error) {
	c.turnMu.Lock()
	var result json.RawMessage
	if c.turn != nil {
		result = c.turn.result
	}
	c.turn = nil
	c.turnMu.Unlock()
	c.session.SetActiveToolsByName(withoutResult(c.session.ActiveToolNames()))
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
		terminal = &agent.Event{Session: t.session, Kind: agent.TurnSucceeded, RequestID: t.request}
	}
	if terminal == nil {
		return
	}
	if stopped {
		terminal.Kind = agent.TurnInterrupted
		terminal.Error = ""
	} else if runErr != nil {
		terminal.Kind = agent.TurnFailed
		terminal.Error = runErr.Error()
	} else if saveErr != nil {
		terminal.Kind = agent.TurnFailed
		terminal.Error = "could not save conversation metadata"
	}
	if terminal.Kind == agent.TurnSucceeded {
		terminal.Result = result
	}
	e.emit(*terminal)
}
