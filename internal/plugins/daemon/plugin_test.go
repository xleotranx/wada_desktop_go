package daemon

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/server"
	"wada-desktop-go/internal/plugintest"
	"wada-desktop-go/internal/protocol"
)

type testDaemonBackend struct {
	*pig.Runtime
	rejectText string
	attempts   atomic.Int32

	mu        sync.Mutex
	cancelled []string
}

func (b *testDaemonBackend) CancelProviderLogin(ctx context.Context, loginID string) error {
	b.mu.Lock()
	b.cancelled = append(b.cancelled, loginID)
	b.mu.Unlock()
	return b.Runtime.CancelProviderLogin(ctx, loginID)
}

func (b *testDaemonBackend) cancelledLogins() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.cancelled)
}

func (b *testDaemonBackend) Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	if b.rejectText != "" && req.Text == b.rejectText {
		b.attempts.Add(1)
		return "", fmt.Errorf("%w: scripted refusal", pig.ErrNotSent)
	}
	return b.Runtime.Send(ctx, req)
}

func waitFor(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func call[T any](t *testing.T, h *kerneltest.Harness, fn func(context.Context) (T, error)) T {
	t.Helper()
	done := make(chan struct{})
	var value T
	var err error
	go func() { value, err = fn(context.Background()); close(done) }()
	waitFor(t, h, "call", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func startDaemonHarness(t *testing.T, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *ai.FauxProviderHandle, *server.Server, *testDaemonBackend) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	daemonDir := t.TempDir()
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(daemonDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	backend := &testDaemonBackend{Runtime: rt, rejectText: "refuse"}
	srv, err := server.New(t.Context(), server.Config{
		DataDir:        daemonDir,
		Listen:         "127.0.0.1:0",
		Version:        "test-daemon",
		DesktopManaged: true,
		Backend:        backend,
	})
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	var p *Plugin
	host := &kittest.Host{Data: t.TempDir()}
	dialHook := func(ctx context.Context) (*client.Client, protocol.DaemonStatusResponse, error) {
		cli, err := client.DialLocal(ctx, daemonDir, client.Options{ClientType: "desktop", AppVersion: "test"})
		if err != nil {
			return nil, protocol.DaemonStatusResponse{}, err
		}
		st, err := cli.Status(ctx)
		if err != nil {
			_ = cli.Close()
			return nil, protocol.DaemonStatusResponse{}, err
		}
		return cli, st, nil
	}
	factory := func() kernel.Plugin {
		inst := &Plugin{dial: dialHook}
		p = inst
		return inst
	}
	factories := append([]kernel.Factory{host.New, factory}, extra...)
	h := kerneltest.New(t, factories...)
	waitFor(t, h, "daemon ready", func() bool { return p.Status().State == agent.Ready })
	return h, p, faux, srv, backend
}

func createChat(t *testing.T, h *kerneltest.Harness, p *Plugin, dir string) agent.Session {
	t.Helper()
	return call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Create(ctx, agent.CreateSession{
			Directory: dir,
			Title:     "Example",
			Model:     agent.ModelRef{Provider: "wada-test", ID: "test-model"},
		})
	})
}

func TestBackendContract(t *testing.T) {
	h, p, _, _, backend := startDaemonHarness(t)
	s := createChat(t, h, p, t.TempDir())
	plugintest.CheckBackend(t, plugintest.BackendCase{
		Harness:  h,
		PluginID: "daemon",
		Session:  s.ID,
		RejectSend: func() (string, func() int) {
			return "refuse", func() int { return int(backend.attempts.Load()) }
		},
	})
}

func TestSessionLifecycleChildrenAndDeletedTopic(t *testing.T) {
	h, p, _, _, _ := startDaemonHarness(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	if s.ID == "" {
		t.Fatal("missing session ID")
	}

	page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{})
	})
	if len(page.Sessions) != 1 || page.Sessions[0].ID != s.ID {
		t.Fatalf("sessions = %+v", page)
	}

	child := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Spawn(ctx, agent.Spawn{
			Parent:    s.ID,
			Name:      "Reviewer",
			Directory: dir,
			Policy:    agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}},
		})
	})
	if child.ID == s.ID {
		t.Fatal("child shares parent identity")
	}
	page = call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{})
	})
	if len(page.Sessions) != 1 {
		t.Fatalf("children leaked into top-level list: %+v", page)
	}

	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Rename(ctx, s.ID, "Renamed") })
	page = call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{Search: "Renamed"})
	})
	if len(page.Sessions) != 1 || page.Sessions[0].Title != "Renamed" {
		t.Fatalf("rename did not persist: %+v", page)
	}

	var deleted agent.SessionID
	kernel.On(h, agent.Deleted, func(id agent.SessionID) { deleted = id })
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Delete(ctx, s.ID) })
	waitFor(t, h, "deleted topic", func() bool { return deleted == s.ID })
}

