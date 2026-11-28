// Package server hosts wadad's HTTP/WebSocket API, admission control,
// idempotent receipts, and sequenced event broadcasting.
package server

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
	"wada-desktop-go/internal/relay"
)

const (
	termSoftBytes    = 4 << 20 // 4 MiB soft cap for droppable terminal output frames
	maxOutboundBytes = 8 << 20 // 8 MiB per-socket high-water mark
	writeWait        = 10 * time.Second

	maxSubscriptionIDLen = 128
	binaryFrameTimeout   = 2 * time.Second
)

// Backend is the agent runtime contract hosted by wadad.
type Backend interface {
	Origin() string
	Events() <-chan protocol.Event
	Close() error

	Sessions(ctx context.Context, q protocol.SessionQuery) (protocol.SessionPage, error)
	Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error)
	Messages(ctx context.Context, id string) ([]protocol.Message, error)
	Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error)
	Abort(ctx context.Context, id string) error
	Reply(ctx context.Context, req protocol.ReplyRequest) error
	Rename(ctx context.Context, id, title string) error
	Delete(ctx context.Context, id string) error
	Spawn(ctx context.Context, req protocol.SpawnRequest) (protocol.Session, error)
	Rewind(ctx context.Context, id, msg string) (string, error)
	Changes(ctx context.Context, id string) ([]protocol.FileChange, error)
	Snapshot(ctx context.Context, id string) (map[string][]byte, error)
	Diff(ctx context.Context, id string, before map[string][]byte) ([]protocol.FileChange, error)
	Models(ctx context.Context) ([]protocol.Model, error)
	DefaultModel(ctx context.Context) (protocol.ModelRef, error)
	SetModel(ctx context.Context, id string, ref protocol.ModelRef) error
	Modes(ctx context.Context) ([]protocol.Mode, error)
	FindFiles(ctx context.Context, dir, query string, limit int) ([]protocol.FileEntry, error)
	ReadFile(ctx context.Context, dir, path string) ([]byte, error)
	FitImage(ctx context.Context, ref protocol.ModelRef, file protocol.File) (int, int, error)
	Skills(ctx context.Context, dir string) ([]protocol.Skill, error)
	Providers(ctx context.Context) ([]protocol.ProviderInfo, error)
	StartProviderLogin(ctx context.Context, req protocol.ProviderLoginStartRequest) (string, error)
	AnswerProviderLogin(ctx context.Context, loginID, value string) error
	CancelProviderLogin(ctx context.Context, loginID string) error
	LogoutProvider(ctx context.Context, providerID string) error
	PendingInteractions(ctx context.Context, id string) ([]protocol.Interaction, error)
	// PendingApprovals lists every approval still waiting for a reply.
	PendingApprovals(ctx context.Context) ([]protocol.PendingApproval, error)
	AnswerInteraction(ctx context.Context, req protocol.InteractionAnswer) error
}

// OrchestrationBackend is optionally implemented by Backends that support
// external CLI harnesses, supervised workers, and Git worktrees.
type OrchestrationBackend interface {
	Harnesses(ctx context.Context) ([]protocol.HarnessInfo, error)
	Workers(ctx context.Context, parentSession string) ([]protocol.WorkerInfo, error)
	ListWorktrees(ctx context.Context, dir string) ([]protocol.WorktreeInfo, error)
	CreateWorktree(ctx context.Context, dir, name, baseRef string) (protocol.WorktreeInfo, error)
	RemoveWorktree(ctx context.Context, dir, wtPath string, force bool) error
	WorktreePR(ctx context.Context, dir string) (*protocol.PRStatus, error)
	CreateWorktreePR(ctx context.Context, req protocol.WorktreeCreatePRRequest) (*protocol.PRStatus, error)
}

// TerminalBackend is optionally implemented by Backends that host detached PTYs.
type TerminalBackend interface {
	ListTerminals(ctx context.Context) ([]protocol.TerminalInfo, error)
	CreateTerminal(ctx context.Context, req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error)
	AttachTerminal(ctx context.Context, clientID string, req protocol.TerminalAttachRequest) (protocol.TerminalInfo, []byte, <-chan []byte, func(), error)
	DetachTerminal(ctx context.Context, clientID, termID string) error
	TerminalInput(ctx context.Context, termID string, data []byte) error
	TerminalResize(ctx context.Context, termID string, cols, rows int) error
	CaptureTerminal(ctx context.Context, termID string, lines int) (protocol.TerminalCaptureResponse, error)
	KillTerminal(ctx context.Context, termID string) error
}

// Config configures a wadad Server.
type Config struct {
	DataDir        string
	Listen         string
	Version        string
	PasswordHash   []byte
	Hostnames      []string
	RelayURL       string
	RelayToken     string
	AppURL         string
	PublicURL      string
	WebDir         string
	DesktopManaged bool
	Backend        Backend
	OnShutdown     func()
}

type queuedFrame struct {
	msgType int
	data    []byte
}

type termBinding struct {
	termID string
	detach func()
	cancel context.CancelFunc
}

type clientConn struct {
	id           string
	tr           relay.Transport
	server       *Server
	mu           sync.Mutex
	subs         map[string][]string // subscriptionID -> session filter (empty = all)
	termChannels map[uint8]termBinding
	termToChan   map[string]uint8
	nextChan     uint8
	queue        []queuedFrame
	queuedByte   int
	closed       bool
	wake         chan struct{}
	done         chan struct{}
	// local is set for a client that proved the local credential, which only
	// processes that can read the data directory have.
	local bool
}

