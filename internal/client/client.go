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
)

// Options configures a connection to wadad.
type Options struct {
	Endpoint     string // host:port or ws://.../ws
	Credential   string
	Password     string
	ClientID     string
	ClientType   string // "desktop" | "web" | "cli"
	AppVersion   string
	Capabilities []string
	PingInterval time.Duration
	RPCTimeout   time.Duration
}

type subStream struct {
	ch     chan protocol.Event
	done   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// Client is a multiplexed WebSocket RPC and event-stream client for wadad.
type Client struct {
	ws         *websocket.Conn
	serverInfo protocol.ServerInfoPayload
	rpcTimeout time.Duration

	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan protocol.Envelope
	subs      map[string]*subStream
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

	ws, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return nil, err
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
		_ = ws.Close()
		return nil, err
	}
	reqID := uuid.NewString()
	if err := ws.WriteJSON(protocol.Envelope{
		Type:      protocol.TypeHello,
		RequestID: reqID,
		Payload:   helloPayload,
	}); err != nil {
		_ = ws.Close()
		return nil, err
	}

	_, raw, err := ws.ReadMessage()
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		_ = ws.Close()
		return nil, err
	}
	if env.Error != nil {
		_ = ws.Close()
		return nil, env.Error
	}
	if env.Type != protocol.TypeServerInfo {
		_ = ws.Close()
		return nil, fmt.Errorf("client: unexpected handshake frame %q", env.Type)
	}
	var info protocol.ServerInfoPayload
	if err := json.Unmarshal(env.Payload, &info); err != nil {
		_ = ws.Close()
		return nil, err
	}

	c := &Client{
		ws:         ws,
		serverInfo: info,
		rpcTimeout: opts.RPCTimeout,
		pending:    map[string]chan protocol.Envelope{},
		subs:       map[string]*subStream{},
		done:       make(chan struct{}),
	}
	go c.readLoop()
	go c.pingLoop(opts.PingInterval)
	return c, nil
}

func normalizeWSURL(endpoint string) string {
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
		c.mu.Unlock()
		close(c.done)
		err = c.ws.Close()
		for _, sub := range subs {
			sub.wg.Wait()
			close(sub.ch)
		}
	})
	return err
}

func (c *Client) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.ws.WriteJSON(v)
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

func (c *Client) Subscribe(ctx context.Context, sessions ...string) (string, <-chan protocol.Event, error) {
	ch := make(chan protocol.Event, 128)
	var resp protocol.SubscribeResponse
	if err := c.Call(ctx, protocol.MethodEventsSubscribe, protocol.SubscribeRequest{Sessions: sessions}, &resp); err != nil {
		return "", nil, err
	}
	sub := &subStream{ch: ch, done: make(chan struct{})}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", nil, errors.New("client: connection closed")
	}
	c.subs[resp.SubscriptionID] = sub
	c.mu.Unlock()
	return resp.SubscriptionID, ch, nil
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

func (c *Client) Skills(ctx context.Context, dir string) ([]protocol.Skill, error) {
	var out []protocol.Skill
	return out, c.Call(ctx, protocol.MethodSkillsList, protocol.SkillsListRequest{Directory: dir}, &out)
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
