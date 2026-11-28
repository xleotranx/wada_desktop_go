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
	"sync/atomic"
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

// A subscription may queue this many events, and an attached terminal this
// much output, before the client ends it rather than stall the connection.
const (
	maxQueuedEvents      = 4096
	maxQueuedOutputBytes = 8 << 20
)

type subStream struct {
	feed   *feed[protocol.Event]
	closed bool // guarded by Client.mu
}

func newEventFeed() *feed[protocol.Event] {
	return newFeed(maxQueuedEvents, func(protocol.Event) int { return 1 })
}

func newOutputFeed() *feed[[]byte] {
	return newFeed(maxQueuedOutputBytes, func(b []byte) int { return len(b) })
}

// Client is a multiplexed WebSocket RPC and event-stream client for wadad.
type Client struct {
	tr         relay.Transport
	serverInfo protocol.ServerInfoPayload
	rpcTimeout time.Duration
	// lastRecv is when the last frame arrived, in Unix nanoseconds.
	lastRecv atomic.Int64

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan protocol.Envelope
	// onResponse runs on the read loop for a successful response before it
	// is delivered, so state it registers is in place for the next frame.
	onResponse map[string]func(protocol.Envelope)
	subs       map[string]*subStream
	termSubs   map[uint8]*termStream
	closed     bool
	closeOnce  sync.Once
	done       chan struct{}
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
	if IsSSHEndpoint(opts.Endpoint) {
		return DialSSH(ctx, opts.Endpoint, "", opts)
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
		opts.Endpoint = off.DirectURL
		cli, derr := Dial(ctx, opts)
		if derr != nil {
			return nil, errors.Join(rerr, derr)
		}
		return cli, nil
	}
	if off.DirectURL == "" {
		return nil, errors.New("client: offer has no reachable endpoint")
	}
	opts.Endpoint = off.DirectURL
	return Dial(ctx, opts)
}

// IsUnauthorized reports that the daemon refused the client's credential or
// password, or the relay its relay token: dialing again as before is futile.
func IsUnauthorized(err error) bool {
	if errors.Is(err, relay.ErrUnauthorized) {
		return true
	}
	var rpcErr *protocol.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrCodeUnauthorized
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
		onResponse: map[string]func(protocol.Envelope){},
		subs:       map[string]*subStream{},
		termSubs:   map[uint8]*termStream{},
		done:       make(chan struct{}),
	}
	c.lastRecv.Store(time.Now().UnixNano())
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
	if !strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
		endpoint = "ws://" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/ws") {
		return endpoint
	}
	return endpoint + "/ws"
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
		clear(c.onResponse)
		subs := make([]*subStream, 0, len(c.subs))
		for id, sub := range c.subs {
			sub.closed = true
			subs = append(subs, sub)
			delete(c.subs, id)
		}
		termSubs := make([]*termStream, 0, len(c.termSubs))
		for chID, ts := range c.termSubs {
			ts.closed = true
			termSubs = append(termSubs, ts)
			delete(c.termSubs, chID)
		}
		c.mu.Unlock()
		close(c.done)
		err = c.tr.Close()
		for _, sub := range subs {
			sub.feed.halt()
		}
		for _, ts := range termSubs {
			ts.feed.halt()
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

// pingLoop pings the server every interval and closes the connection when
// nothing, not even a pong, has arrived for three intervals: a connection that
// went half-open (sleep, a network change) otherwise looks alive for minutes.
func (c *Client) pingLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case t := <-ticker.C:
			if t.Sub(time.Unix(0, c.lastRecv.Load())) > 3*interval {
				_ = c.Close()
				return
			}
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
		c.lastRecv.Store(time.Now().UnixNano())
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
			hook := c.onResponse[env.RequestID]
			delete(c.pending, env.RequestID)
			delete(c.onResponse, env.RequestID)
			c.mu.Unlock()
			if ch != nil {
				if hook != nil && env.Error == nil {
					hook(env)
				}
				ch <- env
			}
		case protocol.TypeEvent:
			var ev protocol.Event
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				continue
			}
			c.mu.Lock()
			sub := c.subs[env.SubscriptionID]
			if sub != nil && sub.closed {
				sub = nil
			}
			c.mu.Unlock()
			if sub != nil && !sub.feed.push(ev) {
				c.dropLagging(env.SubscriptionID, sub)
			}
		}
	}
}

