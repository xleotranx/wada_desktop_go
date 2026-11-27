// Package client implements Wada's WebSocket client SDK for connecting to
// local or remote wadad daemons.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"wada-desktop-go/internal/protocol"
	"wada-desktop-go/internal/relay"
)

// Options configures a connection to wadad.
type Options struct {
	Endpoint       string // host:port or ws://.../ws
	Credential     string
	Password       string
	RelayAuthToken string // optional Bearer token for wada-relay itself
	ClientID       string
	ClientType     string // "desktop" | "web" | "cli"
	AppVersion     string
	Capabilities   []string
	PingInterval   time.Duration
	RPCTimeout     time.Duration
}

type subStream struct {
	ch     chan protocol.Event
	done   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// Client is a multiplexed WebSocket RPC and event-stream client for wadad.
type Client struct {
	tr         relay.Transport
	serverInfo protocol.ServerInfoPayload
	rpcTimeout time.Duration

	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan protocol.Envelope
	subs      map[string]*subStream
	termSubs  map[uint8]*termStream
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

// DialLocal connects to the local wadad instance described by dataDir.
func DialLocal(ctx context.Context, dataDir string, opts Options) (*Client, error) {
	if dataDir == "" {
		var err error
		dataDir, err = DefaultDataDir()
		if err != nil {
			return nil, err
		}
	}
	rec, cred, err := ReadLocalEndpoint(dataDir)
	if err != nil {
		return nil, err
	}
	opts.Endpoint = rec.Listen
	opts.Credential = cred
	return Dial(ctx, opts)
}

// Dial connects to wadad over WebSocket and completes the hello/server_info handshake.
func Dial(ctx context.Context, opts Options) (*Client, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("client: endpoint is required")
	}
	wsURL := normalizeWSURL(opts.Endpoint)
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return nil, err
	}
	return dialTransport(ctx, ws, opts)
}

// DialRelay connects to a wadad instance over a zero-knowledge wada-relay Noise NK tunnel.
func DialRelay(ctx context.Context, relayURL string, serverPub [32]byte, opts Options) (*Client, error) {
	tr, err := relay.DialClientTunnel(ctx, relayURL, serverPub, opts.RelayAuthToken)
	if err != nil {
		return nil, err
	}
	return dialTransport(ctx, tr, opts)
}

// DialOffer decodes a pairing offer or #offer= URL and dials over E2EE relay (preferred) or direct WS.
func DialOffer(ctx context.Context, rawOffer string, opts Options) (*Client, error) {
	off, err := relay.DecodeOffer(rawOffer)
	if err != nil {
		return nil, err
	}
	if opts.Credential == "" && opts.Password == "" && off.Token != "" {
		opts.Credential = off.Token
	}
	if off.RelayURL != "" {
		pub, perr := relay.ParseKey(off.ServerPubKey)
		if perr != nil {
			return nil, perr
		}
		cli, rerr := DialRelay(ctx, off.RelayURL, pub, opts)
		if rerr == nil || off.DirectURL == "" {
			return cli, rerr
		}
	}
	if off.DirectURL == "" {
		return nil, errors.New("client: offer has no reachable endpoint")
	}
	opts.Endpoint = off.DirectURL
	return Dial(ctx, opts)
}

