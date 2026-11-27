package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/protocol"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("WADA_TEST_HARNESS")
	if mode == "" {
		return
	}
	switch mode {
	case "acp":
		runHelperACP()
	case "claude":
		runHelperClaude()
	case "claude-interrupt":
		runHelperClaudeInterrupt()
	case "acp-cancel":
		runHelperACPCancel()
	case "codex":
		runHelperCodex(os.Args)
	case "hang":
		// Sleep rather than block forever: the runtime would end a process
		// whose goroutines are all blocked.
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}

func runHelperACP() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var env struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				MCPServers []any `json:"mcpServers"`
			} `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &env) != nil {
			continue
		}
		switch env.Method {
		case "initialize":
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}`+"\n", string(env.ID))
		case "session/new":
			if env.Params.MCPServers == nil {
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"mcpServers is required"}}`+"\n", string(env.ID))
				continue
			}
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"acp_1"}}`+"\n", string(env.ID))
		case "session/prompt":
			fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"bash","kind":"execute"}}}`)
			fmt.Println(`{"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"toolCall":{"toolCallId":"tc1","title":"bash","kind":"execute"},"options":[{"optionId":"allow_once","kind":"allow_once"},{"optionId":"reject_once","kind":"reject_once"}]}}`)
			// Wait for permission response on stdin.
			if sc.Scan() {
				fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"tc1","title":"bash","kind":"execute","status":"completed","rawOutput":"ok"}}}`)
				fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ACP done."}}}}`)
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", string(env.ID))
			}
		case "session/cancel":
			return
		}
	}
}

func runHelperClaude() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var frame struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &frame) != nil {
			continue
		}
		if frame.Type == "user" {
			fmt.Println(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"foo.go"}}]}}`)
			fmt.Println(`{"type":"control_request","request_id":"cr_1","request":{"subtype":"can_use_tool","tool_name":"Edit","input":{"file_path":"foo.go"}}}`)
			if sc.Scan() {
				fmt.Println(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"edited foo.go"}]}}`)
				fmt.Println(`{"type":"assistant","message":{"content":[{"type":"text","text":"Claude done."}]}}`)
				fmt.Println(`{"type":"result","is_error":false}`)
			}
		}
	}
}

// runHelperClaudeInterrupt starts a turn on every prompt and ends it only when
// it is interrupted.
func runHelperClaudeInterrupt() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var frame struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(sc.Bytes(), &frame) != nil {
			continue
		}
		switch {
		case frame.Type == "user":
			fmt.Println(`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`)
		case frame.Type == "control_request" && frame.Request.Subtype == "interrupt":
			fmt.Printf(`{"type":"control_response","response":{"subtype":"success","request_id":%q}}`+"\n", frame.RequestID)
			fmt.Println(`{"type":"result","is_error":true,"subtype":"error_during_execution"}`)
		}
	}
}

// runHelperACPCancel answers each prompt only when it is cancelled.
func runHelperACPCancel() {
	sc := bufio.NewScanner(os.Stdin)
	var prompt json.RawMessage
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
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"acp_1"}}`+"\n", string(env.ID))
		case "session/prompt":
			prompt = env.ID
		case "session/cancel":
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"cancelled"}}`+"\n", string(prompt))
		}
	}
}

func runHelperCodex(args []string) {
	if len(args) < 2 || args[len(args)-2] != "--" {
		fmt.Println(`{"type":"item.completed","item":{"id":"m0","type":"agent_message","text":"prompt was not passed after --"}}`)
		fmt.Println(`{"type":"turn.completed"}`)
		return
	}
	isResume := false
	for _, a := range args {
		if a == "resume" {
			isResume = true
			break
		}
	}
	if !isResume {
		fmt.Println(`{"type":"thread.started","thread_id":"thr_abc"}`)
	}
	fmt.Println(`{"type":"item.started","item":{"id":"cmd1","type":"command_execution","command":"go test ./..."}}`)
	fmt.Println(`{"type":"item.completed","item":{"id":"cmd1","type":"command_execution","command":"go test ./...","aggregated_output":"PASS","exit_code":0,"status":"completed"}}`)
	if isResume {
		fmt.Println(`{"type":"item.completed","item":{"id":"m2","type":"agent_message","text":"Codex resumed."}}`)
	} else {
		fmt.Println(`{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"Codex done."}}`)
	}
	fmt.Println(`{"type":"turn.completed"}`)
}

