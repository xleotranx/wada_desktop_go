package relay

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	RelayOpOpen  byte = 0x10
	RelayOpData  byte = 0x11
	RelayOpClose byte = 0x12
	RelayOpPing  byte = 0x13
	RelayOpPong  byte = 0x14
	// RelayOpChallenge (relay → daemon) and RelayOpProof (daemon → relay) are
	// the daemon's proof that it holds the key behind its server hash; they are
	// the first frames on a daemon connection, before it is registered.
	RelayOpChallenge byte = 0x15
	RelayOpProof     byte = 0x16

	SessionIDSize = 16
	muxHeaderSize = 1 + SessionIDSize

	// sessionRxFrames is how many inbound frames a relayed session may queue
	// before the daemon closes it rather than stall the shared relay socket.
	sessionRxFrames = 256
)

// Transport abstracts a full-duplex framed message stream (*websocket.Conn or
// an E2EE Noise NK virtual tunnel over wada-relay).
type Transport interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Close() error
}

// EncodeMuxFrame encodes a relay control or session payload with its 16-byte session ID.
func EncodeMuxFrame(op byte, sessID [SessionIDSize]byte, payload []byte) []byte {
	out := make([]byte, muxHeaderSize+len(payload))
	out[0] = op
	copy(out[1:muxHeaderSize], sessID[:])
	copy(out[muxHeaderSize:], payload)
	return out
}

// DecodeMuxFrame parses a relay multiplex frame.
func DecodeMuxFrame(raw []byte) (op byte, sessID [SessionIDSize]byte, payload []byte, err error) {
	if len(raw) < muxHeaderSize {
		return 0, sessID, nil, errors.New("relay: mux frame too short")
	}
	op = raw[0]
	copy(sessID[:], raw[1:muxHeaderSize])
	return op, sessID, raw[muxHeaderSize:], nil
}

// NoiseTransport implements Transport using paired Noise NK CipherStates over a
// binary frame read/write transport. Inner plaintext messages carry a 1-byte
// WebSocket message type prefix (TextMessage or BinaryMessage).
type NoiseTransport struct {
	readMu   sync.Mutex
	writeMu  sync.Mutex
	recv     *CipherState
	send     *CipherState
	recvFn   func(deadline time.Time) ([]byte, error)
	sendFn   func(data []byte, deadline time.Time) error
	closeFn  func() error
	dlMu     sync.Mutex
	readDL   time.Time
	writeDL  time.Time
	closedCh chan struct{}
	once     sync.Once
}

func newNoiseTransport(
	send, recv *CipherState,
	recvFn func(time.Time) ([]byte, error),
	sendFn func([]byte, time.Time) error,
	closeFn func() error,
) *NoiseTransport {
	return &NoiseTransport{
		send:     send,
		recv:     recv,
		recvFn:   recvFn,
		sendFn:   sendFn,
		closeFn:  closeFn,
		closedCh: make(chan struct{}),
	}
}

func (t *NoiseTransport) SetReadDeadline(dl time.Time) error {
	t.dlMu.Lock()
	t.readDL = dl
	t.dlMu.Unlock()
	return nil
}

func (t *NoiseTransport) SetWriteDeadline(dl time.Time) error {
	t.dlMu.Lock()
	t.writeDL = dl
	t.dlMu.Unlock()
	return nil
}

func (t *NoiseTransport) ReadMessage() (int, []byte, error) {
	t.dlMu.Lock()
	dl := t.readDL
	t.dlMu.Unlock()

	t.readMu.Lock()
	defer t.readMu.Unlock()

	ct, err := t.recvFn(dl)
	if err != nil {
		return 0, nil, err
	}
	pt, err := t.recv.DecryptWithAD(nil, ct)
	if err != nil {
		return 0, nil, err
	}
	if len(pt) == 0 {
		return 0, nil, errors.New("relay: empty inner frame")
	}
	msgType := int(pt[0])
	if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
		return 0, nil, fmt.Errorf("relay: unexpected inner frame type %d", msgType)
	}
	payload := make([]byte, len(pt)-1)
	copy(payload, pt[1:])
	return msgType, payload, nil
}

