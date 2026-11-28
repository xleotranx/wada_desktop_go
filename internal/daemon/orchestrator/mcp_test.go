package orchestrator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/worktree"
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
