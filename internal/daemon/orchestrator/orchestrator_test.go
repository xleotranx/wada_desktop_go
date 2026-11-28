package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/server"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/daemon/workspaces"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

func TestHelperProcess(t *testing.T) {
	role := os.Getenv("WADA_ORCH_HELPER")
	if role == "" {
		return
	}
	switch role {
	case "claude":
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			var frame struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(sc.Bytes(), &frame) != nil || frame.Type != "user" {
				continue
			}
			fmt.Println(`{"type":"control_request","request_id":"req_claude_1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"worker_a.txt"}}}`)
			if sc.Scan() {
				_ = os.WriteFile("worker_a.txt", []byte("written by claude worker\n"), 0o600)
				fmt.Println(`{"type":"assistant","message":{"content":[{"type":"text","text":"Created worker_a.txt"}]}}`)
				fmt.Println(`{"type":"result","is_error":false}`)
			}
		}
	case "acp":
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			var env struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(sc.Bytes(), &env) != nil {
				continue
			}
			switch env.Method {
			case "initialize":
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}`+"\n", string(env.ID))
			case "session/new":
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"acp_w2"}}`+"\n", string(env.ID))
			case "session/prompt":
				_ = os.WriteFile("worker_b.txt", []byte("written by acp worker\n"), 0o600)
				fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Created worker_b.txt"}}}}`)
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", string(env.ID))
			}
		}
	}
	os.Exit(0)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.email", "wada@example.com")
	runGit("config", "user.name", "Wada Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "init")
	return dir
}