func (t *NoiseTransport) WriteMessage(msgType int, data []byte) error {
	if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
		return fmt.Errorf("relay: unsupported frame type %d", msgType)
	}
	t.dlMu.Lock()
	dl := t.writeDL
	t.dlMu.Unlock()

	plain := make([]byte, 1+len(data))
	plain[0] = byte(msgType)
	copy(plain[1:], data)

	// Keep encryption and wire transmission serialized together so Noise nonces
	// always reach the peer in strict monotonic order.
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	ct, err := t.send.EncryptWithAD(nil, plain)
	if err != nil {
		return err
	}
	return t.sendFn(ct, dl)
}

func (t *NoiseTransport) Close() error {
	var err error
	t.once.Do(func() {
		close(t.closedCh)
		if t.closeFn != nil {
			err = t.closeFn()
		}
	})
	return err
}

// RelayWSURL builds the relay WebSocket endpoint URL for role ("client" or "daemon").
func RelayWSURL(baseURL, role, serverHash string) (string, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return "", errors.New("relay: empty relay URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("relay: unsupported URL scheme %q", u.Scheme)
	}
	basePath := strings.TrimRight(u.Path, "/")
	basePath = strings.TrimSuffix(basePath, "/ws/client")
	basePath = strings.TrimSuffix(basePath, "/ws/daemon")
	u.Path = basePath + "/ws/" + role + "/" + serverHash
	return u.String(), nil
}

// ErrUnauthorized is returned when the relay refuses the client's relay token.
var ErrUnauthorized = errors.New("relay: the relay refused this client's relay token")

// DialClientTunnel connects through wada-relay to a daemon identified by
// serverPub, completes the Noise_NK_25519_ChaChaPoly_SHA256 handshake, and returns
// an end-to-end encrypted Transport.
func DialClientTunnel(ctx context.Context, relayURL string, serverPub [keySize]byte, relayAuthToken string) (*NoiseTransport, error) {
	hash := ServerPubHash(serverPub)
	wsURL, err := RelayWSURL(relayURL, "client", hash)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	if relayAuthToken != "" {
		hdr.Set("Authorization", "Bearer "+relayAuthToken)
	}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, hdr)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w (HTTP %d)", ErrUnauthorized, resp.StatusCode)
		}
		return nil, err
	}

	hs, err := NewInitiator(serverPub, nil)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	msg1, _, _, _, err := hs.WriteMessage(nil)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := ws.WriteMessage(websocket.BinaryMessage, msg1); err != nil {
		_ = ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	mt, msg2, err := ws.ReadMessage()
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		_ = ws.Close()
		return nil, errors.New("relay: expected binary noise handshake response")
	}
	_, sendCS, recvCS, _, err := hs.ReadMessage(msg2)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	_ = ws.SetWriteDeadline(time.Time{})

	return newNoiseTransport(
		sendCS,
		recvCS,
		func(dl time.Time) ([]byte, error) {
			_ = ws.SetReadDeadline(dl)
			for {
				mtype, data, rerr := ws.ReadMessage()
				if rerr != nil {
					return nil, rerr
				}
				if mtype != websocket.BinaryMessage {
					continue
				}
				return data, nil
			}
		},
		func(data []byte, dl time.Time) error {
			_ = ws.SetWriteDeadline(dl)
			return ws.WriteMessage(websocket.BinaryMessage, data)
		},
		ws.Close,
	), nil
}

