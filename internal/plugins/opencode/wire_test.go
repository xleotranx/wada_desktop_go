package opencode

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
)

var eventFixtures = map[string]struct {
	payload string
	kind    agent.EventKind
}{
	"session.text.started":          {`{"sessionID":"ses-1","assistantMessageID":"msg-1","ordinal":2,"delta":"hello","text":"hello world"}`, agent.TextStarted},
	"session.text.delta":            {`{"sessionID":"ses-1","assistantMessageID":"msg-1","ordinal":2,"delta":"hello","text":"hello world"}`, agent.TextDelta},
	"session.text.ended":            {`{"sessionID":"ses-1","assistantMessageID":"msg-1","ordinal":2,"delta":"hello","text":"hello world"}`, agent.TextEnded},
	"session.tool.input.started":    {`{"sessionID":"ses-1","id":"tool-1","name":"bash","input":{"command":"go test ./..."},"content":[{"type":"text","text":"PASS"}],"metadata":{"status":"completed","exit":0}}`, agent.ToolStarted},
	"session.tool.called":           {`{"sessionID":"ses-1","id":"tool-1","name":"bash","input":{"command":"go test ./..."},"content":[{"type":"text","text":"PASS"}],"metadata":{"status":"completed","exit":0}}`, agent.ToolCalled},
	"session.tool.success":          {`{"sessionID":"ses-1","id":"tool-1","name":"bash","input":{"command":"go test ./..."},"content":[{"type":"text","text":"PASS"}],"metadata":{"status":"completed","exit":0}}`, agent.ToolFinished},
	"permission.asked":              {`{"sessionID":"ses-1","id":"perm-1","action":"shell","resources":["go test ./..."],"save":["go test *"]}`, agent.ApprovalRequested},
	"session.execution.started":     {`{"sessionID":"ses-1"}`, agent.TurnStarted},
	"session.execution.succeeded":   {`{"sessionID":"ses-1"}`, agent.TurnSucceeded},
	"session.execution.failed":      {`{"sessionID":"ses-1"}`, agent.TurnFailed},
	"session.execution.interrupted": {`{"sessionID":"ses-1"}`, agent.TurnInterrupted},
	"session.model.selected":        {`{"sessionID":"ses-1","model":{"providerID":"provider-1","id":"model-1","variant":"high"}}`, agent.ModelChanged},
	"session.renamed":               {`{"sessionID":"ses-1","title":"An updated title"}`, agent.SessionRenamed},
}

func TestDecodeEveryKnownEvent(t *testing.T) {
	for name, fx := range eventFixtures {
		t.Run(name, func(t *testing.T) {
			got, known, err := decodeEvent(wireEvent{Type: name, Data: json.RawMessage(fx.payload)})
			if err != nil || !known {
				t.Fatalf("rejected: known=%t err=%v", known, err)
			}
			if got.Kind != fx.kind || got.Session != "ses-1" || got.Bytes != len(fx.payload) {
				t.Fatalf("identity = %+v", got)
			}
			switch got.Kind {
			case agent.TextStarted, agent.TextDelta, agent.TextEnded:
				if *got.Text != (agent.TextEvent{Message: "msg-1", Ordinal: 2, Delta: "hello", Text: "hello world"}) {
					t.Fatalf("text = %+v", got.Text)
				}
			case agent.ToolStarted, agent.ToolCalled, agent.ToolFinished:
				want := agent.Tool{ID: "tool-1", Name: "bash", Command: "go test ./...", Output: []string{"PASS"}, Status: "completed"}
				if !reflect.DeepEqual(*got.Tool, want) {
					t.Fatalf("tool = %+v", got.Tool)
				}
			case agent.ApprovalRequested:
				want := agent.Approval{ID: "perm-1", Action: "shell", Resources: []string{"go test ./..."}, Save: []string{"go test *"}}
				if !reflect.DeepEqual(*got.Approval, want) {
					t.Fatalf("approval = %+v", got.Approval)
				}
			case agent.ModelChanged:
				if got.Model != (agent.ModelRef{Provider: "provider-1", ID: "model-1", Variant: "high"}) {
					t.Fatalf("model = %+v", got.Model)
				}
			case agent.SessionRenamed:
				if got.Title != "An updated title" {
					t.Fatalf("title = %q", got.Title)
				}
			case agent.TurnStarted, agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			}
		})
	}
}

func TestDecodeIgnoresUnknownEvents(t *testing.T) {
	for _, payload := range []string{`{"sessionID":"ses-1"}`, `{broken`, `null`} {
		got, known, err := decodeEvent(wireEvent{Type: "future.event", Data: json.RawMessage(payload)})
		if known || err != nil || !reflect.DeepEqual(got, agent.Event{}) {
			t.Fatalf("unknown event = %+v, %t, %v", got, known, err)
		}
	}
}