func dialTransport(ctx context.Context, tr relay.Transport, opts Options) (*Client, error) {
	if opts.ClientID == "" {
		opts.ClientID = "cli_" + uuid.NewString()
	}
	if opts.ClientType == "" {
		opts.ClientType = "cli"
	}
	if opts.Capabilities == nil {
		opts.Capabilities = []string{
			protocol.CapOwnedSubscriptions,
			protocol.CapCreationReceipts,
			protocol.CapBinaryTransfer,
		}
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = 10 * time.Second
	}
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = 60 * time.Second
	}

	helloPayload, err := json.Marshal(protocol.HelloPayload{
		ClientID:        opts.ClientID,
		ClientType:      opts.ClientType,
		ProtocolVersion: protocol.ProtocolVersion,
		AppVersion:      opts.AppVersion,
		Credential:      opts.Credential,
		Password:        opts.Password,
		Capabilities:    opts.Capabilities,
	})
	if err != nil {
		_ = tr.Close()
		return nil, err
	}
	reqID := uuid.NewString()
	helloEnv, err := json.Marshal(protocol.Envelope{
		Type:      protocol.TypeHello,
		RequestID: reqID,
		Payload:   helloPayload,
	})
	if err != nil {
		_ = tr.Close()
		return nil, err
	}
	_ = tr.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := tr.WriteMessage(websocket.TextMessage, helloEnv); err != nil {
		_ = tr.Close()
		return nil, err
	}
	_ = tr.SetWriteDeadline(time.Time{})

	_ = tr.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := tr.ReadMessage()
	if err != nil {
		_ = tr.Close()
		return nil, err
	}
	_ = tr.SetReadDeadline(time.Time{})

	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		_ = tr.Close()
		return nil, err
	}
	if env.Error != nil {
		_ = tr.Close()
		return nil, env.Error
	}
	if env.Type != protocol.TypeServerInfo {
		_ = tr.Close()
		return nil, fmt.Errorf("client: unexpected handshake frame %q", env.Type)
	}
	var info protocol.ServerInfoPayload
	if err := json.Unmarshal(env.Payload, &info); err != nil {
		_ = tr.Close()
		return nil, err
	}

	c := &Client{
		tr:         tr,
		serverInfo: info,
		rpcTimeout: opts.RPCTimeout,
		pending:    map[string]chan protocol.Envelope{},
		subs:       map[string]*subStream{},
		termSubs:   map[uint8]*termStream{},
		done:       make(chan struct{}),
	}
	go c.readLoop()
	go c.pingLoop(opts.PingInterval)
	return c, nil
}

func normalizeWSURL(endpoint string) string {
	switch {
	case strings.HasPrefix(endpoint, "http://"):
		endpoint = "ws://" + strings.TrimPrefix(endpoint, "http://")
	case strings.HasPrefix(endpoint, "https://"):
		endpoint = "wss://" + strings.TrimPrefix(endpoint, "https://")
	}
	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		if strings.HasSuffix(endpoint, "/ws") {
			return endpoint
		}
		return strings.TrimRight(endpoint, "/") + "/ws"
	}
	return "ws://" + strings.TrimRight(endpoint, "/") + "/ws"
}

func (c *Client) ServerInfo() protocol.ServerInfoPayload { return c.serverInfo }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		subs := make([]*subStream, 0, len(c.subs))
		for id, sub := range c.subs {
			sub.closed = true
			close(sub.done)
			subs = append(subs, sub)
			delete(c.subs, id)
		}
		termSubs := make([]*termStream, 0, len(c.termSubs))
		for chID, ts := range c.termSubs {
			ts.closed = true
			close(ts.done)
			termSubs = append(termSubs, ts)
			delete(c.termSubs, chID)
		}
		c.mu.Unlock()
		close(c.done)
		err = c.tr.Close()
		for _, sub := range subs {
			sub.wg.Wait()
			close(sub.ch)
		}
		for _, ts := range termSubs {
			ts.wg.Wait()
			close(ts.ch)
		}
	})
	return err
}

func (c *Client) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.tr.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.tr.WriteMessage(websocket.TextMessage, raw)
}

func (c *Client) writeBinary(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.tr.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.tr.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *Client) pingLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case t := <-ticker.C:
			raw, _ := json.Marshal(protocol.PingPongPayload{Timestamp: t.UnixMilli()})
			if err := c.writeJSON(protocol.Envelope{
				Type:      protocol.TypePing,
				RequestID: uuid.NewString(),
				Payload:   raw,
			}); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *Client) readLoop() {
	defer func() { _ = c.Close() }()
	for {
		msgType, raw, err := c.tr.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.BinaryMessage {
			c.handleBinary(raw)
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
		case protocol.TypeRPCResponse:
			c.mu.Lock()
			ch := c.pending[env.RequestID]
			delete(c.pending, env.RequestID)
			c.mu.Unlock()
			if ch != nil {
				ch <- env
			}
		case protocol.TypeEvent:
			var ev protocol.Event
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				continue
			}
			c.mu.Lock()
			sub := c.subs[env.SubscriptionID]
			if sub != nil && !sub.closed {
				sub.wg.Add(1)
			} else {
				sub = nil
			}
			c.mu.Unlock()
			if sub != nil {
				select {
				case sub.ch <- ev:
				case <-sub.done:
				case <-c.done:
				}
				sub.wg.Done()
			}
		}
	}
}