// Server serves wadad's HTTP health endpoint and WebSocket RPC/stream protocol.
type Server struct {
	cfg         Config
	store       *store.Store
	serverID    string
	epoch       uint64
	credential  string
	remoteToken string
	keypair     relay.Keypair
	hostname    string

	authMu       sync.RWMutex
	passwordHash []byte
	authLimit    *authLimiter

	ctx       context.Context
	cancel    context.CancelFunc
	http      *http.Server
	ln        net.Listener
	relayConn *relay.DaemonConnector

	clientsMu sync.Mutex
	shutting  bool
	clients   map[*clientConn]struct{}
	seqBySess map[string]uint64
	wg        sync.WaitGroup
}

// New initializes a wadad server backed by cfg.DataDir and cfg.Backend.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("server: DataDir is required")
	}
	if cfg.Backend == nil {
		return nil, errors.New("server: Backend is required")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:6767"
	}
	if cfg.Version == "" {
		cfg.Version = "0.1.0-dev"
	}
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	sid, err := st.ServerID(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	ep, err := st.NextEpoch(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.PruneTimeline(ctx, ep); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.MarkPendingDeliveriesUnknown(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	cred, err := GenerateCredential()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	remoteTok, err := LoadOrCreateRemoteToken(cfg.DataDir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	kp, err := relay.LoadOrCreateKeypair(cfg.DataDir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	pwHash := slices.Clone(cfg.PasswordHash)
	if len(pwHash) == 0 {
		if diskHash, herr := LoadPasswordHash(cfg.DataDir); herr == nil && len(diskHash) > 0 {
			pwHash = diskHash
		}
	}
	host, _ := os.Hostname()
	sctx, cancel := context.WithCancel(ctx)
	s := &Server{
		cfg:          cfg,
		store:        st,
		serverID:     sid,
		epoch:        ep,
		credential:   cred,
		remoteToken:  remoteTok,
		keypair:      kp,
		passwordHash: pwHash,
		hostname:     host,
		ctx:          sctx,
		cancel:       cancel,
		clients:      map[*clientConn]struct{}{},
		seqBySess:    map[string]uint64{},
		authLimit:    newAuthLimiter(),
	}
	return s, nil
}

func (s *Server) ServerID() string   { return s.serverID }
func (s *Server) Epoch() uint64      { return s.epoch }
func (s *Server) Credential() string { return s.credential }
func (s *Server) RemoteToken() string {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return s.remoteToken
}
func (s *Server) RelayKeypair() relay.Keypair { return s.keypair }
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Start binds the TCP listener, writes runtime PID/credential files, and starts serving.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return "", err
	}
	s.ln = ln
	addr := ln.Addr().String()
	rec := PIDRecord{
		PID:            os.Getpid(),
		StartedAt:      time.Now().UnixMilli(),
		Listen:         addr,
		Version:        s.cfg.Version,
		DesktopManaged: s.cfg.DesktopManaged,
	}
	if err := WriteRuntimeFiles(s.cfg.DataDir, rec, s.credential); err != nil {
		_ = ln.Close()
		return "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/ws", s.handleWS)
	if s.cfg.WebDir != "" {
		mux.Handle("/", newWebHandler(s.cfg.WebDir))
	}
	s.http = &http.Server{
		Handler:           s.hostGuardMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.pumpEvents()
	}()
	go func() {
		defer s.wg.Done()
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.cancel()
		}
	}()
	if s.cfg.RelayURL != "" {
		rc, rerr := relay.StartDaemonConnector(s.cfg.RelayURL, s.cfg.RelayToken, s.keypair, func(tr relay.Transport, peer string) {
			s.ServeTransport(tr, relayPeerKey(peer))
		})
		if rerr != nil {
			_ = s.Shutdown(context.Background())
			return "", rerr
		}
		s.relayConn = rc
	}
	return addr, nil
}

// Shutdown stops accepting connections, closes clients, drains background pumps,
// and cleans up runtime PID/credential files.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancel()
	var err error
	if s.http != nil {
		err = s.http.Shutdown(ctx)
	}
	s.clientsMu.Lock()
	s.shutting = true
	conns := make([]*clientConn, 0, len(s.clients))
	for c := range s.clients {
		conns = append(conns, c)
	}
	rc := s.relayConn
	s.relayConn = nil
	s.clientsMu.Unlock()
	for _, c := range conns {
		c.closeNow()
	}
	if rc != nil {
		rc.Close()
	}
	s.wg.Wait()
	CleanupRuntimeFiles(s.cfg.DataDir)
	if cerr := s.cfg.Backend.Close(); err == nil {
		err = cerr
	}
	if serr := s.store.Close(); err == nil {
		err = serr
	}
	return err
}