func helperOpts(t *testing.T, id, harnessKind string) SessionOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return SessionOptions{
		SessionID: id,
		Directory: t.TempDir(),
		Mode:      "ask",
		Command:   exe,
		Args:      []string{"-test.run=TestHelperProcess"},
		Env:       append(os.Environ(), "WADA_TEST_HARNESS="+harnessKind),
	}
}

func TestACPAdapterStreamingAndPermissionReply(t *testing.T) {
	p := NewACPProvider(ACPConfig{ID: "acp-test", Name: "ACP Test"})
	sess, err := p.Start(t.Context(), helperOpts(t, "s_acp", "acp"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.Send(t.Context(), "run bash"); err != nil {
		t.Fatal(err)
	}

	var gotText strings.Builder
	var succeeded bool
	deadline := time.After(5 * time.Second)
	for !succeeded {
		select {
		case ev := <-sess.Events():
			switch ev.Kind {
			case protocol.EventApprovalRequested:
				if ev.Approval == nil || ev.Approval.Kind != protocol.ToolShell {
					t.Fatalf("unexpected approval = %+v", ev.Approval)
				}
				if err := sess.Reply(t.Context(), ev.Approval.ID, protocol.DecisionOnce); err != nil {
					t.Fatal(err)
				}
			case protocol.EventTextDelta:
				gotText.WriteString(ev.Text.Delta)
			case protocol.EventTurnSucceeded:
				succeeded = true
			case protocol.EventTurnFailed:
				t.Fatalf("turn failed: %s", ev.Error)
			case protocol.EventTextStarted, protocol.EventTextEnded, protocol.EventToolStarted,
				protocol.EventToolCalled, protocol.EventToolFinished, protocol.EventInteractionRequested,
				protocol.EventTurnStarted, protocol.EventTurnInterrupted, protocol.EventModelChanged,
				protocol.EventSessionRenamed, protocol.EventSessionDeleted, protocol.EventModelsChanged,
				protocol.EventProviderAuth, protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
			}
		case <-deadline:
			t.Fatal("timed out waiting for ACP turn")
		}
	}
	if gotText.String() != "ACP done." {
		t.Fatalf("text = %q, want %q", gotText.String(), "ACP done.")
	}
}

func TestClaudeAdapterStreamingAndControlReply(t *testing.T) {
	p := NewClaudeProvider(ClaudeConfig{ID: "claude-test", Name: "Claude Test"})
	sess, err := p.Start(t.Context(), helperOpts(t, "s_claude", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.Send(t.Context(), "edit foo.go"); err != nil {
		t.Fatal(err)
	}

	var gotText strings.Builder
	var succeeded bool
	deadline := time.After(5 * time.Second)
	for !succeeded {
		select {
		case ev := <-sess.Events():
			switch ev.Kind {
			case protocol.EventApprovalRequested:
				if ev.Approval == nil || ev.Approval.Kind != protocol.ToolEdit {
					t.Fatalf("unexpected approval = %+v", ev.Approval)
				}
				if err := sess.Reply(t.Context(), ev.Approval.ID, protocol.DecisionOnce); err != nil {
					t.Fatal(err)
				}
			case protocol.EventTextDelta:
				gotText.WriteString(ev.Text.Delta)
			case protocol.EventTurnSucceeded:
				succeeded = true
			case protocol.EventTurnFailed:
				t.Fatalf("turn failed: %s", ev.Error)
			case protocol.EventTextStarted, protocol.EventTextEnded, protocol.EventToolStarted,
				protocol.EventToolCalled, protocol.EventToolFinished, protocol.EventInteractionRequested,
				protocol.EventTurnStarted, protocol.EventTurnInterrupted, protocol.EventModelChanged,
				protocol.EventSessionRenamed, protocol.EventSessionDeleted, protocol.EventModelsChanged,
				protocol.EventProviderAuth, protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
			}
		case <-deadline:
			t.Fatal("timed out waiting for Claude turn")
		}
	}
	if gotText.String() != "Claude done." {
		t.Fatalf("text = %q, want %q", gotText.String(), "Claude done.")
	}
}

func TestCodexAdapterTurnAndResume(t *testing.T) {
	p := NewCodexProvider(CodexConfig{ID: "codex-test", Name: "Codex Test"})
	sess, err := p.Start(t.Context(), helperOpts(t, "s_codex", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	runTurn := func(prompt, want string) {
		if err := sess.Send(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-sess.Events():
				switch ev.Kind {
				case protocol.EventTextDelta:
					text.WriteString(ev.Text.Delta)
				case protocol.EventTurnSucceeded:
					if text.String() != want {
						t.Fatalf("got %q, want %q", text.String(), want)
					}
					return
				case protocol.EventTurnFailed:
					t.Fatalf("turn failed: %s", ev.Error)
				case protocol.EventTextStarted, protocol.EventTextEnded, protocol.EventToolStarted,
					protocol.EventToolCalled, protocol.EventToolFinished, protocol.EventApprovalRequested,
					protocol.EventInteractionRequested, protocol.EventTurnStarted, protocol.EventTurnInterrupted,
					protocol.EventModelChanged, protocol.EventSessionRenamed, protocol.EventSessionDeleted,
					protocol.EventModelsChanged, protocol.EventProviderAuth, protocol.EventWorkerUpdated,
					protocol.EventTerminalUpdated:
				}
			case <-deadline:
				t.Fatal("timed out waiting for Codex turn")
			}
		}
	}

	runTurn("first", "Codex done.")
	runTurn("--second -m o3", "Codex resumed.")
}

func TestManagedProcessKillCancelsHangingProcessTree(t *testing.T) {
	setAbortGrace(t, 100*time.Millisecond)
	p := NewClaudeProvider(ClaudeConfig{ID: "hang", Name: "Hang"})
	sess, err := p.Start(t.Context(), helperOpts(t, "s_hang", "hang"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Send(t.Context(), "hang forever"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			if ev.Kind == protocol.EventTurnInterrupted {
				_ = sess.Close()
				return
			}
		case <-deadline:
			_ = sess.Close()
			t.Fatal("expected TurnInterrupted after Abort")
		}
	}
}

func setAbortGrace(t *testing.T, d time.Duration) {
	t.Helper()
	prev := abortGrace
	abortGrace = d
	t.Cleanup(func() { abortGrace = prev })
}

// waitFor returns the first event of kind, failing on a failed turn or timeout.
func waitFor(t *testing.T, sess Session, kind protocol.EventKind) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("events closed waiting for %s", kind)
			}
			if ev.Kind == kind {
				return
			}
			if ev.Kind == protocol.EventTurnFailed {
				t.Fatalf("turn failed waiting for %s: %s", kind, ev.Error)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func TestAbortInterruptsTurnAndKeepsSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(*testing.T) (Session, error)
	}{
		{"claude", func(t *testing.T) (Session, error) {
			return NewClaudeProvider(ClaudeConfig{ID: "c"}).Start(t.Context(), helperOpts(t, "s_ci", "claude-interrupt"))
		}},
		{"acp", func(t *testing.T) (Session, error) {
			return NewACPProvider(ACPConfig{ID: "a"}).Start(t.Context(), helperOpts(t, "s_ac", "acp-cancel"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := tc.start(t)
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			for turn := range 2 {
				if err := sess.Send(t.Context(), "work"); err != nil {
					t.Fatalf("send %d: %v", turn, err)
				}
				waitFor(t, sess, protocol.EventTurnStarted)
				if err := sess.Abort(t.Context()); err != nil {
					t.Fatal(err)
				}
				waitFor(t, sess, protocol.EventTurnInterrupted)
			}
		})
	}
}

func TestSendOnClosedSessionIsNotSent(t *testing.T) {
	sess, err := NewClaudeProvider(ClaudeConfig{ID: "c"}).Start(t.Context(), helperOpts(t, "s_closed", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	if err := sess.Send(t.Context(), "hi"); !errors.Is(err, ErrNotSent) {
		t.Fatalf("Send after Close = %v, want ErrNotSent", err)
	}
}

func TestACPHandshakeTimesOut(t *testing.T) {
	prev := acpHandshakeTimeout
	acpHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { acpHandshakeTimeout = prev })
	start := time.Now()
	_, err := NewACPProvider(ACPConfig{ID: "a"}).Start(t.Context(), helperOpts(t, "s_hs", "hang"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("handshake took %v", time.Since(start))
	}
}

func TestClaudeArgsSkipPermissionsOnlyInFullMode(t *testing.T) {
	full := strings.Join(claudeArgs("full"), " ")
	if !strings.Contains(full, "--permission-mode bypassPermissions") || strings.Contains(full, "--permission-prompt-tool") {
		t.Fatalf("full mode args = %s", full)
	}
	ask := strings.Join(claudeArgs("ask"), " ")
	if !strings.Contains(ask, "--permission-prompt-tool stdio") || strings.Contains(ask, "bypassPermissions") {
		t.Fatalf("ask mode args = %s", ask)
	}
}
