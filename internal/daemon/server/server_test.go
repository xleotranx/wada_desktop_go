package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

func startTestServer(t *testing.T, dataDir string, opts ...func(*Config)) (*Server, *pig.Runtime, *ai.FauxProviderHandle, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		DataDir: dataDir,
		Listen:  "127.0.0.1:0",
		Version: "test-0.1",
		Backend: rt,
	}
	for _, fn := range opts {
		fn(&cfg)
	}
	srv, err := New(t.Context(), cfg)
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	addr, err := srv.Start()
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, rt, faux, addr
}

func TestHostHeaderDNSRebindingRejected(t *testing.T) {
	_, _, _, addr := startTestServer(t, t.TempDir())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example.com:6767"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	okReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/health", nil)
	okResp, err := http.DefaultClient.Do(okReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", okResp.StatusCode)
	}
}

func TestHelloRejectsInvalidCredentialAndAdmitsLocalCredential(t *testing.T) {
	dataDir := t.TempDir()
	_, _, _, addr := startTestServer(t, dataDir)

	if _, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Credential: "wrong"}); err == nil {
		t.Fatal("expected invalid credential rejection")
	}

	c, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerID == "" || st.Epoch != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestSessionCreateIdempotencyDedupAndConflict(t *testing.T) {
	dataDir := t.TempDir()
	_, _, _, addr := startTestServer(t, dataDir)
	rec, cred, err := client.ReadLocalEndpoint(dataDir)
	if err != nil || rec.Listen != addr {
		t.Fatalf("local endpoint = %+v, err=%v", rec, err)
	}
	c, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Credential: cred})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	workDir := t.TempDir()
	req := protocol.CreateSessionRequest{
		IdempotencyKey: "create-key-1",
		Title:          "Idempotent",
		Directory:      workDir,
		Model:          protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	}
	s1, err := c.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := c.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID != s2.ID {
		t.Fatalf("idempotent create diverged: %s vs %s", s1.ID, s2.ID)
	}

	req.Title = "Different"
	if _, err := c.Create(t.Context(), req); err == nil {
		t.Fatal("expected conflict on mismatched idempotencyKey payload")
	} else {
		var rpcErr *protocol.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.ErrCodeConflict {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestConcurrentIdempotentCreatesMakeOneSession(t *testing.T) {
	dataDir := t.TempDir()
	_, _, _, addr := startTestServer(t, dataDir)
	_, cred, err := client.ReadLocalEndpoint(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Credential: cred})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := protocol.CreateSessionRequest{
		IdempotencyKey: "create-race",
		Title:          "Raced",
		Directory:      t.TempDir(),
		Model:          protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	}
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := c.Create(t.Context(), req)
			var rpcErr *protocol.RPCError
			switch {
			case err == nil:
				ids[i] = sess.ID
			case errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrCodeOutcomeUnknown:
			default:
				t.Errorf("create %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	final, err := c.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if id != "" && id != final.ID {
			t.Fatalf("create %d made %s, want %s", i, id, final.ID)
		}
	}
	page, err := c.Sessions(t.Context(), protocol.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, sess := range page.Sessions {
		if sess.Title == "Raced" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d sessions created, want 1", n)
	}
}

func TestSetPasswordDisconnectsRemoteClientsAndRotatesToken(t *testing.T) {
	dataDir := t.TempDir()
	srv, _, _, addr := startTestServer(t, dataDir)
	local, err := client.DialLocal(t.Context(), dataDir, client.Options{Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	oldToken := srv.RemoteToken()
	remote, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Credential: oldToken})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()

	if err := local.SetPassword(t.Context(), "n3w-pass"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("remote client stayed connected after the password changed")
	}
	if _, err := local.Status(t.Context()); err != nil {
		t.Fatalf("local client lost: %v", err)
	}
	if srv.RemoteToken() == oldToken {
		t.Fatal("remote token was not rotated")
	}
	if c, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Credential: oldToken}); err == nil {
		_ = c.Close()
		t.Fatal("old token still accepted")
	}
	c, err := client.Dial(t.Context(), client.Options{Endpoint: addr, Password: "n3w-pass"})
	if err != nil {
		t.Fatalf("new password refused: %v", err)
	}
	_ = c.Close()
}