func (s *Server) hostGuardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !HostAllowed(r.Host, s.cfg.Hostnames) {
			http.Error(w, "Host not allowed", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"serverId": s.serverID,
		"version":  s.cfg.Version,
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(req *http.Request) bool {
			return OriginAllowed(req, s.cfg.Hostnames)
		},
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	s.ServeTransport(ws, peer)
}

// relayPeerKey is the sign-in throttling key of a relay client: the address
// the relay reported, or one key shared by every client of a relay that
// reports none.
func relayPeerKey(addr string) string {
	if addr == "" {
		return "relay"
	}
	return "relay " + addr
}

// ServeTransport runs the hello/server_info handshake and multiplexed RPC/event/terminal
// loop over an accepted WebSocket or Noise NK relay tunnel. peer keys the
// throttling of failed sign-ins.
func (s *Server) ServeTransport(tr relay.Transport, peer string) {
	conn := &clientConn{
		id:           uuid.NewString(),
		tr:           tr,
		server:       s,
		subs:         map[string][]string{},
		termChannels: map[uint8]termBinding{},
		termToChan:   map[string]uint8{},
		wake:         make(chan struct{}, 1),
		done:         make(chan struct{}),
	}

	// First frame must be hello with valid credential or password.
	_ = tr.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := tr.ReadMessage()
	if err != nil {
		_ = tr.Close()
		return
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Type != protocol.TypeHello {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeUnauthorized, "expected hello handshake")
		_ = tr.Close()
		return
	}
	var hello protocol.HelloPayload
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeInvalid, "invalid hello payload")
		_ = tr.Close()
		return
	}
	local, ok, wait := s.authenticate(hello, peer)
	if wait > 0 {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeUnauthorized,
			fmt.Sprintf("too many failed sign-ins; try again in %s", wait.Round(time.Second)))
		_ = tr.Close()
		return
	}
	if !ok {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeUnauthorized, "invalid daemon credential or password")
		_ = tr.Close()
		return
	}
	conn.local = local
	_ = tr.SetReadDeadline(time.Time{})

	info := protocol.ServerInfoPayload{
		ServerID:        s.serverID,
		Hostname:        s.hostname,
		Version:         s.cfg.Version,
		ProtocolVersion: protocol.ProtocolVersion,
		Origin:          s.cfg.Backend.Origin(),
		Features: []string{
			protocol.CapOwnedSubscriptions,
			protocol.CapCreationReceipts,
			protocol.CapBinaryTransfer,
		},
	}
	rawInfo, _ := json.Marshal(info)
	if err := writeJSONFrame(tr, protocol.Envelope{
		Type:      protocol.TypeServerInfo,
		RequestID: env.RequestID,
		Payload:   rawInfo,
	}); err != nil {
		_ = tr.Close()
		return
	}

	s.clientsMu.Lock()
	if s.shutting {
		s.clientsMu.Unlock()
		_ = tr.Close()
		return
	}
	s.clients[conn] = struct{}{}
	s.wg.Add(2) // writeLoop + readLoop
	s.clientsMu.Unlock()

	go func() {
		defer s.wg.Done()
		conn.writeLoop()
	}()
	defer s.wg.Done()
	conn.readLoop()
}

func writeJSONFrame(tr relay.Transport, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_ = tr.SetWriteDeadline(time.Now().Add(writeWait))
	return tr.WriteMessage(websocket.TextMessage, raw)
}

// authenticate reports whether hello may connect, and whether it did so with
// the local credential. A peer with too many recent failures gets wait > 0
// and is not checked; the local credential is never throttled.
func (s *Server) authenticate(hello protocol.HelloPayload, peer string) (local, ok bool, wait time.Duration) {
	if hello.Credential != "" && subtle.ConstantTimeCompare([]byte(hello.Credential), []byte(s.credential)) == 1 {
		return true, true, 0
	}
	if wait := s.authLimit.retryAfter(peer); wait > 0 {
		return false, false, wait
	}
	s.authMu.RLock()
	pwHash := slices.Clone(s.passwordHash)
	remoteTok := s.remoteToken
	s.authMu.RUnlock()
	if len(pwHash) > 0 {
		ok = hello.Password != "" && bcrypt.CompareHashAndPassword(pwHash, []byte(hello.Password)) == nil
	} else {
		ok = hello.Credential != "" && remoteTok != "" && subtle.ConstantTimeCompare([]byte(hello.Credential), []byte(remoteTok)) == 1
	}
	if ok {
		s.authLimit.succeed(peer)
	} else {
		s.authLimit.fail(peer)
	}
	return false, ok, 0
}

func (s *Server) pumpEvents() {
	events := s.cfg.Backend.Events()
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			ev.Epoch = s.epoch
			if ev.Session != "" {
				s.seqBySess[ev.Session]++
				ev.Seq = s.seqBySess[ev.Session]
				s.recordEvent(ev)
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			s.broadcastEvent(ev.Session, payload)
		}
	}
}

// recordEvent stores ev for timeline catch-up and applies the timeline's
// retention when a turn ends or the session goes away.
func (s *Server) recordEvent(ev protocol.Event) {
	if ev.Kind == protocol.EventSessionDeleted {
		_ = s.store.DeleteSessionTimeline(s.ctx, ev.Session)
		delete(s.seqBySess, ev.Session)
		return
	}
	_ = s.store.AppendEvent(s.ctx, ev)
	switch ev.Kind {
	case protocol.EventTurnSucceeded, protocol.EventTurnFailed, protocol.EventTurnInterrupted:
		_ = s.store.TrimSessionTimeline(s.ctx, ev.Session, ev.Epoch, ev.Seq)
	case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded,
		protocol.EventToolStarted, protocol.EventToolCalled, protocol.EventToolFinished,
		protocol.EventApprovalRequested, protocol.EventInteractionRequested, protocol.EventTurnStarted,
		protocol.EventModelChanged, protocol.EventSessionRenamed, protocol.EventSessionDeleted,
		protocol.EventModelsChanged, protocol.EventProviderAuth, protocol.EventWorkerUpdated,
		protocol.EventTerminalUpdated:
	}
}