// Call invokes a typed RPC method on wadad.
func (c *Client) Call(ctx context.Context, method string, req, resp any) error {
	var payload json.RawMessage
	if req != nil {
		raw, err := json.Marshal(req)
		if err != nil {
			return err
		}
		payload = raw
	}
	reqID := uuid.NewString()
	ch := make(chan protocol.Envelope, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("client: connection closed")
	}
	c.pending[reqID] = ch
	c.mu.Unlock()

	if err := c.writeJSON(protocol.Envelope{
		Type:      protocol.TypeRPCRequest,
		RequestID: reqID,
		Method:    method,
		Payload:   payload,
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return err
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.rpcTimeout)
		defer cancel()
	}

	select {
	case env, ok := <-ch:
		if !ok {
			return errors.New("client: connection closed")
		}
		if env.Error != nil {
			return env.Error
		}
		if resp != nil && len(env.Payload) > 0 {
			return json.Unmarshal(env.Payload, resp)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return errors.New("client: connection closed")
	}
}

// Subscribe registers an event stream. The subscription is registered locally
// under a client-chosen ID before the request is sent, so events the server
// sends ahead of its response are not dropped.
func (c *Client) Subscribe(ctx context.Context, sessions ...string) (string, <-chan protocol.Event, error) {
	subID := "sub_" + uuid.NewString()
	ch := make(chan protocol.Event, 128)
	sub := &subStream{ch: ch, done: make(chan struct{})}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", nil, errors.New("client: connection closed")
	}
	c.subs[subID] = sub
	c.mu.Unlock()

	var resp protocol.SubscribeResponse
	err := c.Call(ctx, protocol.MethodEventsSubscribe, protocol.SubscribeRequest{SubscriptionID: subID, Sessions: sessions}, &resp)
	if err == nil && resp.SubscriptionID != subID {
		err = fmt.Errorf("client: server returned subscription %q, want %q", resp.SubscriptionID, subID)
	}
	if err != nil {
		c.dropSub(subID, sub)
		return "", nil, err
	}
	return subID, ch, nil
}

// dropSub ends a locally registered subscription that never became active.
func (c *Client) dropSub(subID string, sub *subStream) {
	c.mu.Lock()
	owned := !sub.closed
	if owned {
		sub.closed = true
		close(sub.done)
		if c.subs[subID] == sub {
			delete(c.subs, subID)
		}
	}
	c.mu.Unlock()
	if owned {
		sub.wg.Wait()
		close(sub.ch)
	}
}

func (c *Client) Unsubscribe(ctx context.Context, subID string) error {
	c.mu.Lock()
	sub := c.subs[subID]
	if sub != nil && !sub.closed {
		sub.closed = true
		close(sub.done)
	}
	delete(c.subs, subID)
	c.mu.Unlock()
	if sub != nil {
		sub.wg.Wait()
		close(sub.ch)
	}
	return c.Call(ctx, protocol.MethodEventsUnsubscribe, protocol.UnsubscribeRequest{SubscriptionID: subID}, nil)
}

func (c *Client) Status(ctx context.Context) (protocol.DaemonStatusResponse, error) {
	var out protocol.DaemonStatusResponse
	return out, c.Call(ctx, protocol.MethodDaemonStatus, nil, &out)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.Call(ctx, protocol.MethodDaemonShutdown, nil, nil)
}

func (c *Client) Sessions(ctx context.Context, q protocol.SessionQuery) (protocol.SessionPage, error) {
	var out protocol.SessionPage
	return out, c.Call(ctx, protocol.MethodSessionsList, q, &out)
}

func (c *Client) Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	var out protocol.Session
	return out, c.Call(ctx, protocol.MethodSessionCreate, req, &out)
}

func (c *Client) Spawn(ctx context.Context, req protocol.SpawnRequest) (protocol.Session, error) {
	var out protocol.Session
	return out, c.Call(ctx, protocol.MethodSessionSpawn, req, &out)
}

func (c *Client) Messages(ctx context.Context, id string) ([]protocol.Message, error) {
	var out []protocol.Message
	return out, c.Call(ctx, protocol.MethodSessionMessages, protocol.SessionIDRequest{Session: id}, &out)
}