// dropLagging ends a subscription whose consumer fell maxQueuedEvents behind:
// its channel is already closed, and the server is told to stop sending.
func (c *Client) dropLagging(subID string, sub *subStream) {
	c.mu.Lock()
	sub.closed = true
	if c.subs[subID] == sub {
		delete(c.subs, subID)
	}
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Call(ctx, protocol.MethodEventsUnsubscribe, protocol.UnsubscribeRequest{SubscriptionID: subID}, nil)
	}()
}

// Call invokes a typed RPC method on wadad.
func (c *Client) Call(ctx context.Context, method string, req, resp any) error {
	return c.call(ctx, method, req, resp, nil)
}

// call is Call with onOK, which runs on the read loop when a successful
// response arrives and before Call returns. When Call fails for its context
// after the response arrived, onOK has run all the same.
func (c *Client) call(ctx context.Context, method string, req, resp any, onOK func(protocol.Envelope)) error {
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
	if onOK != nil {
		c.onResponse[reqID] = onOK
	}
	c.mu.Unlock()

	if err := c.writeJSON(protocol.Envelope{
		Type:      protocol.TypeRPCRequest,
		RequestID: reqID,
		Method:    method,
		Payload:   payload,
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		delete(c.onResponse, reqID)
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
		_, waiting := c.pending[reqID]
		delete(c.pending, reqID)
		delete(c.onResponse, reqID)
		c.mu.Unlock()
		if !waiting {
			// The read loop took the response and is about to deliver it;
			// wait so that onOK has finished before returning.
			<-ch
		}
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
	sub := &subStream{feed: newEventFeed()}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		sub.feed.halt()
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
	return subID, sub.feed.out, nil
}

// dropSub ends a locally registered subscription that never became active.
func (c *Client) dropSub(subID string, sub *subStream) {
	c.mu.Lock()
	sub.closed = true
	if c.subs[subID] == sub {
		delete(c.subs, subID)
	}
	c.mu.Unlock()
	sub.feed.halt()
}

func (c *Client) Unsubscribe(ctx context.Context, subID string) error {
	c.mu.Lock()
	sub := c.subs[subID]
	if sub != nil {
		sub.closed = true
	}
	delete(c.subs, subID)
	c.mu.Unlock()
	if sub != nil {
		sub.feed.halt()
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

// Profiles is the agent profiles for an agent starting in dir, or the user's
// only when dir is "".
func (c *Client) Profiles(ctx context.Context, dir string) ([]protocol.AgentProfile, error) {
	var out []protocol.AgentProfile
	return out, c.Call(ctx, protocol.MethodProfilesList, protocol.ProfilesListRequest{Directory: dir}, &out)
}

func (c *Client) Workers(ctx context.Context, parentSession string) ([]protocol.WorkerInfo, error) {
	var out []protocol.WorkerInfo
	return out, c.Call(ctx, protocol.MethodWorkersList, protocol.WorkersListRequest{ParentSession: parentSession}, &out)
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
	feed    *feed[[]byte]
	// closed (guarded by Client.mu) means no detach is owed: the stream was
	// closed, or the server released the channel when the terminal exited.
	closed bool
}

// TerminalStream is an attached live binary PTY stream over WebSocket.
type TerminalStream struct {
	ts *termStream
}

func (s *TerminalStream) Info() protocol.TerminalInfo { return s.ts.info }
func (s *TerminalStream) Snapshot() []byte            { return s.ts.snap }

// Output delivers the terminal's output. It is closed after the last output
// when the terminal exits, when the stream or connection closes, or when the
// consumer falls maxQueuedOutputBytes behind.
func (s *TerminalStream) Output() <-chan []byte { return s.ts.feed.out }

func (s *TerminalStream) WriteInput(data []byte) error {
	return s.ts.client.writeBinary(protocol.EncodeBinaryFrame(protocol.OpTermInput, s.ts.channel, data))
}

func (s *TerminalStream) Resize(cols, rows int) error {
	return s.ts.client.writeBinary(protocol.EncodeBinaryFrame(protocol.OpTermResize, s.ts.channel, protocol.EncodeTermResize(cols, rows)))
}

func (s *TerminalStream) Close() error {
	c := s.ts.client
	c.mu.Lock()
	// Already closed by an earlier Close, by Client.Close (which ended the
	// server-side attachment with the connection), or by the terminal's exit.
	released := s.ts.closed
	s.ts.closed = true
	if c.termSubs[s.ts.channel] == s.ts {
		delete(c.termSubs, s.ts.channel)
	}
	c.mu.Unlock()
	s.ts.feed.halt()
	if released {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.Call(ctx, protocol.MethodTerminalDetach, protocol.TerminalDetachRequest{ID: s.ts.termID}, nil)
}

func (c *Client) handleBinary(raw []byte) {
	op, chID, payload, err := protocol.DecodeBinaryFrame(raw)
	if err != nil {
		return
	}
	switch op {
	case protocol.OpTermOutput:
	case protocol.OpTermExit:
		// The server released the channel after the terminal's last output.
		c.mu.Lock()
		ts := c.termSubs[chID]
		if ts != nil {
			ts.closed = true
			delete(c.termSubs, chID)
		}
		c.mu.Unlock()
		if ts != nil {
			ts.feed.finish()
		}
		return
	default:
		return
	}
	c.mu.Lock()
	ts := c.termSubs[chID]
	c.mu.Unlock()
	if ts == nil {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	if !ts.feed.push(cp) {
		// The consumer fell too far behind and its output channel is closed;
		// further frames are dropped until Close detaches it.
		c.mu.Lock()
		if c.termSubs[chID] == ts {
			delete(c.termSubs, chID)
		}
		c.mu.Unlock()
	}
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
	// The channel is registered on the read loop as the response arrives,
	// before the read loop reads the output the server sends right after it.
	made := make(chan *termStream, 1)
	register := func(env protocol.Envelope) {
		var resp protocol.TerminalAttachResponse
		if json.Unmarshal(env.Payload, &resp) != nil {
			return
		}
		ts := &termStream{
			client:  c,
			termID:  req.ID,
			channel: resp.Channel,
			info:    resp.Info,
			snap:    resp.Snapshot,
			feed:    newOutputFeed(),
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			ts.feed.halt()
			return
		}
		c.termSubs[resp.Channel] = ts
		c.mu.Unlock()
		made <- ts
	}
	var resp protocol.TerminalAttachResponse
	err := c.call(ctx, protocol.MethodTerminalAttach, req, &resp, register)
	var ts *termStream
	select {
	case ts = <-made:
	default:
	}
	if err != nil {
		if ts != nil {
			// Attached after all, but too late for the caller: detach.
			_ = (&TerminalStream{ts: ts}).Close()
		}
		return nil, err
	}
	if ts == nil {
		return nil, errors.New("client: connection closed")
	}
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

func (c *Client) Projects(ctx context.Context, archived bool) ([]protocol.ProjectInfo, error) {
	var out []protocol.ProjectInfo
	return out, c.Call(ctx, protocol.MethodProjectsList, protocol.ProjectsListRequest{Archived: archived}, &out)
}

func (c *Client) AddProject(ctx context.Context, root string) (protocol.ProjectAddResponse, error) {
	var out protocol.ProjectAddResponse
	return out, c.Call(ctx, protocol.MethodProjectAdd, protocol.ProjectAddRequest{Root: root}, &out)
}

func (c *Client) RenameProject(ctx context.Context, id, name string) error {
	return c.Call(ctx, protocol.MethodProjectRename, protocol.ProjectRenameRequest{Project: id, Name: name}, nil)
}

func (c *Client) ArchiveProject(ctx context.Context, id string) error {
	return c.Call(ctx, protocol.MethodProjectArchive, protocol.ProjectArchiveRequest{Project: id}, nil)
}

func (c *Client) Workspaces(ctx context.Context, req protocol.WorkspacesListRequest) ([]protocol.WorkspaceInfo, error) {
	var out []protocol.WorkspaceInfo
	return out, c.Call(ctx, protocol.MethodWorkspacesList, req, &out)
}

func (c *Client) RenameWorkspace(ctx context.Context, id, title string) error {
	return c.Call(ctx, protocol.MethodWorkspaceRename, protocol.WorkspaceRenameRequest{Workspace: id, Title: title}, nil)
}

func (c *Client) CreateWorkspace(ctx context.Context, req protocol.WorkspaceCreateRequest) (protocol.WorkspaceInfo, error) {
	var out protocol.WorkspaceInfo
	return out, c.Call(ctx, protocol.MethodWorkspaceCreate, req, &out)
}

func (c *Client) ArchiveWorkspace(ctx context.Context, id string, force bool) error {
	return c.Call(ctx, protocol.MethodWorkspaceArchive, protocol.WorkspaceArchiveRequest{Workspace: id, Force: force}, nil)
}

func (c *Client) RestoreWorkspace(ctx context.Context, id string) (protocol.WorkspaceInfo, error) {
	var out protocol.WorkspaceInfo
	return out, c.Call(ctx, protocol.MethodWorkspaceRestore, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

func (c *Client) WorkspaceRefs(ctx context.Context, project string) (protocol.WorkspaceRefs, error) {
	var out protocol.WorkspaceRefs
	return out, c.Call(ctx, protocol.MethodWorkspaceRefs, protocol.WorkspaceRefsRequest{Project: project}, &out)
}

func (c *Client) WorkspaceChanges(ctx context.Context, id string) ([]protocol.FileChange, error) {
	var out []protocol.FileChange
	return out, c.Call(ctx, protocol.MethodWorkspaceChanges, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

// WorkspaceConfig reads a workspace's wada.json, its setup and its services.
func (c *Client) WorkspaceConfig(ctx context.Context, id string) (protocol.WorkspaceConfig, error) {
	var out protocol.WorkspaceConfig
	return out, c.Call(ctx, protocol.MethodWorkspaceConfig, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

// TrustWorkspaceConfig allows the wada.json with this hash to run in the
// workspace's project.
func (c *Client) TrustWorkspaceConfig(ctx context.Context, id, hash string) error {
	return c.Call(ctx, protocol.MethodWorkspaceTrust, protocol.WorkspaceTrustRequest{Workspace: id, Hash: hash}, nil)
}

// RunSetup starts the workspace's setup, or returns the one running.
func (c *Client) RunSetup(ctx context.Context, id string) (protocol.TerminalInfo, error) {
	var out protocol.TerminalInfo
	return out, c.Call(ctx, protocol.MethodWorkspaceSetup, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

// StartService starts a service of the workspace, or returns it running.
func (c *Client) StartService(ctx context.Context, id, name string) (protocol.TerminalInfo, error) {
	var out protocol.TerminalInfo
	return out, c.Call(ctx, protocol.MethodServiceStart, protocol.WorkspaceServiceRequest{Workspace: id, Name: name}, &out)
}

// StopService ends a running service of the workspace.
func (c *Client) StopService(ctx context.Context, id, name string) error {
	return c.Call(ctx, protocol.MethodServiceStop, protocol.WorkspaceServiceRequest{Workspace: id, Name: name}, nil)
}

// DraftCommit writes a commit message for everything changed in a workspace.
func (c *Client) DraftCommit(ctx context.Context, id string) (string, error) {
	var out protocol.CommitDraft
	return out.Message, c.Call(ctx, protocol.MethodCommitDraft, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

// Commit commits everything changed in a workspace.
func (c *Client) Commit(ctx context.Context, id, message string) (string, error) {
	var out protocol.CommitResult
	return out.SHA, c.Call(ctx, protocol.MethodCommit, protocol.WorkspaceCommitRequest{Workspace: id, Message: message}, &out)
}

// DraftPR writes a pull request's title and body for a workspace.
func (c *Client) DraftPR(ctx context.Context, id string) (protocol.PRDraft, error) {
	var out protocol.PRDraft
	return out, c.Call(ctx, protocol.MethodPRDraft, protocol.WorkspaceIDRequest{Workspace: id}, &out)
}

// CreateWorkspacePR pushes a workspace's branch and opens a pull request.
func (c *Client) CreateWorkspacePR(ctx context.Context, req protocol.WorkspacePRRequest) (*protocol.PRStatus, error) {
	var out *protocol.PRStatus
	return out, c.Call(ctx, protocol.MethodPRCreate, req, &out)
}