func (s *Server) broadcastEvent(sessionID string, payload json.RawMessage) {
	s.clientsMu.Lock()
	conns := make([]*clientConn, 0, len(s.clients))
	for c := range s.clients {
		conns = append(conns, c)
	}
	s.clientsMu.Unlock()

	for _, c := range conns {
		c.mu.Lock()
		var subIDs []string
		for id, filter := range c.subs {
			if len(filter) == 0 || (sessionID != "" && slices.Contains(filter, sessionID)) {
				subIDs = append(subIDs, id)
			}
		}
		c.mu.Unlock()
		for _, subID := range subIDs {
			raw, err := json.Marshal(protocol.Envelope{
				Type:           protocol.TypeEvent,
				SubscriptionID: subID,
				Payload:        payload,
			})
			if err == nil {
				c.enqueue(websocket.TextMessage, raw)
			}
		}
	}
}

func (c *clientConn) writeImmediateError(reqID, code, msg string) {
	_ = writeJSONFrame(c.tr, protocol.Envelope{
		Type:      protocol.TypeRPCResponse,
		RequestID: reqID,
		Error:     &protocol.RPCError{Code: code, Message: msg},
	})
}

func (c *clientConn) enqueue(msgType int, data []byte) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if c.queuedByte+len(data) > maxOutboundBytes {
		c.closed = true
		c.queue = nil
		c.queuedByte = 0
		c.mu.Unlock()
		_ = c.tr.Close()
		return
	}
	c.queue = append(c.queue, queuedFrame{msgType: msgType, data: data})
	c.queuedByte += len(data)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *clientConn) enqueueTermOutput(data []byte) {
	c.mu.Lock()
	if c.closed || c.queuedByte+len(data) > termSoftBytes {
		c.mu.Unlock()
		return
	}
	c.queue = append(c.queue, queuedFrame{msgType: websocket.BinaryMessage, data: data})
	c.queuedByte += len(data)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *clientConn) closeNow() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.queue = nil
		c.queuedByte = 0
		close(c.done)
	}
	bindings := make([]termBinding, 0, len(c.termChannels))
	for chID, b := range c.termChannels {
		bindings = append(bindings, b)
		delete(c.termChannels, chID)
	}
	clear(c.termToChan)
	c.mu.Unlock()
	for _, b := range bindings {
		if b.cancel != nil {
			b.cancel()
		}
		if b.detach != nil {
			b.detach()
		}
	}
	_ = c.tr.Close()
}

func (c *clientConn) writeLoop() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			closed := c.closed
			c.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-c.wake:
				continue
			case <-c.done:
				return
			case <-c.server.ctx.Done():
				return
			}
		}
		frame := c.queue[0]
		c.queue = c.queue[1:]
		c.queuedByte -= len(frame.data)
		c.mu.Unlock()

		_ = c.tr.SetWriteDeadline(time.Now().Add(writeWait))
		if err := c.tr.WriteMessage(frame.msgType, frame.data); err != nil {
			c.closeNow()
			return
		}
	}
}