func (c *Client) Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	var out protocol.SendResponse
	err := c.Call(ctx, protocol.MethodSessionSend, req, &out)
	return out.Disposition, err
}

func (c *Client) Abort(ctx context.Context, id string) error {
	return c.Call(ctx, protocol.MethodSessionAbort, protocol.SessionIDRequest{Session: id}, nil)
}

func (c *Client) Reply(ctx context.Context, req protocol.ReplyRequest) error {
	return c.Call(ctx, protocol.MethodSessionReply, req, nil)
}

func (c *Client) Rename(ctx context.Context, id, title string) error {
	return c.Call(ctx, protocol.MethodSessionRename, protocol.RenameRequest{Session: id, Title: title}, nil)
}

func (c *Client) Delete(ctx context.Context, id string) error {
	return c.Call(ctx, protocol.MethodSessionDelete, protocol.SessionIDRequest{Session: id}, nil)
}

func (c *Client) Rewind(ctx context.Context, id, msg string) (string, error) {
	var out protocol.RewindResponse
	err := c.Call(ctx, protocol.MethodSessionRewind, protocol.RewindRequest{Session: id, Message: msg}, &out)
	return out.Session, err
}

func (c *Client) Changes(ctx context.Context, id string) ([]protocol.FileChange, error) {
	var out []protocol.FileChange
	return out, c.Call(ctx, protocol.MethodSessionChanges, protocol.SessionIDRequest{Session: id}, &out)
}

func (c *Client) Snapshot(ctx context.Context, id string) (map[string][]byte, error) {
	var out protocol.SnapshotResponse
	err := c.Call(ctx, protocol.MethodSessionSnapshot, protocol.SessionIDRequest{Session: id}, &out)
	return out.Files, err
}

func (c *Client) Diff(ctx context.Context, id string, before map[string][]byte) ([]protocol.FileChange, error) {
	var out []protocol.FileChange
	return out, c.Call(ctx, protocol.MethodSessionDiff, protocol.DiffRequest{Session: id, Before: before}, &out)
}

func (c *Client) Models(ctx context.Context) ([]protocol.Model, error) {
	var out []protocol.Model
	return out, c.Call(ctx, protocol.MethodModelsList, nil, &out)
}

func (c *Client) DefaultModel(ctx context.Context) (protocol.ModelRef, error) {
	var out protocol.ModelRef
	return out, c.Call(ctx, protocol.MethodModelsDefault, nil, &out)
}

func (c *Client) SetModel(ctx context.Context, id string, ref protocol.ModelRef) error {
	return c.Call(ctx, protocol.MethodSessionSetModel, protocol.SetModelRequest{Session: id, Model: ref}, nil)
}

func (c *Client) Modes(ctx context.Context) ([]protocol.Mode, error) {
	var out []protocol.Mode
	return out, c.Call(ctx, protocol.MethodModesList, nil, &out)
}

func (c *Client) FindFiles(ctx context.Context, dir, query string, limit int) ([]protocol.FileEntry, error) {
	var out []protocol.FileEntry
	return out, c.Call(ctx, protocol.MethodFilesFind, protocol.FilesFindRequest{Directory: dir, Query: query, Limit: limit}, &out)
}

func (c *Client) ReadFile(ctx context.Context, dir, path string) ([]byte, error) {
	var out protocol.FilesReadResponse
	err := c.Call(ctx, protocol.MethodFilesRead, protocol.FilesReadRequest{Directory: dir, Path: path}, &out)
	return out.Data, err
}

func (c *Client) FitImage(ctx context.Context, ref protocol.ModelRef, file protocol.File) (int, int, error) {
	var out protocol.FitImageResponse
	err := c.Call(ctx, protocol.MethodImagesFit, protocol.FitImageRequest{Model: ref, File: file}, &out)
	return out.Width, out.Height, err
}

func (c *Client) Skills(ctx context.Context, dir string) ([]protocol.Skill, error) {
	var out []protocol.Skill
	return out, c.Call(ctx, protocol.MethodSkillsList, protocol.SkillsListRequest{Directory: dir}, &out)
}

