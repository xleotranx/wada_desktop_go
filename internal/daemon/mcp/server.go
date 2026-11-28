// Package mcp serves tools to external coding harnesses over the Model
// Context Protocol's streamable HTTP transport, on a loopback port only.
//
// Each caller gets its own bearer token, and with it its own tools: a token
// names one worker, and the tools act as that worker. The server answers
// every request with one JSON response and opens no event streams.
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Name is the server's name in a harness's MCP configuration.
const Name = "wada"

// protocolVersions are the MCP revisions the server speaks, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one tool a token may call.
type Tool struct {
	Name        string
	Description string
	// InputSchema is the arguments' JSON Schema.
	InputSchema map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
}

// Endpoint is what a harness needs to reach the server as one caller.
type Endpoint struct {
	URL   string
	Token string
}

// Server is a loopback MCP server.
type Server struct {
	version string
	ln      net.Listener
	http    *http.Server
	done    chan struct{}

	mu     sync.Mutex
	tokens map[string][]Tool
}

// Start listens on a free loopback port and serves until Close.
func Start(version string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{version: version, ln: ln, done: make(chan struct{}), tokens: map[string][]Tool{}}
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer close(s.done)
		_ = s.http.Serve(ln)
	}()
	return s, nil
}

// URL is the endpoint harnesses post to.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() + "/mcp" }

// Close stops serving; calls in flight are cancelled.
func (s *Server) Close() error {
	err := s.http.Close()
	<-s.done
	return err
}

// Register gives a new token the tools and returns where to reach them.
func (s *Server) Register(tools []Tool) (Endpoint, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return Endpoint{}, err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = slices.Clone(tools)
	s.mu.Unlock()
	return Endpoint{URL: s.URL(), Token: tok}, nil
}

// Revoke forgets a token; its later requests are refused.
func (s *Server) Revoke(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

func (s *Server) tools(r *http.Request) ([]Tool, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, ok := s.tokens[tok]
	return tools, ok
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const maxBody = 4 << 20

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	tools, ok := s.tools(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		// No server-to-client stream: every answer comes with its request.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		http.Error(w, "request too large or unreadable", http.StatusBadRequest)
		return
	}
	body = []byte(strings.TrimSpace(string(body)))
	var out any
	if len(body) > 0 && body[0] == '[' {
		var batch []request
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			return
		}
		var replies []response
		for _, req := range batch {
			if res, ok := s.handle(r.Context(), tools, req); ok {
				replies = append(replies, res)
			}
		}
		if len(replies) > 0 {
			out = replies
		}
	} else {
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			return
		}
		if res, ok := s.handle(r.Context(), tools, req); ok {
			out = res
		}
	}
	if out == nil {
		// Only notifications and responses: accepted, nothing to say.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handle answers one message; a notification or a response gets no answer.
func (s *Server) handle(ctx context.Context, tools []Tool, req request) (response, bool) {
	if len(req.ID) == 0 || string(req.ID) == "null" || req.Method == "" {
		return response{}, false
	}
	res := response{JSONRPC: "2.0", ID: req.ID}
	result, err := s.call(ctx, tools, req)
	if err != nil {
		var re *rpcError
		if !errors.As(err, &re) {
			re = &rpcError{-32603, err.Error()}
		}
		res.Error = re
		return res, true
	}
	res.Result = result
	return res, true
}

func (e *rpcError) Error() string { return e.Message }

func (s *Server) call(ctx context.Context, tools []Tool, req request) (any, error) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": Name, "version": s.version},
			"instructions": "Wada's workspace tools: create Git worktree workspaces in this project, spawn other " +
				"coding agents in them, send them work, wait for them and review their diffs.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		i := slices.IndexFunc(tools, func(t Tool) bool { return t.Name == p.Name })
		if i < 0 {
			return nil, &rpcError{-32602, fmt.Sprintf("unknown tool %q", p.Name)}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		v, err := tools[i].Call(ctx, args)
		if err != nil {
			// A failed tool is a result the model reads, not a protocol error.
			return toolResult(err.Error(), true), nil
		}
		text, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(string(text), false), nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
