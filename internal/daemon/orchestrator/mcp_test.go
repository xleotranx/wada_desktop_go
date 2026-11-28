package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

func TestWorkersCallTheChiefToolsOverMCP(t *testing.T) {
	dataDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	reg := harness.NewRegistry()
	var started []harness.SessionOptions
	reg.Register(recordingProvider{start: func(opts harness.SessionOptions) { started = append(started, opts) }})
	o, err := New(t.Context(), Options{DataDir: dataDir, PiG: rt, Worktrees: worktree.New(dataDir), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	go func() {
		for range o.Events() {
		}
	}()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := o.spawnWorker(t.Context(), spawnWorkerArgs{ParentSession: "chief", Provider: "scripted", Directory: folder})
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].MCP == nil {
		t.Fatalf("the worker was started without the MCP server: %+v", started)
	}
	ep := *started[0].MCP
	post := func(token, body string) (int, map[string]any) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, ep.URL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	if code, _ := post("wrong", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusUnauthorized {
		t.Fatalf("a wrong token got %d", code)
	}
	_, list := post(ep.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	b, _ := json.Marshal(list)
	if !bytes.Contains(b, []byte(`"spawn_agent"`)) || !bytes.Contains(b, []byte(`"inputSchema"`)) {
		t.Fatalf("tools/list = %s", b)
	}

	// The worker's own spawn is its child, in ask mode like itself.
	_, call := post(ep.Token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"spawn_agent","arguments":{"provider":"scripted","name":"child","mode":"full"}}}`)
	result, _ := call["result"].(map[string]any)
	if result == nil || result["isError"] != false {
		t.Fatalf("spawn_agent over MCP = %+v", call)
	}
	if len(started) != 2 || started[1].Mode != "ask" || started[1].Directory != folder {
		t.Fatalf("child started = %+v", started)
	}
	children, err := o.Workers(t.Context(), parent.ID)
	if err != nil || len(children) != 1 {
		t.Fatal("the worker's spawn isn't its child")
	}

	if err := o.Delete(t.Context(), parent.ID); err != nil {
		t.Fatal(err)
	}
	if o.getWorker(children[0].ID) != nil {
		t.Fatal("the stopped worker's child still runs")
	}
	if code, _ := post(ep.Token, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`); code != http.StatusUnauthorized {
		t.Fatalf("a stopped worker's token still works: %d", code)
	}
}

func TestProfilesStartWorkers(t *testing.T) {
	dataDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"),
		[]byte(`{"reviewer":{"provider":"scripted","mode":"full","instructions":"Review only."},"chat":{"instructions":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := harness.NewRegistry()
	var started []harness.SessionOptions
	sess := &textSession{events: make(chan protocol.Event, 4)}
	reg.Register(sessionProvider{sess: sess, start: func(opts harness.SessionOptions) { started = append(started, opts) }})
	o, err := New(t.Context(), Options{DataDir: dataDir, PiG: rt, Worktrees: worktree.New(dataDir), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	go func() {
		for range o.Events() {
		}
	}()
	folder := t.TempDir()
	var spawn pig.Tool
	for _, tool := range o.ChiefTools(func() pig.ChiefContext { return pig.ChiefContext{SessionID: "parent", Dir: folder} }) {
		if tool.Name == "spawn_agent" {
			spawn = tool
		}
	}
	if _, err := spawn.Execute(t.Context(), []byte(`{"profile":"chat","name":"c"}`)); err == nil {
		t.Fatal("spawned an embedded profile as a worker")
	}
	v, err := spawn.Execute(t.Context(), []byte(`{"profile":"reviewer","name":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	// The profile's full mode can't raise an ask conversation's worker.
	if len(started) != 1 || started[0].Mode != "ask" {
		t.Fatalf("started = %+v", started)
	}
	id := v.(protocol.WorkerInfo).ID
	for _, text := range []string{"Look at main.go", "Now the tests"} {
		if _, err := o.Send(t.Context(), protocol.SendRequest{Session: id, Text: text}); err != nil {
			t.Fatal(err)
		}
		sess.events <- protocol.Event{Kind: protocol.EventTurnSucceeded}
		waitIdle(t, o, id)
	}
	if len(sess.sent) != 2 || sess.sent[0] != "Review only.\n\n---\n\nLook at main.go" || sess.sent[1] != "Now the tests" {
		t.Fatalf("sent = %q", sess.sent)
	}
}

// textSession records what it is sent.
type textSession struct {
	scriptedSession
	events chan protocol.Event
	mu     sync.Mutex
	sent   []string
	close  sync.Once
}

func (s *textSession) Send(_ context.Context, text string) error {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.mu.Unlock()
	return nil
}
func (s *textSession) Events() <-chan protocol.Event { return s.events }
func (s *textSession) Close() error {
	s.close.Do(func() { close(s.events) })
	return nil
}

type sessionProvider struct {
	sess  harness.Session
	start func(harness.SessionOptions)
}

func (sessionProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{ID: "scripted", Name: "Scripted"}
}

func (p sessionProvider) Start(_ context.Context, opts harness.SessionOptions) (harness.Session, error) {
	p.start(opts)
	return p.sess, nil
}

func waitIdle(t *testing.T, o *Orchestrator, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if w := o.getWorker(id); w != nil && w.snapshot().Status != protocol.WorkerRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker's turn didn't end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