func (c *Client) Providers(ctx context.Context) ([]protocol.ProviderInfo, error) {
	var out []protocol.ProviderInfo
	return out, c.Call(ctx, protocol.MethodProvidersList, nil, &out)
}

func (c *Client) StartProviderLogin(ctx context.Context, req protocol.ProviderLoginStartRequest) (string, error) {
	var out protocol.ProviderLoginStartResponse
	err := c.Call(ctx, protocol.MethodProviderLoginStart, req, &out)
	return out.LoginID, err
}

func (c *Client) AnswerProviderLogin(ctx context.Context, loginID, value string) error {
	return c.Call(ctx, protocol.MethodProviderLoginAnswer, protocol.ProviderLoginAnswerRequest{LoginID: loginID, Value: value}, nil)
}

func (c *Client) CancelProviderLogin(ctx context.Context, loginID string) error {
	return c.Call(ctx, protocol.MethodProviderLoginCancel, protocol.ProviderLoginCancelRequest{LoginID: loginID}, nil)
}

func (c *Client) LogoutProvider(ctx context.Context, providerID string) error {
	return c.Call(ctx, protocol.MethodProviderLogout, protocol.ProviderLogoutRequest{ProviderID: providerID}, nil)
}

func (c *Client) PendingInteractions(ctx context.Context, id string) ([]protocol.Interaction, error) {
	var out []protocol.Interaction
	return out, c.Call(ctx, protocol.MethodInteractionsPending, protocol.SessionIDRequest{Session: id}, &out)
}

func (c *Client) AnswerInteraction(ctx context.Context, ans protocol.InteractionAnswer) error {
	return c.Call(ctx, protocol.MethodInteractionsAnswer, ans, nil)
}

func (c *Client) FetchTimeline(ctx context.Context, req protocol.TimelineFetchRequest) (protocol.TimelineFetchResponse, error) {
	var out protocol.TimelineFetchResponse
	return out, c.Call(ctx, protocol.MethodTimelineFetch, req, &out)
}

func (c *Client) Harnesses(ctx context.Context) ([]protocol.HarnessInfo, error) {
	var out []protocol.HarnessInfo
	return out, c.Call(ctx, protocol.MethodHarnessesList, nil, &out)
}

func (c *Client) Workers(ctx context.Context, parentSession string) ([]protocol.WorkerInfo, error) {
	var out []protocol.WorkerInfo
	return out, c.Call(ctx, protocol.MethodWorkersList, protocol.WorkersListRequest{ParentSession: parentSession}, &out)
}

func (c *Client) ListWorktrees(ctx context.Context, dir string) ([]protocol.WorktreeInfo, error) {
	var out []protocol.WorktreeInfo
	return out, c.Call(ctx, protocol.MethodWorktreesList, protocol.WorktreesListRequest{Directory: dir}, &out)
}

func (c *Client) CreateWorktree(ctx context.Context, req protocol.WorktreeCreateRequest) (protocol.WorktreeInfo, error) {
	var out protocol.WorktreeInfo
	return out, c.Call(ctx, protocol.MethodWorktreeCreate, req, &out)
}

func (c *Client) RemoveWorktree(ctx context.Context, req protocol.WorktreeRemoveRequest) error {
	return c.Call(ctx, protocol.MethodWorktreeRemove, req, nil)
}

func (c *Client) WorktreePR(ctx context.Context, dir string) (*protocol.PRStatus, error) {
	var out *protocol.PRStatus
	return out, c.Call(ctx, protocol.MethodWorktreePR, protocol.WorktreePRRequest{Directory: dir}, &out)
}

func (c *Client) CreateWorktreePR(ctx context.Context, req protocol.WorktreeCreatePRRequest) (*protocol.PRStatus, error) {
	var out *protocol.PRStatus
	return out, c.Call(ctx, protocol.MethodWorktreeCreatePR, req, &out)
}

type termStream struct {
	client  *Client
	termID  string
	channel uint8
	info    protocol.TerminalInfo
	snap    []byte
	ch      chan []byte
	done    chan struct{}
	closed  bool
	wg      sync.WaitGroup
}

// TerminalStream is an attached live binary PTY stream over WebSocket.
type TerminalStream struct {
	ts *termStream
}

