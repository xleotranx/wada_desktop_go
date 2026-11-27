package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/server"
	"wada-desktop-go/internal/daemon/terminal"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

func TestHelperTermWorker(t *testing.T) {
	if os.Getenv("WADA_ORCH_TERM_WORKER") != "1" {
		return
	}
	dir := os.Getenv("WADA_ORCH_TERM_DIR")
	if err := terminal.RunWorker(context.Background(), dir); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestWebSocketBinaryTerminalLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	termSup := terminal.NewSupervisor(terminal.SupervisorOptions{
		DataDir:     dataDir,
		WorkerBin:   exe,
		WorkerArgs:  []string{"-test.run=TestHelperTermWorker"},
		WorkerEnv:   append(os.Environ(), "WADA_ORCH_TERM_WORKER=1", "WADA_ORCH_TERM_DIR="+dataDir),
		KeepOnClose: false,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = termSup.ShutdownWorker(ctx)
		cancel()
	})

	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := New(t.Context(), Options{
		DataDir:   dataDir,
		PiG:       rt,
		Worktrees: worktree.New(dataDir),
		Registry:  harness.NewRegistry(),
		Terminals: termSup,
	})
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}

	srv, err := server.New(t.Context(), server.Config{
		DataDir: dataDir,
		Listen:  "127.0.0.1:0",
		Version: "term-ws-test",
		Backend: orch,
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

	info, err := cli.CreateTerminal(t.Context(), protocol.TerminalCreateRequest{
		Name:      "ws-shell",
		Directory: t.TempDir(),
		Shell:     "/bin/sh",
		Cols:      80,
		Rows:      24,
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := cli.AttachTerminal(t.Context(), protocol.TerminalAttachRequest{
		ID:   info.ID,
		Cols: 90,
		Rows: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if err := stream.Resize(100, 32); err != nil {
		t.Fatal(err)
	}
	if err := stream.WriteInput([]byte("echo WS_TERM_BINARY_OK\n")); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(out.String(), "WS_TERM_BINARY_OK") {
		select {
		case chunk := <-stream.Output():
			out.Write(chunk)
		case <-deadline:
			t.Fatalf("did not receive binary terminal output: %q", out.String())
		}
	}

	capResp, err := cli.CaptureTerminal(t.Context(), info.ID, 50)
	if err != nil || !strings.Contains(capResp.Text, "WS_TERM_BINARY_OK") {
		t.Fatalf("capture = %+v, err=%v", capResp, err)
	}

	list, err := cli.Terminals(t.Context())
	if err != nil || len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("list = %+v, err=%v", list, err)
	}

	if err := cli.KillTerminal(t.Context(), info.ID); err != nil {
		t.Fatal(err)
	}
}
