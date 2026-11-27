package server_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/server"
	"wada-desktop-go/internal/protocol"
	"wada-desktop-go/internal/relay"
)

func TestDaemonRemoteRelayPairingAndPasswordAuthE2E(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()

	router := relay.NewRouter(relay.RouterConfig{AuthToken: "relay-tok"})
	relaySrv := httptest.NewServer(router)
	defer relaySrv.Close()

	rt, err := pig.New(t.Context(), pig.Options{Root: filepath.Join(dataDir, "pig")})
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}

	srv, err := server.New(t.Context(), server.Config{
		DataDir:    dataDir,
		Listen:     "127.0.0.1:0",
		Version:    "relay-e2e",
		RelayURL:   relaySrv.URL,
		RelayToken: "relay-tok",
		AppURL:     "https://app.wada.dev",
		Backend:    rt,
	})
	if err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// Local client obtains a pairing offer.
	localCli, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer localCli.Close()

	offerResp, err := localCli.PairingOffer(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if offerResp.RequiresPassword || offerResp.RelayURL != relaySrv.URL {
		t.Fatalf("unexpected pairing offer: %+v", offerResp)
	}

	// Remote client dials through the zero-knowledge relay using the offer URL.
	var remoteCli *client.Client
	deadline := time.Now().Add(5 * time.Second)
	for {
		remoteCli, err = client.DialOffer(t.Context(), offerResp.URL, client.Options{
			ClientType:     "web",
			RelayAuthToken: "relay-tok",
		})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out dialing through relay: %v", err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	defer remoteCli.Close()

	st, err := remoteCli.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerID != srv.ServerID() {
		t.Fatalf("ServerID = %q, want %q", st.ServerID, srv.ServerID())
	}

	// Verify RPCs (session create + list) work end-to-end through the encrypted Noise tunnel.
	workDir := t.TempDir()
	sess, err := remoteCli.Create(t.Context(), protocol.CreateSessionRequest{
		IdempotencyKey: "k_relay_1",
		Directory:      workDir,
		Title:          "From Phone",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := remoteCli.Sessions(t.Context(), protocol.SessionQuery{})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != sess.ID {
		t.Fatalf("Sessions = %+v, err=%v", page, err)
	}

	// Enable password protection dynamically; token-only pairing offers stop working
	// and require the configured password inside the encrypted hello.
	if err := localCli.SetPassword(t.Context(), "s3cr3t-pass"); err != nil {
		t.Fatal(err)
	}
	pwOffer, err := localCli.PairingOffer(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !pwOffer.RequiresPassword {
		t.Fatalf("expected RequiresPassword=true, got %+v", pwOffer)
	}
	decoded, err := relay.DecodeOffer(pwOffer.URL)
	if err != nil || decoded.Token != "" {
		t.Fatalf("password-protected offer must omit token: %+v, err=%v", decoded, err)
	}

	if _, err := client.DialOffer(t.Context(), offerResp.URL, client.Options{RelayAuthToken: "relay-tok"}); err == nil {
		t.Fatal("expected old token-only offer to be rejected once password is set")
	}
	if _, err := client.DialOffer(t.Context(), pwOffer.URL, client.Options{RelayAuthToken: "relay-tok", Password: "wrong"}); err == nil {
		t.Fatal("expected wrong password to be rejected")
	}
	authedCli, err := client.DialOffer(t.Context(), pwOffer.URL, client.Options{RelayAuthToken: "relay-tok", Password: "s3cr3t-pass"})
	if err != nil {
		t.Fatalf("expected valid password over Noise relay to succeed: %v", err)
	}
	_ = authedCli.Close()
}
