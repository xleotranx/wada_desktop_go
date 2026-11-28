package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServer(t *testing.T) {
	s, err := Start("test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ep, err := s.Register([]Tool{
		{Name: "echo", InputSchema: map[string]any{"type": "object"}, Call: func(_ context.Context, args json.RawMessage) (any, error) {
			var in map[string]any
			_ = json.Unmarshal(args, &in)
			return in, nil
		}},
		{Name: "fail", Call: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("nope") }},
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(method, body string) (int, string) {
		req, _ := http.NewRequestWithContext(t.Context(), method, ep.URL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ep.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for _, c := range []struct{ body, want string }{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, `"protocolVersion":"2025-03-26"`},
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`, `"protocolVersion":"2025-06-18"`},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, `"name":"echo"`},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`, `"isError":false`},
		{`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail"}}`, `"text":"nope"`},
		{`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"missing"}}`, `"code":-32602`},
		{`{"jsonrpc":"2.0","id":6,"method":"nothing"}`, `"code":-32601`},
		{`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":7,"method":"ping"}]`, `[{"jsonrpc":"2.0","id":7,"result":{}}]`},
	} {
		code, body := post(http.MethodPost, c.body)
		if code != http.StatusOK || !strings.Contains(body, c.want) {
			t.Errorf("%s → %d %s, want %s", c.body, code, body, c.want)
		}
	}
	if code, _ := post(http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted {
		t.Errorf("a notification got %d", code)
	}
	if code, _ := post(http.MethodGet, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET got %d", code)
	}
	s.Revoke(ep.Token)
	if code, _ := post(http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != http.StatusUnauthorized {
		t.Errorf("a revoked token got %d", code)
	}
}
