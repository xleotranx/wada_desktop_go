package relay

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRouterForwardsOpaqueCiphertextAcrossConcurrentClients(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(RouterConfig{AuthToken: "relay-secret", MaxClientsPerDaemon: 4})
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Wrap router traffic slightly just to ensure plain text never appears on relay.
	var (
		mu       sync.Mutex
		sessions []*NoiseTransport
	)
	accepted := make(chan *NoiseTransport, 4)

	peers := make(chan string, 4)
	dc, err := StartDaemonConnector(srv.URL, "relay-secret", kp, func(tr Transport, peer string) {
		peers <- peer
		nt := tr.(*NoiseTransport)
		mu.Lock()
		sessions = append(sessions, nt)
		mu.Unlock()
		accepted <- nt
		defer nt.Close()

		for {
			mt, data, rerr := nt.ReadMessage()
			if rerr != nil {
				return
			}
			reply := append([]byte("echo:"), data...)
			if werr := nt.WriteMessage(mt, reply); werr != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	// Wait until daemon registers with router.
	deadline := time.Now().Add(5 * time.Second)
	var c1 *NoiseTransport
	for {
		c1, err = DialClientTunnel(context.Background(), srv.URL, kp.Public, "relay-secret")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out dialing client 1 through relay: %v", err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	defer c1.Close()

	c2, err := DialClientTunnel(context.Background(), srv.URL, kp.Public, "relay-secret")
	if err != nil {
		t.Fatalf("dial client 2: %v", err)
	}
	defer c2.Close()
	for range 2 {
		if peer := <-peers; peer != "127.0.0.1" {
			t.Fatalf("relay reported client %q, want 127.0.0.1", peer)
		}
	}

	// Wrong relay auth token must fail.
	if _, err := DialClientTunnel(context.Background(), srv.URL, kp.Public, "wrong"); err == nil {
		t.Fatal("expected wrong relay auth token to be rejected")
	}

	// Drain accepted channel.
	<-accepted
	<-accepted

	// Concurrent text & binary frames on both independent encrypted tunnels.
	if err := c1.WriteMessage(websocket.TextMessage, []byte("from-c1")); err != nil {
		t.Fatal(err)
	}
	if err := c2.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatal(err)
	}

	mt1, d1, err := c1.ReadMessage()
	if err != nil || mt1 != websocket.TextMessage || string(d1) != "echo:from-c1" {
		t.Fatalf("c1 got (%d, %q, %v)", mt1, d1, err)
	}
	mt2, d2, err := c2.ReadMessage()
	if err != nil || mt2 != websocket.BinaryMessage || !bytes.Equal(d2, []byte("echo:\x01\x02\x03")) {
		t.Fatalf("c2 got (%d, %x, %v)", mt2, d2, err)
	}

	// Offline daemon hash returns 503.
	kp2, _ := GenerateKeypair()
	if _, err := DialClientTunnel(context.Background(), srv.URL, kp2.Public, "relay-secret"); err == nil {
		t.Fatal("expected offline daemon dial to fail")
	}

	// Closing dc tears down server-side Noise sessions cleanly.
	dc.Close()
	_ = c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := c1.ReadMessage(); err == nil {
		t.Fatal("expected client read to fail after daemon connector closed")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("client read timed out waiting for EOF after daemon closed: %v", err)
	}
}

func TestStalledSessionDoesNotBlockOtherTunnels(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter(RouterConfig{}))
	defer srv.Close()

	release := make(chan struct{})
	dc, err := StartDaemonConnector(srv.URL, "", kp, func(tr Transport, _ string) {
		defer tr.Close()
		_, first, rerr := tr.ReadMessage()
		if rerr != nil {
			return
		}
		if string(first) == "stall" {
			<-release // stop reading, as a session blocked in a slow call would
			return
		}
		for {
			mt, data, rerr := tr.ReadMessage()
			if rerr != nil {
				return
			}
			if werr := tr.WriteMessage(mt, append([]byte("echo:"), data...)); werr != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	defer close(release) // before dc.Close, which waits for the stalled handler

	dial := func() *NoiseTransport {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, derr := DialClientTunnel(context.Background(), srv.URL, kp.Public, "")
			if derr == nil {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatalf("dial through relay: %v", derr)
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	stalled := dial()
	defer stalled.Close()
	healthy := dial()
	defer healthy.Close()

	if err := stalled.WriteMessage(websocket.TextMessage, []byte("stall")); err != nil {
		t.Fatal(err)
	}
	if err := healthy.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	for range sessionRxFrames + 64 {
		if err := stalled.WriteMessage(websocket.BinaryMessage, []byte("flood")); err != nil {
			break // the daemon may already have closed the stalled tunnel
		}
	}

	// The daemon closes the session that stopped reading instead of blocking
	// the relay socket it shares with every other session.
	_ = stalled.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = stalled.ReadMessage()
	var netErr net.Error
	if err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		t.Fatalf("stalled tunnel was not closed: %v", err)
	}

	if err := healthy.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = healthy.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, data, err := healthy.ReadMessage(); err != nil || string(data) != "echo:ping" {
		t.Fatalf("healthy tunnel got (%q, %v) after another session stalled", data, err)
	}
}

func TestClientAddrTrustsForwardedForOnlyFromTrustedProxies(t *testing.T) {
	rt := NewRouter(RouterConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}})
	cases := []struct {
		remote, fwd, want string
	}{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"203.0.113.9:5000", "198.51.100.1", "203.0.113.9"}, // not a trusted proxy
		{"172.18.0.3:5000", "198.51.100.1", "198.51.100.1"},
		{"172.18.0.3:5000", "10.0.0.1, 198.51.100.2", "198.51.100.2"},
		{"172.18.0.3:5000", "garbage", "172.18.0.3"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/ws/client/x", nil)
		r.RemoteAddr = tc.remote
		if tc.fwd != "" {
			r.Header.Set("X-Forwarded-For", tc.fwd)
		}
		if got := rt.clientAddr(r); got != tc.want {
			t.Errorf("clientAddr(%s, %q) = %q, want %q", tc.remote, tc.fwd, got, tc.want)
		}
	}
}