// DaemonConnector maintains a persistent outbound WebSocket connection from
// wadad to wada-relay and dispatches each inbound Noise NK client tunnel to onSession.
type DaemonConnector struct {
	relayURL   string
	authToken  string
	kp         Keypair
	onSession  func(Transport)
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// StartDaemonConnector dials relayURL outbound and reconnects with backoff until Close().
func StartDaemonConnector(relayURL, authToken string, kp Keypair, onSession func(Transport)) (*DaemonConnector, error) {
	hash := ServerPubHash(kp.Public)
	if _, err := RelayWSURL(relayURL, "daemon", hash); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	dc := &DaemonConnector{
		relayURL:  relayURL,
		authToken: authToken,
		kp:        kp,
		onSession: onSession,
		ctx:       ctx,
		cancel:    cancel,
	}
	dc.wg.Add(1)
	go func() {
		defer dc.wg.Done()
		dc.loop()
	}()
	return dc, nil
}

func (dc *DaemonConnector) Close() {
	dc.cancel()
	dc.wg.Wait()
}

func (dc *DaemonConnector) loop() {
	backoffs := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	streak := 0
	hash := ServerPubHash(dc.kp.Public)
	wsURL, _ := RelayWSURL(dc.relayURL, "daemon", hash)

	for {
		if dc.ctx.Err() != nil {
			return
		}
		err := dc.runOnce(wsURL)
		if dc.ctx.Err() != nil {
			return
		}
		if err == nil {
			streak = 0
		}
		wait := backoffs[min(streak, len(backoffs)-1)]
		streak++
		t := time.NewTimer(wait)
		select {
		case <-dc.ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

type muxConn struct {
	dc       *DaemonConnector
	ws       *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	sessions map[[SessionIDSize]byte]*muxSession
	closed   bool
}

type muxSession struct {
	mc       *muxConn
	id       [SessionIDSize]byte
	rxCh     chan []byte
	done     chan struct{}
	once     sync.Once
}

func (dc *DaemonConnector) runOnce(wsURL string) error {
	dialCtx, cancel := context.WithTimeout(dc.ctx, 15*time.Second)
	hdr := http.Header{}
	if dc.authToken != "" {
		hdr.Set("Authorization", "Bearer "+dc.authToken)
	}
	ws, _, err := websocket.DefaultDialer.DialContext(dialCtx, wsURL, hdr)
	cancel()
	if err != nil {
		return err
	}
	if err := answerRelayChallenge(ws, dc.kp); err != nil {
		_ = ws.Close()
		return err
	}

	mc := &muxConn{
		dc:       dc,
		ws:       ws,
		sessions: map[[SessionIDSize]byte]*muxSession{},
	}

	connCtx, connCancel := context.WithCancel(dc.ctx)
	defer func() {
		connCancel()
		mc.closeAll()
	}()

	var loopWG sync.WaitGroup
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case t := <-ticker.C:
				var zero [SessionIDSize]byte
				var ts [8]byte
				binary.LittleEndian.PutUint64(ts[:], uint64(t.UnixMilli()))
				if err := mc.writeBinary(EncodeMuxFrame(RelayOpPing, zero, ts[:])); err != nil {
					_ = ws.Close()
					return
				}
			}
		}
	}()

	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		<-connCtx.Done()
		_ = ws.Close()
	}()

	for {
		mt, raw, rerr := ws.ReadMessage()
		if rerr != nil {
			connCancel()
			loopWG.Wait()
			return rerr
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		op, sid, payload, derr := DecodeMuxFrame(raw)
		if derr != nil {
			continue
		}
		switch op {
		case RelayOpPing:
			_ = mc.writeBinary(EncodeMuxFrame(RelayOpPong, sid, payload))
		case RelayOpOpen:
			mc.openSession(sid)
		case RelayOpData:
			cp := make([]byte, len(payload))
			copy(cp, payload)
			mc.deliver(sid, cp)
		case RelayOpClose:
			mc.closeSession(sid)
		}
	}
}

// answerRelayChallenge proves to wada-relay that this daemon holds kp's
// private key, which the relay requires before it routes any client to it.
func answerRelayChallenge(ws *websocket.Conn, kp Keypair) error {
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	mt, raw, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Time{})
	if mt != websocket.BinaryMessage {
		return errors.New("relay: expected daemon key challenge")
	}
	op, sid, challenge, err := DecodeMuxFrame(raw)
	if err != nil || op != RelayOpChallenge {
		return errors.New("relay: expected daemon key challenge")
	}
	proof, err := answerDaemonChallenge(kp, challenge)
	if err != nil {
		return err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = ws.WriteMessage(websocket.BinaryMessage, EncodeMuxFrame(RelayOpProof, sid, proof))
	_ = ws.SetWriteDeadline(time.Time{})
	return err
}

func (mc *muxConn) writeBinary(frame []byte) error {
	mc.writeMu.Lock()
	defer mc.writeMu.Unlock()
	_ = mc.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return mc.ws.WriteMessage(websocket.BinaryMessage, frame)
}

func (mc *muxConn) openSession(sid [SessionIDSize]byte) {
	ms := &muxSession{
		mc:   mc,
		id:   sid,
		rxCh: make(chan []byte, sessionRxFrames),
		done: make(chan struct{}),
	}
	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		return
	}
	mc.sessions[sid] = ms
	mc.dc.wg.Add(1)
	mc.mu.Unlock()

	go func() {
		defer mc.dc.wg.Done()
		defer ms.closeRemote()
		tr, err := ms.acceptNoise(mc.dc.ctx, mc.dc.kp)
		if err != nil {
			return
		}
		mc.dc.onSession(tr)
	}()
}

