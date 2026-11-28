package relay

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultMaxClientsPerDaemon = 64
	maxRelayFrameBytes         = 4 << 20
	maxClientQueueBytes        = 4 << 20
	daemonProofTimeout         = 10 * time.Second
)

// RouterConfig configures a stateless zero-knowledge wada-relay router.
type RouterConfig struct {
	AuthToken           string
	MaxClientsPerDaemon int
	// CheckOrigin optionally restricts WebSocket Upgrade Origin headers (e.g. for web app origins).
	CheckOrigin func(*http.Request) bool
}

// Router forwards opaque Noise NK ciphertext frames between daemons and
// clients without terminating TLS E2EE keys or holding any plaintext keys.
type Router struct {
	cfg      RouterConfig
	upgrader websocket.Upgrader

	mu      sync.Mutex
	daemons map[string]*daemonSlot
}

type daemonSlot struct {
	hash    string
	ws      *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	clients map[[SessionIDSize]byte]*clientSlot
	closed  bool
}

type clientSlot struct {
	id         [SessionIDSize]byte
	ws         *websocket.Conn
	daemon     *daemonSlot
	mu         sync.Mutex
	queue      [][]byte
	queuedByte int
	closed     bool
	wake       chan struct{}
	done       chan struct{}
}

// NewRouter creates a stateless HTTP/WebSocket relay router.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.MaxClientsPerDaemon <= 0 {
		cfg.MaxClientsPerDaemon = defaultMaxClientsPerDaemon
	}
	checkOrigin := cfg.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = func(*http.Request) bool { return true }
	}
	return &Router{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: checkOrigin,
		},
		daemons: map[string]*daemonSlot{},
	}
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	case strings.HasPrefix(r.URL.Path, "/ws/daemon/"):
		hash := strings.TrimPrefix(r.URL.Path, "/ws/daemon/")
		rt.handleDaemon(w, r, hash)
	case strings.HasPrefix(r.URL.Path, "/ws/client/"):
		hash := strings.TrimPrefix(r.URL.Path, "/ws/client/")
		rt.handleClient(w, r, hash)
	default:
		http.NotFound(w, r)
	}
}

func (rt *Router) authorized(r *http.Request) bool {
	if rt.cfg.AuthToken == "" {
		return true
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(rt.cfg.AuthToken)) == 1
}

func (rt *Router) handleDaemon(w http.ResponseWriter, r *http.Request, hash string) {
	if !isValidServerHash(hash) {
		http.Error(w, "invalid server hash", http.StatusBadRequest)
		return
	}
	if !rt.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxRelayFrameBytes + muxHeaderSize)
	if err := proveDaemon(ws, hash); err != nil {
		_ = ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "daemon key proof failed"),
			time.Now().Add(time.Second))
		_ = ws.Close()
		return
	}

	// Only a daemon that proved it holds the key replaces the current slot,
	// which is how a reconnecting daemon takes over from a dead connection.
	slot := &daemonSlot{
		hash:    hash,
		ws:      ws,
		clients: map[[SessionIDSize]byte]*clientSlot{},
	}

	rt.mu.Lock()
	prev := rt.daemons[hash]
	rt.daemons[hash] = slot
	rt.mu.Unlock()
	if prev != nil {
		prev.closeAll()
	}

	defer func() {
		rt.mu.Lock()
		if rt.daemons[hash] == slot {
			delete(rt.daemons, hash)
		}
		rt.mu.Unlock()
		slot.closeAll()
	}()

	for {
		mt, raw, rerr := ws.ReadMessage()
		if rerr != nil {
			return
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
			_ = slot.writeBinary(EncodeMuxFrame(RelayOpPong, sid, payload))
		case RelayOpData:
			slot.mu.Lock()
			cs := slot.clients[sid]
			slot.mu.Unlock()
			if cs != nil {
				cp := make([]byte, len(payload))
				copy(cp, payload)
				cs.enqueue(cp)
			}
		case RelayOpClose:
			slot.mu.Lock()
			cs := slot.clients[sid]
			delete(slot.clients, sid)
			slot.mu.Unlock()
			if cs != nil {
				cs.closeNow()
			}
		}
	}
}

