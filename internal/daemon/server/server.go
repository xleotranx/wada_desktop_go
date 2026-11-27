// Package server hosts wadad's HTTP/WebSocket API, admission control,
// idempotent receipts, and sequenced event broadcasting.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

const (
	maxOutboundBytes = 8 << 20 // 8 MiB per-socket high-water mark
	writeWait        = 10 * time.Second
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
	Skills(ctx context.Context, dir string) ([]protocol.Skill, error)
	PendingInteractions(ctx context.Context, id string) ([]protocol.Interaction, error)
	AnswerInteraction(ctx context.Context, req protocol.InteractionAnswer) error
}

// Config configures a wadad Server.
type Config struct {
	DataDir        string
	Listen         string
	Version        string
	PasswordHash   []byte
	Hostnames      []string
	DesktopManaged bool
	Backend        Backend
	OnShutdown     func()
}

type queuedFrame struct {
	msgType int
	data    []byte
}

type clientConn struct {
	id         string
	ws         *websocket.Conn
	server     *Server
	mu         sync.Mutex
	subs       map[string][]string // subscriptionID -> session filter (empty = all)
	queue      []queuedFrame
	queuedByte int
	closed     bool
	wake       chan struct{}
	done       chan struct{}
}

// Server serves wadad's HTTP health endpoint and WebSocket RPC/stream protocol.
type Server struct {
	cfg        Config
	store      *store.Store
	serverID   string
	epoch      uint64
	credential string
	hostname   string

	ctx    context.Context
	cancel context.CancelFunc
	http   *http.Server
	ln     net.Listener

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
	if err := st.MarkPendingDeliveriesUnknown(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	cred, err := GenerateCredential()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	host, _ := os.Hostname()
	sctx, cancel := context.WithCancel(ctx)
	s := &Server{
		cfg:        cfg,
		store:      st,
		serverID:   sid,
		epoch:      ep,
		credential: cred,
		hostname:   host,
		ctx:        sctx,
		cancel:     cancel,
		clients:    map[*clientConn]struct{}{},
		seqBySess:  map[string]uint64{},
	}
	return s, nil
}

func (s *Server) ServerID() string { return s.serverID }
func (s *Server) Epoch() uint64    { return s.epoch }
func (s *Server) Credential() string {
	return s.credential
}
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
	s.clientsMu.Unlock()
	for _, c := range conns {
		c.closeNow()
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
	conn := &clientConn{
		id:     uuid.NewString(),
		ws:     ws,
		server: s,
		subs:   map[string][]string{},
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}

	// First frame must be hello with valid credential or password.
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		_ = ws.Close()
		return
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Type != protocol.TypeHello {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeUnauthorized, "expected hello handshake")
		_ = ws.Close()
		return
	}
	var hello protocol.HelloPayload
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeInvalid, "invalid hello payload")
		_ = ws.Close()
		return
	}
	if !s.authenticate(hello) {
		conn.writeImmediateError(env.RequestID, protocol.ErrCodeUnauthorized, "invalid daemon credential or password")
		_ = ws.Close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

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
	if err := ws.WriteJSON(protocol.Envelope{
		Type:      protocol.TypeServerInfo,
		RequestID: env.RequestID,
		Payload:   rawInfo,
	}); err != nil {
		_ = ws.Close()
		return
	}

	s.clientsMu.Lock()
	if s.shutting {
		s.clientsMu.Unlock()
		_ = ws.Close()
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

func (s *Server) authenticate(hello protocol.HelloPayload) bool {
	if hello.Credential != "" && subtle.ConstantTimeCompare([]byte(hello.Credential), []byte(s.credential)) == 1 {
		return true
	}
	if len(s.cfg.PasswordHash) > 0 && hello.Password != "" {
		return bcrypt.CompareHashAndPassword(s.cfg.PasswordHash, []byte(hello.Password)) == nil
	}
	return false
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
				_ = s.store.AppendEvent(s.ctx, ev)
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			s.broadcastEvent(ev.Session, payload)
		}
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
	_ = c.ws.WriteJSON(protocol.Envelope{
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
		_ = c.ws.Close()
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

func (c *clientConn) closeNow() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.queue = nil
		c.queuedByte = 0
		close(c.done)
	}
	c.mu.Unlock()
	_ = c.ws.Close()
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

		_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
		if err := c.ws.WriteMessage(frame.msgType, frame.data); err != nil {
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
		msgType, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
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
	case protocol.MethodEventsSubscribe:
		var req protocol.SubscribeRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, invalidErr(err)
			}
		}
		subID := "sub_" + uuid.NewString()
		c.mu.Lock()
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
	case protocol.MethodInteractionsAnswer:
		var req protocol.InteractionAnswer
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		if err := s.cfg.Backend.AnswerInteraction(ctx, req); err != nil {
			return nil, internalErr(err)
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: fmt.Sprintf("unknown method %q", method)}
	}
}

func (s *Server) createWithReceipt(ctx context.Context, req protocol.CreateSessionRequest) (any, *protocol.RPCError) {
	if req.IdempotencyKey != "" {
		reqHash := hashStruct(req)
		rec, ok, err := s.store.GetCreationReceipt(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, internalErr(err)
		}
		if ok {
			if rec.Kind != "session" || rec.RequestHash != reqHash {
				return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "idempotencyKey reused with different arguments"}
			}
			if sess, found := s.lookupSession(ctx, rec.ResourceID); found {
				return sess, nil
			}
			return protocol.Session{ID: rec.ResourceID, Title: req.Title, Directory: req.Directory, Model: req.Model}, nil
		}
	}
	sess, err := s.cfg.Backend.Create(ctx, req)
	if sess.ID != "" && req.IdempotencyKey != "" {
		_ = s.store.PutCreationReceipt(ctx, store.CreationReceipt{
			IdempotencyKey: req.IdempotencyKey,
			Kind:           "session",
			ResourceID:     sess.ID,
			RequestHash:    hashStruct(req),
		})
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return sess, nil
}

func (s *Server) spawnWithReceipt(ctx context.Context, req protocol.SpawnRequest) (any, *protocol.RPCError) {
	if req.IdempotencyKey != "" {
		reqHash := hashStruct(req)
		rec, ok, err := s.store.GetCreationReceipt(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, internalErr(err)
		}
		if ok {
			if rec.Kind != "spawn" || rec.RequestHash != reqHash {
				return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "idempotencyKey reused with different arguments"}
			}
			return protocol.Session{ID: rec.ResourceID, Title: req.Name, Directory: req.Directory, Model: req.Model}, nil
		}
	}
	sess, err := s.cfg.Backend.Spawn(ctx, req)
	if sess.ID != "" && req.IdempotencyKey != "" {
		_ = s.store.PutCreationReceipt(ctx, store.CreationReceipt{
			IdempotencyKey: req.IdempotencyKey,
			Kind:           "spawn",
			ResourceID:     sess.ID,
			RequestHash:    hashStruct(req),
		})
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
