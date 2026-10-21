package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/agent"
)

func validEventPayload(kind EventKind) string {
	switch kind {
	case TextStarted, TextDelta, TextEnded:
		return `{"sessionID":"ses-1","assistantMessageID":"msg-1","ordinal":2,"delta":"hello","text":"hello world"}`
	case ToolStarted, ToolCalled, ToolSucceeded:
		return `{"sessionID":"ses-1","id":"tool-1","name":"bash","input":{"command":"go test ./..."},"content":[{"type":"text","text":"PASS"}],"metadata":{"status":"completed","exit":0}}`
	case PermissionAsked:
		return `{"sessionID":"ses-1","id":"perm-1","action":"shell","resources":["go test ./..."],"save":["go test *"]}`
	case ExecutionStarted, ExecutionSucceeded, ExecutionFailed, ExecutionInterrupted:
		return `{"sessionID":"ses-1"}`
	case ModelSelected:
		return `{"sessionID":"ses-1","model":{"providerID":"provider-1","id":"model-1","variant":"high"}}`
	case SessionRenamed:
		return `{"sessionID":"ses-1","title":"An updated title"}`
	default:
		panic("missing known event fixture")
	}
}

var supportedEventKinds = []EventKind{
	TextStarted, TextDelta, TextEnded,
	ToolStarted, ToolCalled, ToolSucceeded,
	PermissionAsked, ExecutionStarted, ExecutionSucceeded,
	ExecutionFailed, ExecutionInterrupted, ModelSelected, SessionRenamed,
}

func TestDecodeEveryKnownEvent(t *testing.T) {
	for _, kind := range supportedEventKinds {
		t.Run(string(kind), func(t *testing.T) {
			raw := validEventPayload(kind)
			got, known, err := DecodeEvent(Event{Type: string(kind), Data: json.RawMessage(raw)})
			if err != nil || !known {
				t.Fatalf("known event rejected: known=%t err=%v", known, err)
			}
			if got.Kind != eventKinds[kind] || got.SessionID != "ses-1" || got.Bytes != len(raw) {
				t.Fatalf("event identity/size = %+v", got)
			}
			switch kind {
			case TextStarted, TextDelta, TextEnded:
				if got.Text.MessageID != "msg-1" || got.Text.Ordinal != 2 || got.Text.Delta != "hello" || got.Text.Text != "hello world" {
					t.Fatalf("text payload = %+v", got.Text)
				}
			case ToolStarted, ToolCalled, ToolSucceeded:
				if got.Tool.ID != "tool-1" || got.Tool.Name != "bash" || got.Tool.Command != "go test ./..." || len(got.Tool.Content) != 1 || got.Tool.Content[0].Text != "PASS" || got.Tool.Status != "completed" {
					t.Fatalf("typed tool payload = %+v", got.Tool)
				}
			case PermissionAsked:
				if got.Permission.ID != "perm-1" || got.Permission.Action != "shell" || !reflect.DeepEqual(got.Permission.Resources, []string{"go test ./..."}) || !reflect.DeepEqual(got.Permission.Save, []string{"go test *"}) {
					t.Fatalf("permission payload = %+v", got.Permission)
				}
			case ModelSelected:
				if got.Model != (agent.ModelRef{ProviderID: "provider-1", ID: "model-1", Variant: "high"}) {
					t.Fatalf("model payload = %+v", got.Model)
				}
			case SessionRenamed:
				if got.Title != "An updated title" {
					t.Fatalf("title = %q", got.Title)
				}
			case ExecutionStarted, ExecutionSucceeded, ExecutionFailed, ExecutionInterrupted:
			}
		})
	}
}

func TestDecodeUnknownEventIgnoresItsPayload(t *testing.T) {
	for _, payload := range []string{`{"sessionID":"ses-1"}`, `{broken`, `null`} {
		got, known, err := DecodeEvent(Event{Type: "future.event", Data: json.RawMessage(payload)})
		if known || err != nil || !reflect.DeepEqual(got, agent.Update{}) {
			t.Fatalf("unknown event = %+v, %t, %v", got, known, err)
		}
	}
}

func requireMalformedEvent(t *testing.T, kind EventKind, payload string) {
	t.Helper()
	got, known, err := DecodeEvent(Event{Type: string(kind), Data: json.RawMessage(payload)})
	if !known || err == nil {
		t.Fatalf("malformed event accepted: %+v, known=%t err=%v", got, known, err)
	}
	if !reflect.DeepEqual(got, agent.Update{}) {
		t.Fatalf("malformed event exposed partial update: %+v", got)
	}
	if len(err.Error()) > 128 || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unbounded or leaking diagnostic: %q", err)
	}
}