func TestDecodeRejectsMalformedKnownEvents(t *testing.T) {
	for name, fx := range eventFixtures {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			json.Unmarshal([]byte(fx.payload), &doc)
			delete(doc, "sessionID")
			noSession, _ := json.Marshal(doc)
			for _, raw := range []string{`{private-secret`, `null`, `[]`, `"private-secret"`, `42`, `true`, `{}`, `{"sessionID":42}`, string(noSession)} {
				got, known, err := decodeEvent(wireEvent{Type: name, Data: json.RawMessage(raw)})
				if !known || err == nil {
					t.Fatalf("%s accepted: %+v", raw, got)
				}
				if !reflect.DeepEqual(got, agent.Event{}) {
					t.Fatalf("%s exposed a partial event: %+v", raw, got)
				}
				if len(err.Error()) > 128 || strings.Contains(err.Error(), "private-secret") {
					t.Fatalf("unbounded or leaking error: %q", err)
				}
			}
		})
	}
}

func TestStoredMessagesConvert(t *testing.T) {
	raw := `[{"id":"msg_u","type":"user","payload":{"text":"Run the wiring check"}},` +
		`{"id":"msg_r","type":"assistant","cost":0.5,"tokens":{"output":116,"reasoning":387},` +
		`"model":{"providerID":"p","id":"m"},"content":[` +
		`{"type":"reasoning","text":"thinking"},{"type":"text","text":"Running it now."},` +
		`{"type":"tool","id":"call_9","name":"shell","state":{"status":"completed","input":{"command":"make check"},` +
		`"content":[{"type":"text","text":"ok\n"},{"type":"image","text":"skip"}],"metadata":{"exit":2,"truncated":true}}}]},` +
		`{"id":"msg_u2","type":"user","text":"Top-level hello","files":[{"data":"eA==","mime":"image/png","source":{"type":"inline"},"name":"shot.png"}]},` +
		`{"id":"idle","type":"idle"}]`
	var wire []wireMessage
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatal(err)
	}
	var got []agent.Message
	for _, m := range wire {
		got = append(got, m.message())
	}
	want := []agent.Message{
		{ID: "msg_u", Role: agent.RoleUser, Text: "Run the wiring check"},
		{ID: "msg_r", Role: agent.RoleAssistant, Cost: 0.5, Tokens: agent.Tokens{Output: 116, Reasoning: 387},
			Model: agent.ModelRef{Provider: "p", ID: "m"}, Parts: []agent.Part{
				{Kind: agent.PartReasoning, Text: "thinking"},
				{Kind: agent.PartText, Text: "Running it now."},
				{Kind: agent.PartTool, Tool: &agent.Tool{ID: "call_9", Name: "shell", Command: "make check",
					Output: []string{"ok\n"}, Status: "completed", Exit: 2, Truncated: true}},
			}},
		{ID: "msg_u2", Role: agent.RoleUser, Text: "Top-level hello", Files: []agent.File{{Name: "shot.png", MIME: "image/png"}}},
		{ID: "idle", Role: agent.RoleOther},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages\n got %+v\nwant %+v", got, want)
	}
}

func TestAPIBase(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:4096":          "http://localhost:4096/api",
		"https://host.test/":      "https://host.test/api",
		"http://host.test/api/":   "http://host.test/api",
		"http://host.test/proxy/": "http://host.test/proxy/api",
		"  http://h.test:1/api  ": "http://h.test:1/api",
	} {
		if got, err := apiBase(in); err != nil || got != want {
			t.Errorf("apiBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "http://"} {
		if _, err := apiBase(bad); err == nil {
			t.Errorf("apiBase(%q) accepted", bad)
		}
	}
}

func TestFailedTurnCarriesTheError(t *testing.T) {
	for payload, want := range map[string]string{
		`{"sessionID":"s","error":"quota exceeded"}`:                        "quota exceeded",
		`{"sessionID":"s","error":{"name":"APIError","message":"bad key"}}`: "bad key",
		`{"sessionID":"s","error":{"data":{"message":"nested"}}}`:           "nested",
		`{"sessionID":"s"}`: "",
	} {
		ev, _, err := decodeEvent(wireEvent{Type: "session.execution.failed", Data: json.RawMessage(payload)})
		if err != nil || ev.Kind != agent.TurnFailed || ev.Error != want {
			t.Errorf("%s: %+v, %v", payload, ev, err)
		}
	}
}