func TestStreamingApprovalsChangesAndImageFit(t *testing.T) {
	h, p, faux, _, _ := startDaemonHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, dir)

	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "app.go", "content": "package app\n// via daemon\n"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Applied cleanly.")}},
	})

	var events []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		events = append(events, batch...)
	})

	disp := call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{
			ID:      "turn-req-1",
			Session: s.ID,
			Text:    "Update app.go",
			Files:   []agent.File{{Name: "note.txt", MIME: "text/plain", Data: []byte("attached")}},
		})
	})
	if disp != agent.SendAccepted {
		t.Fatalf("disposition = %v, want SendAccepted", disp)
	}

	approved := false
	succeeded := false
	waitFor(t, h, "approval and completion", func() bool {
		for _, ev := range events {
			if ev.Kind == agent.TurnFailed {
				t.Fatalf("turn failed: %s", ev.Error)
			}
			if ev.Kind == agent.ApprovalRequested && !approved {
				approved = true
				call(t, h, func(ctx context.Context) (struct{}, error) {
					return struct{}{}, p.Reply(ctx, agent.Reply{
						Session:  ev.Approval.Session,
						Approval: ev.Approval.ID,
						Decision: agent.Once,
					})
				})
			}
			if ev.Kind == agent.TurnSucceeded {
				succeeded = true
			}
		}
		return succeeded
	})

	msgs := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(msgs) != 3 || msgs[0].Text != "Update app.go" || len(msgs[0].Files) != 1 || msgs[0].Files[0].Name != "note.txt" {
		t.Fatalf("messages = %+v", msgs)
	}

	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 1 || !strings.Contains(changes[0].Patch, "+// via daemon") {
		t.Fatalf("changes = %+v", changes)
	}

	// Verify agent.ImagesKey RPC.
	img := image.NewNRGBA(image.Rect(0, 0, 120, 80))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	dims := call(t, h, func(ctx context.Context) ([2]int, error) {
		w, hh, err := p.Fit(ctx, agent.ModelRef{}, agent.File{Name: "shot.png", MIME: "image/png", Data: pngBuf.Bytes()})
		return [2]int{w, hh}, err
	})
	if dims != [2]int{120, 80} {
		t.Fatalf("Fit = %v, want [120 80]", dims)
	}
}

func TestReconnectAdvancesAttemptOnSocketDrop(t *testing.T) {
	h, p, _, _, _ := startDaemonHarness(t)
	firstAttempt := p.Status().Attempt
	cli, err := p.current()
	if err != nil {
		t.Fatal(err)
	}
	_ = cli.Close()
	waitFor(t, h, "reconnect ready", func() bool {
		st := p.Status()
		return st.State == agent.Ready && st.Attempt > firstAttempt
	})
}

func TestProvidesTerminalService(t *testing.T) {
	h, _, _, _, _ := startDaemonHarness(t)
	svc, ok := kernel.Use(h, terminal.ServiceKey)
	if !ok {
		t.Fatal("daemon plugin did not provide terminal.ServiceKey")
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.List(context.Background())
		done <- err
	}()
	var err error
	waitFor(t, h, "terminal list call", func() bool {
		select {
		case err = <-done:
			return true
		default:
			return false
		}
	})
	if err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("err = %v, want not_found error when server.Terminals is nil", err)
	}
}

func TestDaemonSettingsPairingAndMultiHost(t *testing.T) {
	h, p, _, _, _ := startDaemonHarness(t)
	hostFake := p.host.(*kittest.Host)

	p.copyPairingLink()
	waitFor(t, h, "pairing link copied", func() bool {
		return strings.Contains(hostFake.Copied, "#offer=") && p.remote.pairingURL == hostFake.Copied
	})

	p.remote.newName = "remote-box"
	p.remote.newTarget = hostFake.Copied
	p.addAndConnectHost()
	waitFor(t, h, "remote host saved and reconnected", func() bool {
		return len(p.remote.hosts) == 1 && p.remote.hosts[0].Name == "remote-box" && p.Status().State == agent.Ready
	})

	tt := ui.NewTester(p.daemonSection, 520, 640)
	tt.Frame()
	if !tt.HasText("remote-box") || !tt.HasText("Copy Pairing Link") {
		t.Fatalf("daemonSection missing expected multi-host UI: %q", tt.Texts())
	}
}

func TestRefusedCredentialsStopRetryingAndAskForThePassword(t *testing.T) {
	var dials atomic.Int32
	var p *Plugin
	host := &kittest.Host{Data: t.TempDir()}
	factory := func() kernel.Plugin {
		p = &Plugin{dial: func(context.Context) (*client.Client, protocol.DaemonStatusResponse, error) {
			dials.Add(1)
			return nil, protocol.DaemonStatusResponse{}, &protocol.RPCError{Code: protocol.ErrCodeUnauthorized, Message: "invalid daemon credential or password"}
		}}
		return p
	}
	h := kerneltest.New(t, host.New, factory)
	waitFor(t, h, "refusal reported", func() bool { return p.remote.authFailed && p.Status().State == agent.Down })
	time.Sleep(700 * time.Millisecond) // past the first backoff
	h.Flush()
	if n := dials.Load(); n != 1 {
		t.Fatalf("dialed %d times after a refusal, want 1", n)
	}

	_ = p.activeHost.Set("box")
	p.remote.authPassword = "hunter2"
	p.retryWithPassword()
	waitFor(t, h, "redialed with the password", func() bool { return dials.Load() == 2 && p.remote.authFailed })
	if p.remote.passwords["box"] != "hunter2" || p.remote.authPassword != "" {
		t.Fatalf("passwords = %v, typed = %q", p.remote.passwords, p.remote.authPassword)
	}
	tt := ui.NewTester(p.daemonSection, 520, 640)
	tt.Frame()
	if !tt.HasText("box needs its password") {
		t.Fatalf("no password prompt: %q", tt.Texts())
	}
}

func TestReplacingALoginCancelsThePreviousOne(t *testing.T) {
	h, p, _, _, backend := startDaemonHarness(t)
	p.login("first-provider", protocol.ProviderAuthOAuth)
	first := p.providers.loginID
	p.login("second-provider", protocol.ProviderAuthOAuth)
	waitFor(t, h, "first login cancelled on the daemon", func() bool {
		return slices.Contains(backend.cancelledLogins(), first)
	})
}