func (mc *muxConn) deliver(sid [SessionIDSize]byte, payload []byte) {
	mc.mu.Lock()
	ms := mc.sessions[sid]
	mc.mu.Unlock()
	if ms == nil {
		return
	}
	// Never block the shared relay read loop on one session: a session whose
	// receive buffer is full is closed, since dropping a single Noise frame
	// would break its nonce sequence anyway.
	select {
	case ms.rxCh <- payload:
	case <-ms.done:
	default:
		ms.closeRemote()
	}
}

func (mc *muxConn) closeSession(sid [SessionIDSize]byte) {
	mc.mu.Lock()
	ms := mc.sessions[sid]
	if ms != nil {
		delete(mc.sessions, sid)
	}
	mc.mu.Unlock()
	if ms != nil {
		ms.closeLocalOnly()
	}
}

func (mc *muxConn) closeAll() {
	mc.mu.Lock()
	mc.closed = true
	all := make([]*muxSession, 0, len(mc.sessions))
	for sid, ms := range mc.sessions {
		all = append(all, ms)
		delete(mc.sessions, sid)
	}
	mc.mu.Unlock()
	for _, ms := range all {
		ms.closeLocalOnly()
	}
	_ = mc.ws.Close()
}

func (ms *muxSession) closeLocalOnly() {
	ms.once.Do(func() {
		close(ms.done)
	})
}

func (ms *muxSession) closeRemote() {
	ms.mc.closeSession(ms.id)
	_ = ms.mc.writeBinary(EncodeMuxFrame(RelayOpClose, ms.id, nil))
}

func (ms *muxSession) recvFrame(ctx context.Context, dl time.Time) ([]byte, error) {
	var timer *time.Timer
	var dlCh <-chan time.Time
	if !dl.IsZero() {
		wait := time.Until(dl)
		if wait <= 0 {
			return nil, context.DeadlineExceeded
		}
		timer = time.NewTimer(wait)
		defer timer.Stop()
		dlCh = timer.C
	}
	select {
	case <-ms.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dlCh:
		return nil, context.DeadlineExceeded
	case data := <-ms.rxCh:
		return data, nil
	}
}

func (ms *muxSession) acceptNoise(ctx context.Context, kp Keypair) (*NoiseTransport, error) {
	hs, err := NewResponder(kp.Private, nil)
	if err != nil {
		return nil, err
	}
	msg1, err := ms.recvFrame(ctx, time.Now().Add(15*time.Second))
	if err != nil {
		return nil, err
	}
	if _, _, _, _, err := hs.ReadMessage(msg1); err != nil {
		return nil, err
	}
	msg2, sendCS, recvCS, _, err := hs.WriteMessage(nil)
	if err != nil {
		return nil, err
	}
	if err := ms.mc.writeBinary(EncodeMuxFrame(RelayOpData, ms.id, msg2)); err != nil {
		return nil, err
	}
	return newNoiseTransport(
		sendCS,
		recvCS,
		func(dl time.Time) ([]byte, error) {
			return ms.recvFrame(ctx, dl)
		},
		func(data []byte, _ time.Time) error {
			select {
			case <-ms.done:
				return io.EOF
			default:
			}
			return ms.mc.writeBinary(EncodeMuxFrame(RelayOpData, ms.id, data))
		},
		func() error {
			ms.closeRemote()
			return nil
		},
	), nil
}

func newSessionID() ([SessionIDSize]byte, error) {
	var sid [SessionIDSize]byte
	_, err := rand.Read(sid[:])
	return sid, err
}
