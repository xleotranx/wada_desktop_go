package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		ID:        "req-ws-1",
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
				protocol.EventModelsChanged, protocol.EventProviderAuth:
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