// proveDaemon challenges a newly connected daemon to prove it holds the private
// key behind hash before it may register.
func proveDaemon(ws *websocket.Conn, hash string) error {
	eph, challenge, err := newDaemonChallenge()
	if err != nil {
		return err
	}
	var zero [SessionIDSize]byte
	_ = ws.SetWriteDeadline(time.Now().Add(daemonProofTimeout))
	if err := ws.WriteMessage(websocket.BinaryMessage, EncodeMuxFrame(RelayOpChallenge, zero, challenge)); err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Now().Add(daemonProofTimeout))
	mt, raw, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Time{})
	_ = ws.SetWriteDeadline(time.Time{})
	if mt != websocket.BinaryMessage {
		return errBadDaemonProof
	}
	op, _, proof, err := DecodeMuxFrame(raw)
	if err != nil || op != RelayOpProof {
		return errBadDaemonProof
	}
	return verifyDaemonProof(eph, challenge, hash, proof)
}

func (rt *Router) handleClient(w http.ResponseWriter, r *http.Request, hash string) {
	if !isValidServerHash(hash) {
		http.Error(w, "invalid server hash", http.StatusBadRequest)
		return
	}
	if !rt.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rt.mu.Lock()
	d := rt.daemons[hash]
	rt.mu.Unlock()
	if d == nil {
		http.Error(w, "daemon offline", http.StatusServiceUnavailable)
		return
	}

	sid, err := newSessionID()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		http.Error(w, "daemon offline", http.StatusServiceUnavailable)
		return
	}
	if len(d.clients) >= rt.cfg.MaxClientsPerDaemon {
		d.mu.Unlock()
		http.Error(w, "daemon client limit reached", http.StatusTooManyRequests)
		return
	}
	ws, uerr := rt.upgrader.Upgrade(w, r, nil)
	if uerr != nil {
		d.mu.Unlock()
		return
	}
	ws.SetReadLimit(maxRelayFrameBytes)

	cs := &clientSlot{
		id:     sid,
		ws:     ws,
		daemon: d,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	d.clients[sid] = cs
	d.mu.Unlock()

	if err := d.writeBinary(EncodeMuxFrame(RelayOpOpen, sid, nil)); err != nil {
		d.mu.Lock()
		delete(d.clients, sid)
		d.mu.Unlock()
		cs.closeNow()
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cs.writeLoop()
	}()

	defer func() {
		d.mu.Lock()
		delete(d.clients, sid)
		d.mu.Unlock()
		_ = d.writeBinary(EncodeMuxFrame(RelayOpClose, sid, nil))
		cs.closeNow()
		wg.Wait()
	}()

	for {
		mt, raw, rerr := ws.ReadMessage()
		if rerr != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		if err := d.writeBinary(EncodeMuxFrame(RelayOpData, sid, raw)); err != nil {
			return
		}
	}
}

func (d *daemonSlot) writeBinary(frame []byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_ = d.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return d.ws.WriteMessage(websocket.BinaryMessage, frame)
}

func (d *daemonSlot) closeAll() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	clients := make([]*clientSlot, 0, len(d.clients))
	for sid, cs := range d.clients {
		clients = append(clients, cs)
		delete(d.clients, sid)
	}
	d.mu.Unlock()
	for _, cs := range clients {
		cs.closeNow()
	}
	_ = d.ws.Close()
}

func (cs *clientSlot) enqueue(frame []byte) {
	cs.mu.Lock()
	if cs.closed {
		cs.mu.Unlock()
		return
	}
	if cs.queuedByte+len(frame) > maxClientQueueBytes {
		// Dropping one ciphertext frame would break the tunnel's Noise nonce
		// sequence, so a client this far behind is disconnected instead; its
		// read loop then tells the daemon the session closed.
		cs.mu.Unlock()
		cs.closeNow()
		return
	}
	cs.queue = append(cs.queue, frame)
	cs.queuedByte += len(frame)
	cs.mu.Unlock()
	select {
	case cs.wake <- struct{}{}:
	default:
	}
}

func (cs *clientSlot) closeNow() {
	cs.mu.Lock()
	if !cs.closed {
		cs.closed = true
		cs.queue = nil
		cs.queuedByte = 0
		close(cs.done)
	}
	cs.mu.Unlock()
	_ = cs.ws.Close()
}

func (cs *clientSlot) writeLoop() {
	for {
		cs.mu.Lock()
		if len(cs.queue) == 0 {
			closed := cs.closed
			cs.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-cs.wake:
				continue
			case <-cs.done:
				return
			}
		}
		frame := cs.queue[0]
		cs.queue = cs.queue[1:]
		cs.queuedByte -= len(frame)
		cs.mu.Unlock()

		_ = cs.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := cs.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			cs.closeNow()
			return
		}
	}
}
