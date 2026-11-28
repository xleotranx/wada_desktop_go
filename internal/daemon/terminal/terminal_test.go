package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/protocol"
)

func TestHelperWorkerProcess(t *testing.T) {
	if os.Getenv("WADA_TERM_HELPER_WORKER") != "1" {
		return
	}
	dir := os.Getenv("WADA_TERM_HELPER_DIR")
	if err := RunWorker(context.Background(), dir); err != nil {
		fmt.Fprintf(os.Stderr, "helper worker error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestVTBufferANSIAndScrollback(t *testing.T) {
	vt := NewVTBuffer(20, 4)
	for i := 1; i <= 15; i++ {
		vt.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	capText := vt.CaptureText(20)
	if !strings.Contains(capText, "line-01") || !strings.Contains(capText, "line-15") {
		t.Fatalf("unexpected CaptureText:\n%s", capText)
	}
	if last5 := vt.CaptureText(3); strings.Contains(last5, "line-01") {
		t.Fatalf("expected truncated CaptureText(3), got:\n%s", last5)
	}

	// Verify ANSI cursor overwrite & line erase.
	vt.Write([]byte("\x1b[2J\x1b[Hhello world\x1b[1;7H\x1b[0Kwada"))
	snap := vt.Snapshot()
	if !strings.Contains(string(snap), "hello wada") {
		t.Fatalf("snapshot missing overwritten line: %q", string(snap))
	}

	// Verify 10,000-line scrollback cap without panicking.
	vtBig := NewVTBuffer(40, 5)
	var batch strings.Builder
	for i := range 10050 {
		fmt.Fprintf(&batch, "row-%d\r\n", i)
	}
	vtBig.Write([]byte(batch.String()))
	out := vtBig.CaptureText(10010)
	if strings.Contains(out, "row-10\n") || !strings.Contains(out, "row-10049") {
		t.Fatalf("scrollback cap broken")
	}
}

func TestPTYInteractiveEchoAndResize(t *testing.T) {
	p, err := startPTY(StartOptions{
		Directory: t.TempDir(),
		Shell:     "/bin/sh",
		Cols:      80,
		Rows:      24,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Resize(100, 30); err != nil {
		t.Fatal(err)
	}

	outCh := make(chan string, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := p.Read(buf)
			if n > 0 {
				outCh <- string(buf[:n])
			}
			if rerr != nil {
				close(outCh)
				return
			}
		}
	}()

	if _, err := p.Write([]byte("echo WADA_PTY_OK\nexit 0\n")); err != nil {
		t.Fatal(err)
	}

	var combined strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-outCh:
			if !ok {
				if !strings.Contains(combined.String(), "WADA_PTY_OK") {
					t.Fatalf("missing PTY echo output: %q", combined.String())
				}
				return
			}
			combined.WriteString(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for PTY output: %q", combined.String())
		}
	}
}

func TestDetachedWorkerSurvivesSupervisorRestart(t *testing.T) {
	dataDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "WADA_TERM_HELPER_WORKER=1", "WADA_TERM_HELPER_DIR="+dataDir)

	sup1 := NewSupervisor(SupervisorOptions{
		DataDir:     dataDir,
		WorkerBin:   exe,
		WorkerArgs:  []string{"-test.run=TestHelperWorkerProcess"},
		WorkerEnv:   env,
		KeepOnClose: true,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = sup1.ShutdownWorker(ctx)
		cancel()
		_ = sup1.Close()
	})

	info, err := sup1.Create(t.Context(), protocol.TerminalCreateRequest{
		Name:      "persisted",
		Directory: t.TempDir(),
		Shell:     "/bin/sh",
		Cols:      80,
		Rows:      24,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid1, err := sup1.WorkerPID(t.Context())
	if err != nil || pid1 <= 0 {
		t.Fatalf("WorkerPID = %d, err=%v", pid1, err)
	}

	if err := sup1.Input(t.Context(), info.ID, []byte("echo WADA_SURVIVED_RESTART\n")); err != nil {
		t.Fatal(err)
	}

	// Wait until VTBuffer sees the echoed token.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		capResp, cerr := sup1.Capture(t.Context(), info.ID, 50)
		if cerr == nil && strings.Contains(capResp.Text, "WADA_SURVIVED_RESTART") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	// Close Supervisor #1 (KeepOnClose=true simulates wadad shutdown/restart).
	if err := sup1.Close(); err != nil {
		t.Fatal(err)
	}

	// Start Supervisor #2 and verify same worker PID, live terminal, and snapshot replay!
	sup2 := NewSupervisor(SupervisorOptions{
		DataDir:     dataDir,
		WorkerBin:   exe,
		WorkerArgs:  []string{"-test.run=TestHelperWorkerProcess"},
		WorkerEnv:   env,
		KeepOnClose: false,
	})
	defer sup2.Close()

	pid2, err := sup2.WorkerPID(t.Context())
	if err != nil || pid2 != pid1 {
		t.Fatalf("expected re-attach to same worker PID %d, got %d (err=%v)", pid1, pid2, err)
	}

	reInfo, snap, ch, detach, err := sup2.Attach(t.Context(), "test-client-2", protocol.TerminalAttachRequest{
		ID:   info.ID,
		Cols: 90,
		Rows: 28,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	if !reInfo.Running || !strings.Contains(string(snap), "WADA_SURVIVED_RESTART") {
		t.Fatalf("reattached terminal info=%+v snap=%q", reInfo, string(snap))
	}

	if err := sup2.Input(t.Context(), info.ID, []byte("echo SECOND_COMMAND_OK\n")); err != nil {
		t.Fatal(err)
	}
	var live strings.Builder
	deadline2 := time.After(5 * time.Second)
	for !strings.Contains(live.String(), "SECOND_COMMAND_OK") {
		select {
		case chunk := <-ch:
			live.Write(chunk)
		case <-deadline2:
			t.Fatalf("did not receive live output after reattach: %q", live.String())
		}
	}
}

func newHelperSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	dataDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "WADA_TERM_HELPER_WORKER=1", "WADA_TERM_HELPER_DIR="+dataDir)
	sup := NewSupervisor(SupervisorOptions{
		DataDir:    dataDir,
		WorkerBin:  exe,
		WorkerArgs: []string{"-test.run=TestHelperWorkerProcess"},
		WorkerEnv:  env,
	})
	t.Cleanup(func() { _ = sup.Close() })
	return sup
}

func waitForOutput(t *testing.T, ch <-chan []byte, token string) {
	t.Helper()
	var live strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(live.String(), token) {
		select {
		case chunk, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed before %q arrived: %q", token, live.String())
			}
			live.Write(chunk)
		case <-deadline:
			t.Fatalf("did not receive %q: %q", token, live.String())
		}
	}
}

func TestDetachKeepsOtherSubscribersStreaming(t *testing.T) {
	sup := newHelperSupervisor(t)
	info, err := sup.Create(t.Context(), protocol.TerminalCreateRequest{Directory: t.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, detachA, err := sup.Attach(t.Context(), "client-a", protocol.TerminalAttachRequest{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, chB, detachB, err := sup.Attach(t.Context(), "client-b", protocol.TerminalAttachRequest{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer detachB()

	detachA()
	detachA() // a repeated detach must not detach the remaining subscriber either
	if err := sup.Input(t.Context(), info.ID, []byte("echo STILL_STREAMING\n")); err != nil {
		t.Fatal(err)
	}
	waitForOutput(t, chB, "STILL_STREAMING")
}

func TestReconnectReattachesSubscriptions(t *testing.T) {
	sup := newHelperSupervisor(t)
	info, err := sup.Create(t.Context(), protocol.TerminalCreateRequest{Directory: t.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	_, _, ch, detach, err := sup.Attach(t.Context(), "client", protocol.TerminalAttachRequest{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer detach()

	sup.connMu.Lock()
	old := sup.conn
	sup.connMu.Unlock()
	_ = old.wc.conn.Close()
	<-old.done

	if err := sup.Input(t.Context(), info.ID, []byte("echo AFTER_RECONNECT\n")); err != nil {
		t.Fatal(err)
	}
	waitForOutput(t, ch, "AFTER_RECONNECT")
}

func TestExitEndsAttachedStreams(t *testing.T) {
	sup := newHelperSupervisor(t)
	info, err := sup.Create(t.Context(), protocol.TerminalCreateRequest{Directory: t.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	_, _, ch, detach, err := sup.Attach(t.Context(), "client", protocol.TerminalAttachRequest{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	if err := sup.Input(t.Context(), info.ID, []byte("echo LAST_WORDS; exit\n")); err != nil {
		t.Fatal(err)
	}
	// The output before the exit arrives, then the stream closes.
	var out strings.Builder
	deadline := time.After(10 * time.Second)
	for open := true; open; {
		select {
		case chunk, ok := <-ch:
			out.Write(chunk)
			open = ok
		case <-deadline:
			t.Fatalf("stream still open after the shell exited: %q", out.String())
		}
	}
	if !strings.Contains(out.String(), "LAST_WORDS") {
		t.Fatalf("output before the exit was lost: %q", out.String())
	}

	// Attaching to the exited terminal gives its snapshot and a closed stream.
	_, _, ch2, detach2, err := sup.Attach(t.Context(), "late", protocol.TerminalAttachRequest{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer detach2()
	select {
	case _, ok := <-ch2:
		if ok {
			t.Fatal("stream of an exited terminal delivered output")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream of an exited terminal stayed open")
	}
}

func TestVTBufferLeavingAltScreenAfterResize(t *testing.T) {
	for _, size := range [][2]int{{120, 20}, {40, 4}} {
		b := NewVTBuffer(80, 10)
		b.Write([]byte("hi\r\n\x1b[?1049h"))
		b.Resize(size[0], size[1])
		b.Write([]byte("\x1b[?1049l"))
		long := strings.Repeat("0123456789", 15)
		for range 30 {
			b.Write([]byte(long + "\r\n"))
		}
		if got := b.CaptureText(1000); !strings.HasPrefix(got, "hi\n") {
			t.Fatalf("%v: capture starts %q, want the main screen's first line", size, got[:min(len(got), 20)])
		}
	}
}

func TestPTYRunsACommandWithItsEnvironment(t *testing.T) {
	p, err := startPTY(StartOptions{
		Directory: t.TempDir(),
		Shell:     "/bin/sh",
		Cols:      80,
		Rows:      24,
		Env:       append(os.Environ(), "WADA_PORT=41000"),
		Command:   "echo port=$WADA_PORT; exit 3",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := p.Read(buf)
		out.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	code, _ := p.Wait()
	if !strings.Contains(out.String(), "port=41000") || code != 3 {
		t.Fatalf("output %q, exit %d", out.String(), code)
	}
}