func (s *TerminalStream) Channel() uint8              { return s.ts.channel }
func (s *TerminalStream) Info() protocol.TerminalInfo { return s.ts.info }
func (s *TerminalStream) Snapshot() []byte            { return s.ts.snap }
func (s *TerminalStream) Output() <-chan []byte       { return s.ts.ch }

func (s *TerminalStream) WriteInput(data []byte) error {
	return s.ts.client.writeBinary(protocol.EncodeBinaryFrame(protocol.OpTermInput, s.ts.channel, data))
}

func (s *TerminalStream) Resize(cols, rows int) error {
	return s.ts.client.writeBinary(protocol.EncodeBinaryFrame(protocol.OpTermResize, s.ts.channel, protocol.EncodeTermResize(cols, rows)))
}

func (s *TerminalStream) Close() error {
	c := s.ts.client
	c.mu.Lock()
	if s.ts.closed {
		// Already closed, by an earlier Close or by Client.Close, which also
		// closed ts.ch and ended the server-side attachment with the connection.
		c.mu.Unlock()
		return nil
	}
	s.ts.closed = true
	close(s.ts.done)
	if c.termSubs[s.ts.channel] == s.ts {
		delete(c.termSubs, s.ts.channel)
	}
	c.mu.Unlock()
	s.ts.wg.Wait()
	close(s.ts.ch)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.Call(ctx, protocol.MethodTerminalDetach, protocol.TerminalDetachRequest{ID: s.ts.termID}, nil)
}

func (c *Client) handleBinary(raw []byte) {
	op, chID, payload, err := protocol.DecodeBinaryFrame(raw)
	if err != nil {
		return
	}
	if op != protocol.OpTermOutput && op != protocol.OpTermSnapshot {
		return
	}
	c.mu.Lock()
	ts := c.termSubs[chID]
	if ts != nil && !ts.closed {
		ts.wg.Add(1)
	} else {
		ts = nil
	}
	c.mu.Unlock()
	if ts == nil {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case ts.ch <- cp:
	case <-ts.done:
	case <-c.done:
	}
	ts.wg.Done()
}

func (c *Client) Terminals(ctx context.Context) ([]protocol.TerminalInfo, error) {
	var out []protocol.TerminalInfo
	return out, c.Call(ctx, protocol.MethodTerminalsList, nil, &out)
}

func (c *Client) CreateTerminal(ctx context.Context, req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	var out protocol.TerminalInfo
	return out, c.Call(ctx, protocol.MethodTerminalCreate, req, &out)
}

func (c *Client) AttachTerminal(ctx context.Context, req protocol.TerminalAttachRequest) (*TerminalStream, error) {
	var resp protocol.TerminalAttachResponse
	if err := c.Call(ctx, protocol.MethodTerminalAttach, req, &resp); err != nil {
		return nil, err
	}
	ts := &termStream{
		client:  c,
		termID:  req.ID,
		channel: resp.Channel,
		info:    resp.Info,
		snap:    resp.Snapshot,
		ch:      make(chan []byte, 128),
		done:    make(chan struct{}),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("client: connection closed")
	}
	c.termSubs[resp.Channel] = ts
	c.mu.Unlock()
	return &TerminalStream{ts: ts}, nil
}

func (c *Client) CaptureTerminal(ctx context.Context, id string, lines int) (protocol.TerminalCaptureResponse, error) {
	var out protocol.TerminalCaptureResponse
	return out, c.Call(ctx, protocol.MethodTerminalCapture, protocol.TerminalCaptureRequest{ID: id, Lines: lines}, &out)
}

func (c *Client) KillTerminal(ctx context.Context, id string) error {
	return c.Call(ctx, protocol.MethodTerminalKill, protocol.TerminalKillRequest{ID: id}, nil)
}

func (c *Client) PairingOffer(ctx context.Context, appURL string) (protocol.PairingOfferResponse, error) {
	var out protocol.PairingOfferResponse
	return out, c.Call(ctx, protocol.MethodDaemonPairingOffer, protocol.PairingOfferRequest{AppURL: appURL}, &out)
}

func (c *Client) SetPassword(ctx context.Context, password string) error {
	return c.Call(ctx, protocol.MethodDaemonSetPassword, protocol.SetPasswordRequest{Password: password}, nil)
}