func (c *clientConn) readLoop() {
	defer func() {
		c.server.clientsMu.Lock()
		delete(c.server.clients, c)
		c.server.clientsMu.Unlock()
		c.closeNow()
	}()
	for {
		msgType, raw, err := c.tr.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.BinaryMessage {
			c.handleBinaryFrame(raw)
			continue
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		switch env.Type {
		case protocol.TypePing:
			resp, _ := json.Marshal(protocol.Envelope{Type: protocol.TypePong, RequestID: env.RequestID, Payload: env.Payload})
			c.enqueue(websocket.TextMessage, resp)
		case protocol.TypeRPCRequest:
			c.server.clientsMu.Lock()
			shutting := c.server.shutting
			if !shutting {
				c.server.wg.Add(1)
			}
			c.server.clientsMu.Unlock()
			if shutting {
				return
			}
			go func(req protocol.Envelope) {
				defer c.server.wg.Done()
				c.handleRPC(req)
			}(env)
		}
	}
}

func (c *clientConn) handleRPC(req protocol.Envelope) {
	res, rpcErr := c.server.dispatchRPC(c.server.ctx, c, req.Method, req.Payload)
	resp := protocol.Envelope{
		Type:      protocol.TypeRPCResponse,
		RequestID: req.RequestID,
		Method:    req.Method,
	}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else if res != nil {
		raw, err := json.Marshal(res)
		if err != nil {
			resp.Error = &protocol.RPCError{Code: protocol.ErrCodeInternal, Message: err.Error()}
		} else {
			resp.Payload = raw
		}
	}
	data, err := json.Marshal(resp)
	if err == nil {
		c.enqueue(websocket.TextMessage, data)
	}
}

func (s *Server) dispatchRPC(ctx context.Context, c *clientConn, method string, payload json.RawMessage) (any, *protocol.RPCError) {
	switch method {
	case protocol.MethodDaemonStatus:
		return protocol.DaemonStatusResponse{
			ServerID:       s.serverID,
			Version:        s.cfg.Version,
			Origin:         s.cfg.Backend.Origin(),
			PID:            os.Getpid(),
			Epoch:          s.epoch,
			DesktopManaged: s.cfg.DesktopManaged,
		}, nil
	case protocol.MethodDaemonShutdown:
		if s.cfg.OnShutdown != nil {
			go s.cfg.OnShutdown()
		} else {
			go func() {
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.Shutdown(shutCtx)
			}()
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodDaemonPairingOffer:
		var req protocol.PairingOfferRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		return s.pairingOffer(req.AppURL)
	case protocol.MethodDaemonSetPassword:
		var req protocol.SetPasswordRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		if err := s.setPassword(c, req.Password); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodEventsSubscribe:
		var req protocol.SubscribeRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		subID := req.SubscriptionID
		if subID == "" {
			subID = "sub_" + uuid.NewString()
		} else if len(subID) > maxSubscriptionIDLen {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeInvalid, Message: "subscriptionId is too long"}
		}
		c.mu.Lock()
		if _, taken := c.subs[subID]; taken {
			c.mu.Unlock()
			return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "subscriptionId is already in use"}
		}
		c.subs[subID] = slices.Clone(req.Sessions)
		c.mu.Unlock()
		return protocol.SubscribeResponse{SubscriptionID: subID}, nil
	case protocol.MethodEventsUnsubscribe:
		var req protocol.UnsubscribeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		c.mu.Lock()
		delete(c.subs, req.SubscriptionID)
		c.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	case protocol.MethodTimelineFetch:
		var req protocol.TimelineFetchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		events, ep, hasMore, err := s.store.FetchTimeline(ctx, req.Session, req.Epoch, req.AfterSeq, req.Limit)
		if err != nil {
			return nil, internalErr(err)
		}
		if ep == 0 {
			ep = s.epoch
		}
		return protocol.TimelineFetchResponse{Epoch: ep, Events: events, HasMore: hasMore}, nil
	case protocol.MethodSessionsList:
		var req protocol.SessionQuery
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		page, err := s.cfg.Backend.Sessions(ctx, req)
		if err != nil {
			return nil, internalErr(err)
		}
		return page, nil
	case protocol.MethodSessionCreate:
		var req protocol.CreateSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		return s.createWithReceipt(ctx, req)
	case protocol.MethodSessionSpawn:
		var req protocol.SpawnRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		return s.spawnWithReceipt(ctx, req)
	case protocol.MethodSessionMessages:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		msgs, err := s.cfg.Backend.Messages(ctx, req.Session)
		if err != nil {
			return nil, internalErr(err)
		}
		return msgs, nil
	case protocol.MethodSessionSend:
		var req protocol.SendRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		return s.sendWithReceipt(ctx, req)
	case protocol.MethodSessionAbort:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.Abort(ctx, req.Session); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodSessionReply:
		var req protocol.ReplyRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.Reply(ctx, req); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodSessionRename:
		var req protocol.RenameRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.Rename(ctx, req.Session, req.Title); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodSessionDelete:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.Delete(ctx, req.Session); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodSessionRewind:
		var req protocol.RewindRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		id, err := s.cfg.Backend.Rewind(ctx, req.Session, req.Message)
		if err != nil {
			return nil, internalErr(err)
		}
		return protocol.RewindResponse{Session: id}, nil
	case protocol.MethodSessionChanges:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		changes, err := s.cfg.Backend.Changes(ctx, req.Session)
		if err != nil {
			return nil, internalErr(err)
		}
		return changes, nil
	case protocol.MethodSessionSnapshot:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		snap, err := s.cfg.Backend.Snapshot(ctx, req.Session)
		if err != nil {
			return nil, internalErr(err)
		}
		return protocol.SnapshotResponse{Files: snap}, nil
	case protocol.MethodSessionDiff:
		var req protocol.DiffRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		changes, err := s.cfg.Backend.Diff(ctx, req.Session, req.Before)
		if err != nil {
			return nil, internalErr(err)
		}
		return changes, nil
	case protocol.MethodModelsList:
		models, err := s.cfg.Backend.Models(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return models, nil
	case protocol.MethodModelsDefault:
		ref, err := s.cfg.Backend.DefaultModel(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return ref, nil
	case protocol.MethodSessionSetModel:
		var req protocol.SetModelRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.SetModel(ctx, req.Session, req.Model); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodModesList:
		modes, err := s.cfg.Backend.Modes(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return modes, nil
	case protocol.MethodFilesFind:
		var req protocol.FilesFindRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		entries, err := s.cfg.Backend.FindFiles(ctx, req.Directory, req.Query, req.Limit)
		if err != nil {
			return nil, internalErr(err)
		}
		return entries, nil
	case protocol.MethodFilesRead:
		var req protocol.FilesReadRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		data, err := s.cfg.Backend.ReadFile(ctx, req.Directory, req.Path)
		if err != nil {
			return nil, internalErr(err)
		}
		return protocol.FilesReadResponse{Data: data}, nil
	case protocol.MethodImagesFit:
		var req protocol.FitImageRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		w, h, err := s.cfg.Backend.FitImage(ctx, req.Model, req.File)
		if err != nil {
			return nil, internalErr(err)
		}
		return protocol.FitImageResponse{Width: w, Height: h}, nil
	case protocol.MethodSkillsList:
		var req protocol.SkillsListRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		skills, err := s.cfg.Backend.Skills(ctx, req.Directory)
		if err != nil {
			return nil, internalErr(err)
		}
		return skills, nil
	case protocol.MethodProvidersList:
		list, err := s.cfg.Backend.Providers(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return list, nil
	case protocol.MethodProviderLoginStart:
		var req protocol.ProviderLoginStartRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		loginID, err := s.cfg.Backend.StartProviderLogin(ctx, req)
		if err != nil {
			return nil, internalErr(err)
		}
		return protocol.ProviderLoginStartResponse{LoginID: loginID}, nil
	case protocol.MethodProviderLoginAnswer:
		var req protocol.ProviderLoginAnswerRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.AnswerProviderLogin(ctx, req.LoginID, req.Value); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodProviderLoginCancel:
		var req protocol.ProviderLoginCancelRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.CancelProviderLogin(ctx, req.LoginID); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodProviderLogout:
		var req protocol.ProviderLogoutRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.LogoutProvider(ctx, req.ProviderID); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodInteractionsPending:
		var req protocol.SessionIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		pending, err := s.cfg.Backend.PendingInteractions(ctx, req.Session)
		if err != nil {
			return nil, internalErr(err)
		}
		return pending, nil
	case protocol.MethodApprovalsPending:
		pending, err := s.cfg.Backend.PendingApprovals(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return pending, nil
	case protocol.MethodInteractionsAnswer:
		var req protocol.InteractionAnswer
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.AnswerInteraction(ctx, req); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodHarnessesList:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		list, err := orch.Harnesses(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return list, nil
	case protocol.MethodWorkersList:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorkersListRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		list, err := orch.Workers(ctx, req.ParentSession)
		if err != nil {
			return nil, internalErr(err)
		}
		return list, nil
	case protocol.MethodWorktreesList:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorktreesListRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		list, err := orch.ListWorktrees(ctx, req.Directory)
		if err != nil {
			return nil, internalErr(err)
		}
		return list, nil
	case protocol.MethodWorktreeCreate:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorktreeCreateRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		wt, err := orch.CreateWorktree(ctx, req.Directory, req.Name, req.BaseRef)
		if err != nil {
			return nil, internalErr(err)
		}
		return wt, nil
	case protocol.MethodWorktreeRemove:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorktreeRemoveRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := orch.RemoveWorktree(ctx, req.Directory, req.Path, req.Force); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodWorktreePR:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorktreePRRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		pr, err := orch.WorktreePR(ctx, req.Directory)
		if err != nil {
			return nil, internalErr(err)
		}
		return pr, nil
	case protocol.MethodWorktreeCreatePR:
		orch, ok := s.cfg.Backend.(OrchestrationBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "orchestration is not supported"}
		}
		var req protocol.WorktreeCreatePRRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		pr, err := orch.CreateWorktreePR(ctx, req)
		if err != nil {
			return nil, internalErr(err)
		}
		return pr, nil
	case protocol.MethodTerminalsList:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		list, err := tb.ListTerminals(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		return list, nil
	case protocol.MethodTerminalCreate:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalCreateRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		info, err := tb.CreateTerminal(ctx, req)
		if err != nil {
			return nil, internalErr(err)
		}
		return info, nil
	case protocol.MethodTerminalAttach:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalAttachRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		info, snap, outCh, detach, err := tb.AttachTerminal(ctx, c.id, req)
		if err != nil {
			return nil, internalErr(err)
		}
		chID, berr := c.bindTerminalChannel(req.ID, outCh, detach)
		if berr != nil {
			detach()
			return nil, internalErr(berr)
		}
		return protocol.TerminalAttachResponse{Channel: chID, Info: info, Snapshot: snap}, nil
	case protocol.MethodTerminalDetach:
		var req protocol.TerminalDetachRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		c.unbindTerminalChannel(req.ID)
		return map[string]bool{"ok": true}, nil
	case protocol.MethodTerminalInput:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalInputRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := tb.TerminalInput(ctx, req.ID, req.Data); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodTerminalResize:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalResizeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := tb.TerminalResize(ctx, req.ID, req.Cols, req.Rows); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodTerminalCapture:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalCaptureRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		resp, err := tb.CaptureTerminal(ctx, req.ID, req.Lines)
		if err != nil {
			return nil, internalErr(err)
		}
		return resp, nil
	case protocol.MethodTerminalKill:
		tb, ok := s.cfg.Backend.(TerminalBackend)
		if !ok {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
		}
		var req protocol.TerminalKillRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		c.unbindTerminalChannel(req.ID)
		if err := tb.KillTerminal(ctx, req.ID); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: fmt.Sprintf("unknown method %q", method)}
	}
}

func (s *Server) createWithReceipt(ctx context.Context, req protocol.CreateSessionRequest) (any, *protocol.RPCError) {
	return s.withCreationReceipt(ctx, req.IdempotencyKey, "session", hashStruct(req),
		func(id string) any {
			if sess, found := s.lookupSession(ctx, id); found {
				return sess
			}
			return protocol.Session{ID: id, Title: req.Title, Directory: req.Directory, Model: req.Model}
		},
		func() (protocol.Session, error) { return s.cfg.Backend.Create(ctx, req) })
}

func (s *Server) spawnWithReceipt(ctx context.Context, req protocol.SpawnRequest) (any, *protocol.RPCError) {
	return s.withCreationReceipt(ctx, req.IdempotencyKey, "spawn", hashStruct(req),
		func(id string) any {
			return protocol.Session{ID: id, Title: req.Name, Directory: req.Directory, Model: req.Model}
		},
		func() (protocol.Session, error) { return s.cfg.Backend.Spawn(ctx, req) })
}

// withCreationReceipt runs create at most once per idempotency key. The key is
// reserved before create runs, so a concurrent or later request with the same
// key gets the first one's resource (from existing) instead of a second one.
func (s *Server) withCreationReceipt(ctx context.Context, key, kind, reqHash string, existing func(id string) any, create func() (protocol.Session, error)) (any, *protocol.RPCError) {
	if key == "" {
		sess, err := create()
		if err != nil {
			return nil, internalErr(err)
		}
		return sess, nil
	}
	reserved, rec, err := s.store.ReserveCreationReceipt(ctx, store.CreationReceipt{IdempotencyKey: key, Kind: kind, RequestHash: reqHash})
	if err != nil {
		return nil, internalErr(err)
	}
	if !reserved {
		if rec.Kind != kind || rec.RequestHash != reqHash {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "idempotencyKey reused with different arguments"}
		}
		if rec.ResourceID == "" {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeOutcomeUnknown, Message: "a creation with this idempotencyKey is in progress or its outcome is unknown"}
		}
		return existing(rec.ResourceID), nil
	}
	sess, err := create()
	if sess.ID != "" {
		// The resource exists; a lost receipt only means a retry with this key
		// is refused as unknown instead of answered.
		if perr := s.store.PutCreationReceipt(context.WithoutCancel(ctx), store.CreationReceipt{IdempotencyKey: key, Kind: kind, ResourceID: sess.ID, RequestHash: reqHash}); perr != nil {
			log.Printf("wadad: record %s receipt for %s: %v", kind, sess.ID, perr)
		}
	} else if err != nil {
		// The backend reports nothing was created, so the key may be used again.
		_ = s.store.DeleteCreationReceipt(context.WithoutCancel(ctx), key)
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return sess, nil
}

func (s *Server) sendWithReceipt(ctx context.Context, req protocol.SendRequest) (any, *protocol.RPCError) {
	var reqHash string
	if req.MessageID != "" {
		reqHash = hashStruct(req)
		rec, ok, err := s.store.GetDeliveryReceipt(ctx, req.Session, req.MessageID)
		if err != nil {
			return nil, internalErr(err)
		}
		if ok {
			if rec.RequestHash != reqHash {
				return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "messageId reused with different arguments"}
			}
			switch rec.Status {
			case store.DeliveryAccepted:
				return protocol.SendResponse{Disposition: protocol.SendAccepted}, nil
			case store.DeliveryUnknown:
				return nil, &protocol.RPCError{Code: protocol.ErrCodeOutcomeUnknown, Message: "previous delivery outcome is unknown after interruption"}
			case store.DeliveryPending:
				return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "message delivery already in progress"}
			case store.DeliveryRejected:
				if err := s.store.UpdateDeliveryReceipt(ctx, req.Session, req.MessageID, store.DeliveryPending); err != nil {
					return nil, internalErr(err)
				}
			}
		} else {
			if err := s.store.PutDeliveryReceipt(ctx, store.DeliveryReceipt{
				SessionID:   req.Session,
				MessageID:   req.MessageID,
				RequestHash: reqHash,
				Status:      store.DeliveryPending,
			}); err != nil {
				return nil, internalErr(err)
			}
		}
	}
	disp, err := s.cfg.Backend.Send(ctx, req)
	if err != nil {
		if errors.Is(err, pig.ErrNotSent) {
			if req.MessageID != "" {
				_ = s.store.UpdateDeliveryReceipt(ctx, req.Session, req.MessageID, store.DeliveryRejected)
			}
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotSent, Message: err.Error()}
		}
		if req.MessageID != "" {
			_ = s.store.UpdateDeliveryReceipt(ctx, req.Session, req.MessageID, store.DeliveryUnknown)
		}
		return nil, &protocol.RPCError{Code: protocol.ErrCodeOutcomeUnknown, Message: err.Error()}
	}
	if req.MessageID != "" {
		_ = s.store.UpdateDeliveryReceipt(ctx, req.Session, req.MessageID, store.DeliveryAccepted)
	}
	return protocol.SendResponse{Disposition: disp}, nil
}

func (s *Server) lookupSession(ctx context.Context, id string) (protocol.Session, bool) {
	cursor := ""
	for range 20 {
		page, err := s.cfg.Backend.Sessions(ctx, protocol.SessionQuery{Cursor: cursor})
		if err != nil {
			return protocol.Session{}, false
		}
		for _, sess := range page.Sessions {
			if sess.ID == id {
				return sess, true
			}
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	return protocol.Session{}, false
}

func hashStruct(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func invalidErr(err error) *protocol.RPCError {
	return &protocol.RPCError{Code: protocol.ErrCodeInvalid, Message: err.Error()}
}

func internalErr(err error) *protocol.RPCError {
	return &protocol.RPCError{Code: protocol.ErrCodeInternal, Message: err.Error()}
}

func (s *Server) pairingOffer(appURL string) (protocol.PairingOfferResponse, *protocol.RPCError) {
	s.authMu.RLock()
	requiresPW := len(s.passwordHash) > 0
	s.authMu.RUnlock()

	directURL := s.cfg.PublicURL
	if directURL == "" && s.cfg.RelayURL == "" && s.Addr() != "" {
		directURL = "ws://" + s.Addr()
	}
	pubStr := relay.EncodeKey(s.keypair.Public)
	offer := relay.Offer{
		Version:          relay.OfferVersion,
		RelayURL:         s.cfg.RelayURL,
		DirectURL:        directURL,
		ServerID:         s.serverID,
		Hostname:         s.hostname,
		ServerPubKey:     pubStr,
		RequiresPassword: requiresPW,
	}
	if !requiresPW {
		offer.Token = s.RemoteToken()
	}
	enc, err := relay.EncodeOffer(offer)
	if err != nil {
		return protocol.PairingOfferResponse{}, internalErr(err)
	}
	link, err := relay.OfferURL(cmp.Or(appURL, s.cfg.AppURL), offer)
	if err != nil {
		return protocol.PairingOfferResponse{}, internalErr(err)
	}
	return protocol.PairingOfferResponse{
		Offer:            enc,
		URL:              link,
		RelayURL:         offer.RelayURL,
		DirectURL:        offer.DirectURL,
		ServerID:         s.serverID,
		ServerPubKey:     pubStr,
		RequiresPassword: requiresPW,
	}, nil
}

// setPassword replaces the daemon password and the remote pairing token, and
// disconnects every remote client except caller: whoever got in with the old
// password or token must authenticate again.
func (s *Server) setPassword(caller *clientConn, password string) error {
	var hash []byte
	if password != "" {
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	tok, err := RotateRemoteToken(s.cfg.DataDir)
	if err != nil {
		return err
	}
	if err := SavePasswordHash(s.cfg.DataDir, hash); err != nil {
		return err
	}
	s.authMu.Lock()
	s.passwordHash = hash
	s.remoteToken = tok
	s.authMu.Unlock()

	s.clientsMu.Lock()
	var remote []*clientConn
	for c := range s.clients {
		if c != caller && !c.local {
			remote = append(remote, c)
		}
	}
	s.clientsMu.Unlock()
	for _, c := range remote {
		c.closeNow()
	}
	return nil
}

func newWebHandler(dir string) http.Handler {
	fsys := os.DirFS(dir)
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+r.URL.Path)), "/")
		if clean == "" {
			clean = "index.html"
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean))); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, clean, "index.html")); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func (c *clientConn) bindTerminalChannel(termID string, outCh <-chan []byte, detach func()) (uint8, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, errors.New("connection closed")
	}
	if oldCh, exists := c.termToChan[termID]; exists {
		old := c.termChannels[oldCh]
		delete(c.termChannels, oldCh)
		delete(c.termToChan, termID)
		if old.cancel != nil {
			old.cancel()
		}
		if old.detach != nil {
			old.detach()
		}
	}
	var chID uint8
	for range 255 {
		c.nextChan++
		if c.nextChan == 0 {
			c.nextChan = 1
		}
		if _, used := c.termChannels[c.nextChan]; !used {
			chID = c.nextChan
			break
		}
	}
	if chID == 0 {
		c.mu.Unlock()
		return 0, errors.New("maximum attached terminals reached")
	}
	bctx, cancel := context.WithCancel(c.server.ctx)
	c.termChannels[chID] = termBinding{termID: termID, detach: detach, cancel: cancel}
	c.termToChan[termID] = chID
	c.server.clientsMu.Lock()
	shutting := c.server.shutting
	if !shutting {
		c.server.wg.Add(1)
	}
	c.server.clientsMu.Unlock()
	c.mu.Unlock()

	if shutting {
		cancel()
		return 0, errors.New("server shutting down")
	}

	go func() {
		defer c.server.wg.Done()
		for {
			select {
			case <-bctx.Done():
				return
			case <-c.done:
				return
			case chunk, ok := <-outCh:
				if !ok {
					// A detach cancels bctx before it closes outCh; otherwise
					// the terminal ended, so tell the client and free the channel.
					if bctx.Err() == nil {
						c.endTerminalChannel(termID, chID)
					}
					return
				}
				c.enqueueTermOutput(protocol.EncodeBinaryFrame(protocol.OpTermOutput, chID, chunk))
			}
		}
	}()
	return chID, nil
}