func TestDecodeKnownEventsRejectMalformedShapesAndMissingSession(t *testing.T) {
	for _, kind := range supportedEventKinds {
		t.Run(string(kind), func(t *testing.T) {
			for _, raw := range []string{`{private-secret`, `null`, `[]`, `"private-secret"`, `42`, `true`, `{}`, `{"sessionID":42}`} {
				requireMalformedEvent(t, kind, raw)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(validEventPayload(kind)), &payload); err != nil {
				t.Fatal(err)
			}
			delete(payload, "sessionID")
			raw, _ := json.Marshal(payload)
			requireMalformedEvent(t, kind, string(raw))
		})
	}
}

func TestDecodeKnownEventsRequireSpecificIdentities(t *testing.T) {
	cases := []struct {
		kind    EventKind
		payload string
	}{
		{TextDelta, `{"sessionID":"ses-1","ordinal":0,"delta":"private-secret"}`},
		{TextDelta, `{"sessionID":"ses-1","assistantMessageID":"msg-1","ordinal":-1}`},
		{TextStarted, `{"sessionID":"ses-1","assistantMessageID":23}`},
		{ToolStarted, `{"sessionID":"ses-1","name":"bash"}`},
		{ToolCalled, `{"sessionID":"ses-1","id":"tool-1","input":{"command":42}}`},
		{ToolCalled, `{"sessionID":"ses-1","id":"tool-1","input":"private-secret"}`},
		{ToolSucceeded, `{"sessionID":"ses-1","id":"tool-1","content":"private-secret"}`},
		{PermissionAsked, `{"sessionID":"ses-1","action":"shell"}`},
		{PermissionAsked, `{"sessionID":"ses-1","id":"perm-1"}`},
		{PermissionAsked, `{"sessionID":"ses-1","id":"perm-1","action":"shell","resources":42}`},
		{ModelSelected, `{"sessionID":"ses-1","model":{"id":"model-1"}}`},
		{ModelSelected, `{"sessionID":"ses-1","model":{"providerID":"provider-1"}}`},
		{ModelSelected, `{"sessionID":"ses-1","model":"private-secret"}`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%s/%d", tc.kind, i), func(t *testing.T) { requireMalformedEvent(t, tc.kind, tc.payload) })
	}
	requireMalformedEvent(t, ToolCalled, `{"sessionID":"ses-1","id":"tool-1","input":{"command":42,"private-secret":"`+strings.Repeat("private-secret", 10000)+`"}}`)
}

func TestEventsCancellationClosesStreamingRequest(t *testing.T) {
	received := make(chan Event, 1)
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/event" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("unexpected stream request: %s %s", r.URL.Path, r.Header.Get("Accept"))
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "opencode" || password != "test-password" {
			t.Error("missing stream authentication")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"session.execution.started\",\"data\":{\"sessionID\":\"ses-1\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	client, err := NewClient(agent.Config{URL: server.URL, Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Events(ctx, func(ev Event) { received <- ev }) }()
	select {
	case ev := <-received:
		if ev.Type != string(ExecutionStarted) {
			t.Fatalf("stream event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not deliver event")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop on cancellation")
	}
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("server request did not close on cancellation")
	}
}

func TestNormalizedStreamFiltersUnknownAndBoundsMalformedDiagnostics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range 1000 {
			fmt.Fprintln(w, `data: {"type":"session.text.delta","data":{"sessionID":"missing-identity"}}`)
		}
		fmt.Fprintln(w, `data: {"type":"future.event","data":{}}`)
		fmt.Fprintln(w, `data: {"type":"session.text.delta","data":{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"hello"}}`)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	notices, count := 0, 0
	err := c.Updates(context.Background(), func(update agent.Update) {
		count++
		if update.Kind != agent.TextDelta || update.Text.Delta != "hello" {
			t.Fatalf("update=%+v", update)
		}
	}, func(string) { notices++ })
	if err != nil || notices != 1 || count != 1 {
		t.Fatalf("err=%v notices=%d updates=%d", err, notices, count)
	}
}

func TestTextNormalizationDoesNotAllocateASecondPayload(t *testing.T) {
	raw := []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"hello"}`)
	wire := testing.AllocsPerRun(100, func() {
		var payload TextData
		if err := json.Unmarshal(raw, &payload); err != nil {
			panic(err)
		}
	})
	normalized := testing.AllocsPerRun(100, func() {
		if _, _, err := DecodeEvent(Event{Type: string(TextDelta), Data: raw}); err != nil {
			panic(err)
		}
	})
	if normalized > wire {
		t.Fatalf("normalized payload uses %.0f allocations, wire decode %.0f", normalized, wire)
	}
}
