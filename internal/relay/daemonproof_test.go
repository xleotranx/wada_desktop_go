package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDaemonProofVerifiesOnlyTheKeyHolder(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	hash := ServerPubHash(kp.Public)

	eph, challenge, err := newDaemonChallenge()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := answerDaemonChallenge(kp, challenge)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDaemonProof(eph, challenge, hash, proof); err != nil {
		t.Fatalf("genuine proof rejected: %v", err)
	}

	wrongKey, _ := answerDaemonChallenge(other, challenge)
	if verifyDaemonProof(eph, challenge, hash, wrongKey) == nil {
		t.Fatal("proof from another key accepted for this hash")
	}
	claimed := append([]byte(nil), wrongKey...)
	copy(claimed, kp.Public[:]) // claims the real public key without its private key
	if verifyDaemonProof(eph, challenge, hash, claimed) == nil {
		t.Fatal("proof claiming the real public key without its private key accepted")
	}
	tampered := append([]byte(nil), proof...)
	tampered[len(tampered)-1] ^= 1
	if verifyDaemonProof(eph, challenge, hash, tampered) == nil {
		t.Fatal("tampered proof accepted")
	}
	_, otherChallenge, _ := newDaemonChallenge()
	if verifyDaemonProof(eph, otherChallenge, hash, proof) == nil {
		t.Fatal("proof replayed against another challenge accepted")
	}
}

func TestImpostorCannotEvictRegisteredDaemon(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	impostorKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter(RouterConfig{}))
	defer srv.Close()

	dc, err := StartDaemonConnector(srv.URL, "", kp, func(tr Transport) {
		defer tr.Close()
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

	var c *NoiseTransport
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err = DialClientTunnel(context.Background(), srv.URL, kp.Public, "")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial through relay: %v", err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	defer c.Close()

	// The impostor knows the real hash and answers the challenge with its own key.
	wsURL, _ := RelayWSURL(srv.URL, "daemon", ServerPubHash(kp.Public))
	imp, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	defer imp.Close()
	_, raw, err := imp.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	op, sid, challenge, err := DecodeMuxFrame(raw)
	if err != nil || op != RelayOpChallenge {
		t.Fatalf("first relay frame op=%#x err=%v, want challenge", op, err)
	}
	proof, err := answerDaemonChallenge(impostorKey, challenge)
	if err != nil {
		t.Fatal(err)
	}
	if err := imp.WriteMessage(websocket.BinaryMessage, EncodeMuxFrame(RelayOpProof, sid, proof)); err != nil {
		t.Fatal(err)
	}
	_ = imp.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := imp.ReadMessage(); err == nil || !strings.Contains(err.Error(), "proof failed") {
		t.Fatalf("impostor connection not refused: %v", err)
	}

	// The real daemon's existing tunnel and new dials keep working.
	if err := c.WriteMessage(websocket.TextMessage, []byte("still-here")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, data, err := c.ReadMessage(); err != nil || string(data) != "echo:still-here" {
		t.Fatalf("existing tunnel got (%q, %v) after impostor attempt", data, err)
	}
	c2, err := DialClientTunnel(context.Background(), srv.URL, kp.Public, "")
	if err != nil {
		t.Fatalf("new dial after impostor attempt: %v", err)
	}
	_ = c2.Close()
}

func TestClientFarBehindIsDisconnectedNotDropped(t *testing.T) {
	upgrader := websocket.Upgrader{}
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- ws
		}
	}))
	defer srv.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	ws := <-accepted

	cs := &clientSlot{ws: ws, wake: make(chan struct{}, 1), done: make(chan struct{})}
	frame := make([]byte, 1<<20)
	for range maxClientQueueBytes/len(frame) + 1 {
		cs.enqueue(frame)
	}
	select {
	case <-cs.done:
	default:
		t.Fatal("client past the queue limit was not disconnected")
	}
	cs.mu.Lock()
	queued := len(cs.queue)
	cs.mu.Unlock()
	if queued != 0 {
		t.Fatalf("closed client still has %d queued frames", queued)
	}
}