// endTerminalChannel sends OpTermExit for chID and releases it, if it is still
// termID's channel. The frame is queued after the terminal's last output and,
// being tiny, regardless of the output backlog.
func (c *clientConn) endTerminalChannel(termID string, chID uint8) {
	c.mu.Lock()
	b, ok := c.termChannels[chID]
	if ok && c.termToChan[termID] == chID {
		delete(c.termChannels, chID)
		delete(c.termToChan, termID)
	} else {
		ok = false
	}
	if ok && !c.closed {
		frame := protocol.EncodeBinaryFrame(protocol.OpTermExit, chID, nil)
		c.queue = append(c.queue, queuedFrame{msgType: websocket.BinaryMessage, data: frame})
		c.queuedByte += len(frame)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.detach != nil {
		b.detach()
	}
}

func (c *clientConn) unbindTerminalChannel(termID string) {
	c.mu.Lock()
	chID, ok := c.termToChan[termID]
	var b termBinding
	if ok {
		b = c.termChannels[chID]
		delete(c.termChannels, chID)
		delete(c.termToChan, termID)
	}
	c.mu.Unlock()
	if ok {
		if b.cancel != nil {
			b.cancel()
		}
		if b.detach != nil {
			b.detach()
		}
	}
}

func (c *clientConn) handleBinaryFrame(raw []byte) {
	op, chID, payload, err := protocol.DecodeBinaryFrame(raw)
	if err != nil {
		return
	}
	tb, ok := c.server.cfg.Backend.(TerminalBackend)
	if !ok {
		return
	}
	c.mu.Lock()
	b, bound := c.termChannels[chID]
	c.mu.Unlock()
	if !bound {
		return
	}
	// Binary frames are handled on the read loop, so bound each call: a slow
	// terminal worker must not stall this connection's RPCs and, over the
	// relay, every other tunnel sharing it.
	ctx, cancel := context.WithTimeout(c.server.ctx, binaryFrameTimeout)
	defer cancel()
	switch op {
	case protocol.OpTermInput:
		_ = tb.TerminalInput(ctx, b.termID, payload)
	case protocol.OpTermResize:
		if cols, rows, derr := protocol.DecodeTermResize(payload); derr == nil {
			_ = tb.TerminalResize(ctx, b.termID, cols, rows)
		}
	}
}