func TestLiveStreamingApprovalAndTimelineCatchup(t *testing.T) {
	dataDir := t.TempDir()
	_, _, faux, addr := startTestServer(t, dataDir)
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := c.Create(t.Context(), protocol.CreateSessionRequest{
		Directory: workDir,
		Model:     protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}

	subID, ch, err := c.Subscribe(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}

	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "app.go", "content": "package app\n// via ws\n"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done over WS.")}},
	})

	disp, err := c.Send(t.Context(), protocol.SendRequest{
		MessageID: "msg-ws-1",
		Session:   s.ID,
		Text:      "Update app.go",
	})
	if err != nil || disp != protocol.SendAccepted {
		t.Fatalf("send = %q, err = %v", disp, err)
	}

	var firstSeq uint64
	deadline := time.After(10 * time.Second)
	succeeded := false
	for !succeeded {
		select {
		case ev := <-ch:
			if firstSeq == 0 && ev.Seq > 0 {
				firstSeq = ev.Seq
			}
			switch ev.Kind {
			case protocol.EventApprovalRequested:
				if err := c.Reply(t.Context(), protocol.ReplyRequest{
					Session:  s.ID,
					Approval: ev.Approval.ID,
					Decision: protocol.DecisionOnce,
				}); err != nil {
					t.Fatal(err)
				}
			case protocol.EventTurnSucceeded:
				succeeded = true
			case protocol.EventTurnFailed:
				t.Fatalf("turn failed: %s", ev.Error)
			case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded,
				protocol.EventToolStarted, protocol.EventToolCalled, protocol.EventToolFinished,
				protocol.EventInteractionRequested, protocol.EventTurnStarted, protocol.EventTurnInterrupted,
				protocol.EventModelChanged, protocol.EventSessionRenamed, protocol.EventSessionDeleted,
				protocol.EventModelsChanged, protocol.EventProviderAuth, protocol.EventWorkerUpdated,
				protocol.EventTerminalUpdated, protocol.EventWorkspaceUpdated:
			}
		case <-deadline:
			t.Fatal("timed out waiting for WS turn")
		}
	}
	_ = c.Unsubscribe(t.Context(), subID)
	_ = c.Close()

	// Reconnect on a fresh client and fetch missed timeline events after firstSeq.
	c2, err := client.DialLocal(t.Context(), dataDir, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	tl, err := c2.FetchTimeline(t.Context(), protocol.TimelineFetchRequest{Session: s.ID, Epoch: 1, AfterSeq: firstSeq, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Events) == 0 || tl.Events[len(tl.Events)-1].Kind != protocol.EventTurnSucceeded {
		t.Fatalf("timeline catchup = %+v", tl)
	}
	changes, err := c2.Changes(t.Context(), s.ID)
	if err != nil || len(changes) != 1 || !strings.Contains(changes[0].Patch, "+// via ws") {
		t.Fatalf("changes = %+v, err=%v", changes, err)
	}
}

func TestPendingDeliveryBecomesOutcomeUnknownAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	req := protocol.SendRequest{Session: "sess-1", MessageID: "msg-inflight", Text: "hello"}
	if err := st.PutDeliveryReceipt(t.Context(), store.DeliveryReceipt{
		SessionID:   req.Session,
		MessageID:   req.MessageID,
		RequestHash: hashStruct(req),
		Status:      store.DeliveryPending,
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Starting server marks previous pending deliveries unknown.
	_, _, _, _ = startTestServer(t, dataDir)
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Send(t.Context(), req); err == nil {
		t.Fatal("expected outcome_unknown error")
	} else {
		var rpcErr *protocol.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.ErrCodeOutcomeUnknown {
			t.Fatalf("got %v, want outcome_unknown", err)
		}
	}
}

func TestSubscribeHonorsClientChosenIDAndRejectsReuse(t *testing.T) {
	dataDir := t.TempDir()
	_, _, _, addr := startTestServer(t, dataDir)
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{Endpoint: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var resp protocol.SubscribeResponse
	if err := c.Call(t.Context(), protocol.MethodEventsSubscribe, protocol.SubscribeRequest{SubscriptionID: "sub_mine"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SubscriptionID != "sub_mine" {
		t.Fatalf("subscriptionId = %q, want sub_mine", resp.SubscriptionID)
	}
	err = c.Call(t.Context(), protocol.MethodEventsSubscribe, protocol.SubscribeRequest{SubscriptionID: "sub_mine"}, &resp)
	var rpcErr *protocol.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.ErrCodeConflict {
		t.Fatalf("reused subscriptionId err = %v, want conflict", err)
	}
	if err := c.Call(t.Context(), protocol.MethodEventsSubscribe, protocol.SubscribeRequest{}, &resp); err != nil || !strings.HasPrefix(resp.SubscriptionID, "sub_") {
		t.Fatalf("server-chosen subscriptionId = %q, err = %v", resp.SubscriptionID, err)
	}
}

// exitingTerminals is a backend whose one terminal ends when the test closes
// out.
type exitingTerminals struct {
	*pig.Runtime
	out chan []byte
}

func (*exitingTerminals) ListTerminals(context.Context) ([]protocol.TerminalInfo, error) {
	return nil, nil
}

func (*exitingTerminals) CreateTerminal(context.Context, protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	return protocol.TerminalInfo{}, errors.New("not supported")
}

func (b *exitingTerminals) AttachTerminal(_ context.Context, _ string, req protocol.TerminalAttachRequest) (protocol.TerminalInfo, []byte, <-chan []byte, func(), error) {
	return protocol.TerminalInfo{ID: req.ID, Running: true}, nil, b.out, func() {}, nil
}

func (*exitingTerminals) TerminalInput(context.Context, string, []byte) error { return nil }
func (*exitingTerminals) TerminalResize(context.Context, string, int, int) error {
	return nil
}

func (*exitingTerminals) CaptureTerminal(context.Context, string, int) (protocol.TerminalCaptureResponse, error) {
	return protocol.TerminalCaptureResponse{}, nil
}
func (*exitingTerminals) KillTerminal(context.Context, string) error { return nil }

func TestTerminalExitEndsClientStream(t *testing.T) {
	dataDir := t.TempDir()
	backend := &exitingTerminals{out: make(chan []byte, 4)}
	startTestServer(t, dataDir, func(cfg *Config) {
		backend.Runtime = cfg.Backend.(*pig.Runtime)
		cfg.Backend = backend
	})
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.AttachTerminal(t.Context(), protocol.TerminalAttachRequest{ID: "term_1"})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	backend.out <- []byte("bye")
	close(backend.out)

	var got []byte
	timeout := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case chunk, ok := <-st.Output():
			got = append(got, chunk...)
			open = ok
		case <-timeout:
			t.Fatalf("stream still open after the terminal ended; got %q", got)
		}
	}
	if string(got) != "bye" {
		t.Fatalf("output = %q, want bye", got)
	}
}