func TestChiefAgentOrchestratesWorktreesAndMultipleHarnessesOverWS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	repoDir := initGitRepo(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	reg := harness.NewRegistry()
	reg.Register(harness.NewClaudeProvider(harness.ClaudeConfig{
		ID:     "claude-mock",
		Name:   "Mock Claude",
		Binary: exe,
		Args:   []string{"-test.run=TestHelperProcess"},
	}))
	reg.Register(harness.NewACPProvider(harness.ACPConfig{
		ID:     "acp-mock",
		Name:   "Mock ACP",
		Binary: exe,
		Args:   []string{"-test.run=TestHelperProcess"},
	}))

	// Inject per-provider helper env via a tiny wrapper provider so both work cleanly in the same test.
	claudeProv, _ := reg.Get("claude-mock")
	acpProv, _ := reg.Get("acp-mock")
	reg.Register(&envProvider{inner: claudeProv, env: "WADA_ORCH_HELPER=claude"})
	reg.Register(&envProvider{inner: acpProv, env: "WADA_ORCH_HELPER=acp"})

	wtMgr := worktree.New(dataDir)
	st, err := store.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	wsReg, err := workspaces.Open(t.Context(), st, workspaces.Options{Inbox: filepath.Join(dataDir, "workspaces", "inbox")})
	if err != nil {
		t.Fatal(err)
	}
	spaces := workspaces.NewService(wsReg, wtMgr)
	var orch *Orchestrator
	rt, err := pig.New(t.Context(), pig.Options{
		Root: filepath.Join(dataDir, "pig"),
		ChiefTools: func(getCtx func() pig.ChiefContext) []pig.Tool {
			return orch.ChiefTools(getCtx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}

	orch, err = New(t.Context(), Options{
		DataDir:    dataDir,
		PiG:        rt,
		Worktrees:  wtMgr,
		Workspaces: spaces,
		Registry:   reg,
	})
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}

	srv, err := server.New(t.Context(), server.Config{
		DataDir:    dataDir,
		Listen:     "127.0.0.1:0",
		Version:    "orch-test",
		Store:      st,
		Workspaces: spaces,
		Backend:    orch,
	})
	if err != nil {
		_ = orch.Close()
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		_ = orch.Close()
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	cli, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// Create two worktrees via PiG Chief Agent orchestration sequence using direct worktree + harness calls?
	// No: drive PiG Chief Agent via FauxProvider tool calls over real WebSocket RPC!
	project, err := cli.AddProject(t.Context(), repoDir)
	if err != nil {
		t.Fatal(err)
	}
	wtA, err := cli.CreateWorkspace(t.Context(), protocol.WorkspaceCreateRequest{Project: project.Project.ID,
		Isolation: protocol.IsolationWorktree, Slug: "part-a"})
	if err != nil {
		t.Fatal(err)
	}
	wtB, err := cli.CreateWorkspace(t.Context(), protocol.WorkspaceCreateRequest{Project: project.Project.ID,
		Isolation: protocol.IsolationWorktree, Slug: "part-b"})
	if err != nil {
		t.Fatal(err)
	}

	chiefSess, err := cli.Create(t.Context(), protocol.CreateSessionRequest{
		Directory: repoDir,
		Title:     "Chief orchestration",
		Model:     protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}

	subID, ch, err := cli.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Unsubscribe(context.Background(), subID) }()

	// PiG Chief spawns Claude mock in wtA, ACP mock in wtB, sends both tasks, waits, inspects diffs.
	// Using Dynamic step with simple JSON/state isn't needed—use two tool steps: spawn+send, then wait+inspect+reply.
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxToolCall("list_providers", map[string]any{}, nil),
			ai.FauxToolCall("list_workspaces", map[string]any{}, nil),
			ai.FauxToolCall("spawn_agent", map[string]any{
				"provider":     "claude-mock",
				"name":         "Claude Worker A",
				"workspace_id": wtA.ID,
			}, nil),
			ai.FauxToolCall("spawn_agent", map[string]any{
				"provider":     "acp-mock",
				"name":         "ACP Worker B",
				"workspace_id": wtB.ID,
			}, nil),
		}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Spawned workers step.")}},
	})

	if _, err := cli.Send(t.Context(), protocol.SendRequest{
		Session: chiefSess.ID,
		Mode:    "full",
		Text:    "Check harnesses and spawn workers",
	}); err != nil {
		t.Fatal(err)
	}

	waitChiefTurn := func() {
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ev := <-ch:
				if ev.Session == chiefSess.ID && ev.Kind == protocol.EventTurnSucceeded {
					return
				}
				if ev.Session == chiefSess.ID && ev.Kind == protocol.EventTurnFailed {
					t.Fatalf("chief turn failed: %s", ev.Error)
				}
			case <-deadline:
				t.Fatal("timed out waiting for chief turn")
			}
		}
	}
	waitChiefTurn()

	workers, err := cli.Workers(t.Context(), chiefSess.ID)
	if err != nil || len(workers) != 2 {
		t.Fatalf("expected 2 workers under chief, got %+v, err=%v", workers, err)
	}
	var claudeID, acpID string
	for _, w := range workers {
		switch w.Provider {
		case "claude-mock":
			claudeID = w.ID
		case "acp-mock":
			acpID = w.ID
		}
	}
	if claudeID == "" || acpID == "" {
		t.Fatalf("missing spawned worker IDs: %+v", workers)
	}

	// Next turn: Chief sends prompts to both workers, then in the event loop we answer the forwarded
	// Claude approval on Chief's session while Chief waits for both workers!
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxToolCall("send_agent", map[string]any{"worker_id": claudeID, "prompt": "Write A"}, nil),
			ai.FauxToolCall("send_agent", map[string]any{"worker_id": acpID, "prompt": "Write B"}, nil),
		}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxToolCall("wait_agent", map[string]any{"worker_ids": []string{claudeID, acpID}, "timeout_seconds": 10}, nil),
		}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxToolCall("inspect_agent_diff", map[string]any{"worker_id": claudeID}, nil),
			ai.FauxToolCall("inspect_agent_diff", map[string]any{"worker_id": acpID}, nil),
		}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Both workers finished with isolated diffs.")}},
	})

	if _, err := cli.Send(t.Context(), protocol.SendRequest{
		Session: chiefSess.ID,
		Mode:    "full",
		Text:    "Run workers and inspect diffs",
	}); err != nil {
		t.Fatal(err)
	}

	sawApproval := false
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == protocol.EventApprovalRequested && ev.Session == chiefSess.ID && ev.Approval != nil {
				sawApproval = true
				if err := cli.Reply(t.Context(), protocol.ReplyRequest{
					Session:  ev.Approval.Session,
					Approval: ev.Approval.ID,
					Decision: protocol.DecisionOnce,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if ev.Session == chiefSess.ID && ev.Kind == protocol.EventTurnSucceeded {
				goto verified
			}
			if ev.Session == chiefSess.ID && ev.Kind == protocol.EventTurnFailed {
				t.Fatalf("chief turn 2 failed: %s", ev.Error)
			}
		case <-deadline:
			t.Fatal("timed out waiting for orchestrated worker completion")
		}
	}

verified:
	if !sawApproval {
		t.Fatal("expected forwarded approval from Claude worker on Chief session")
	}

	diffA, err := cli.Changes(t.Context(), claudeID)
	if err != nil || len(diffA) != 1 || diffA[0].Path != "worker_a.txt" {
		t.Fatalf("claude diff = %+v, err=%v", diffA, err)
	}
	diffB, err := cli.Changes(t.Context(), acpID)
	if err != nil || len(diffB) != 1 || diffB[0].Path != "worker_b.txt" {
		t.Fatalf("acp diff = %+v, err=%v", diffB, err)
	}
	// The main checkout stayed clean, and each workspace shows its work against its base.
	refs, err := cli.WorkspaceRefs(t.Context(), project.Project.ID)
	if err != nil || len(refs.Worktrees) != 3 || refs.Worktrees[0].Dirty {
		t.Fatalf("expected clean main checkout + 2 worktrees, got %+v, err=%v", refs.Worktrees, err)
	}
	baseA, err := cli.WorkspaceChanges(t.Context(), wtA.ID)
	if err != nil || len(baseA) != 1 || baseA[0].Path != "worker_a.txt" {
		t.Fatalf("workspace A against its base = %+v, err=%v", baseA, err)
	}
	// Archiving refuses to lose B's work until forced, then removes its worktree.
	if err := cli.ArchiveWorkspace(t.Context(), wtB.ID, false); err == nil {
		t.Fatal("archived a worktree with uncommitted work")
	}
	if err := cli.ArchiveWorkspace(t.Context(), wtB.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtB.WorktreeRoot); !os.IsNotExist(err) {
		t.Fatalf("the archived worktree is still on disk: %v", err)
	}
	restored, err := cli.RestoreWorkspace(t.Context(), wtB.ID)
	if err != nil || restored.Archived != 0 {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(wtB.WorktreeRoot, "README.md")); err != nil {
		t.Fatalf("the restored worktree is missing: %v", err)
	}
}

type envProvider struct {
	inner harness.Provider
	env   string
}

func (p *envProvider) Info() protocol.HarnessInfo { return p.inner.Info() }

func (p *envProvider) Start(ctx context.Context, opts harness.SessionOptions) (harness.Session, error) {
	opts.Env = append(os.Environ(), p.env)
	return p.inner.Start(ctx, opts)
}
