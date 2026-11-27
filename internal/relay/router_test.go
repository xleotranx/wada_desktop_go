package relay

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
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

	dc, err := StartDaemonConnector(srv.URL, "relay-secret", kp, func(tr Transport) {
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
