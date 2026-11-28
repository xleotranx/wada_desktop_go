package client

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"wada-desktop-go/internal/protocol"
)

// pipeMsg is one frame from the test's fake server to the client.
type pipeMsg struct {
	typ  int
	data []byte
}

// pipeTransport is an in-memory relay.Transport whose peer is the test.
type pipeTransport struct {
	toServer chan []byte
	toClient chan pipeMsg
	once     sync.Once
	closed   chan struct{}
}

func newPipeTransport() *pipeTransport {
	return &pipeTransport{
		toServer: make(chan []byte, 64),
		toClient: make(chan pipeMsg, 1024),
		closed:   make(chan struct{}),
	}
}

func (p *pipeTransport) ReadMessage() (int, []byte, error) {
	select {
	case m := <-p.toClient:
		return m.typ, m.data, nil
	case <-p.closed:
		return 0, nil, errors.New("closed")
	}
}

func (p *pipeTransport) WriteMessage(_ int, data []byte) error {
	select {
	case <-p.closed:
		return errors.New("closed")
	default:
	}
	select {
	case p.toServer <- append([]byte(nil), data...):
		return nil
	case <-p.closed:
		return errors.New("closed")
	}
}

func (p *pipeTransport) SetReadDeadline(time.Time) error  { return nil }
func (p *pipeTransport) SetWriteDeadline(time.Time) error { return nil }
func (p *pipeTransport) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

// fakeServer answers the hello on tr and returns a function that reads the
// next RPC request, skipping pings.
func fakeServer(t *testing.T, tr *pipeTransport) func() protocol.Envelope {
	t.Helper()
	next := func() protocol.Envelope {
		t.Helper()
		for {
			select {
			case raw := <-tr.toServer:
				var env protocol.Envelope
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatal(err)
				}
				if env.Type == protocol.TypePing {
					continue
				}
				return env
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for client frame")
			}
		}
	}
	go func() {
		hello := <-tr.toServer
		var env protocol.Envelope
		_ = json.Unmarshal(hello, &env)
		info, _ := json.Marshal(protocol.ServerInfoPayload{ServerID: "srv"})
		raw, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeServerInfo, RequestID: env.RequestID, Payload: info})
		tr.toClient <- pipeMsg{websocket.TextMessage, raw}
	}()
	return next
}

func reply(t *testing.T, tr *pipeTransport, req protocol.Envelope, payload any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeRPCResponse, RequestID: req.RequestID, Method: req.Method, Payload: body})
	tr.toClient <- pipeMsg{websocket.TextMessage, raw}
}

func TestSubscribeReceivesEventsSentBeforeResponse(t *testing.T) {
	tr := newPipeTransport()
	next := fakeServer(t, tr)
	c, err := dialTransport(t.Context(), tr, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	go func() {
		req := next()
		var sr protocol.SubscribeRequest
		_ = json.Unmarshal(req.Payload, &sr)
		ev, _ := json.Marshal(protocol.Event{Kind: protocol.EventTerminalUpdated, Session: "s1"})
		raw, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeEvent, SubscriptionID: sr.SubscriptionID, Payload: ev})
		tr.toClient <- pipeMsg{websocket.TextMessage, raw}
		reply(t, tr, req, protocol.SubscribeResponse{SubscriptionID: sr.SubscriptionID})
	}()

	subID, ch, err := c.Subscribe(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if subID == "" {
		t.Fatal("empty subscription id")
	}
	select {
	case ev := <-ch:
		if ev.Session != "s1" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event sent before the subscribe response was dropped")
	}
}

func TestTerminalStreamCloseAfterClientClose(t *testing.T) {
	tr := newPipeTransport()
	next := fakeServer(t, tr)
	c, err := dialTransport(t.Context(), tr, Options{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		req := next()
		reply(t, tr, req, protocol.TerminalAttachResponse{Channel: 1, Info: protocol.TerminalInfo{ID: "term_1"}})
	}()
	st, err := c.AttachTerminal(t.Context(), protocol.TerminalAttachRequest{ID: "term_1"})
	if err != nil {
		t.Fatal(err)
	}

	_ = c.Close()
	if _, ok := <-st.Output(); ok {
		t.Fatal("output channel still open after client close")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close after client close = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func TestAttachKeepsOutputAndEndsAtExit(t *testing.T) {
	tr := newPipeTransport()
	next := fakeServer(t, tr)
	c, err := dialTransport(t.Context(), tr, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		req := next()
		// Output right behind the response must not be lost, and the exit
		// frame ends the stream after it.
		reply(t, tr, req, protocol.TerminalAttachResponse{Channel: 3, Info: protocol.TerminalInfo{ID: "term_1"}})
		tr.toClient <- pipeMsg{websocket.BinaryMessage, protocol.EncodeBinaryFrame(protocol.OpTermOutput, 3, []byte("hello"))}
		tr.toClient <- pipeMsg{websocket.BinaryMessage, protocol.EncodeBinaryFrame(protocol.OpTermExit, 3, nil)}
	}()
	st, err := c.AttachTerminal(t.Context(), protocol.TerminalAttachRequest{ID: "term_1"})
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for chunk := range st.Output() {
		got = append(got, chunk...)
	}
	if string(got) != "hello" {
		t.Fatalf("output = %q, want hello", got)
	}
	// The server already released the channel, so Close sends no detach.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-tr.toServer:
		var env protocol.Envelope
		_ = json.Unmarshal(raw, &env)
		if env.Type != protocol.TypePing {
			t.Fatalf("Close after exit sent %s %s", env.Type, env.Method)
		}
	default:
	}
}

func TestUnreadEventsDoNotStallRPCs(t *testing.T) {
	tr := newPipeTransport()
	next := fakeServer(t, tr)
	c, err := dialTransport(t.Context(), tr, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		req := next()
		var sr protocol.SubscribeRequest
		_ = json.Unmarshal(req.Payload, &sr)
		reply(t, tr, req, protocol.SubscribeResponse{SubscriptionID: sr.SubscriptionID})
		ev, _ := json.Marshal(protocol.Event{Kind: protocol.EventTextDelta, Session: "s1"})
		raw, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeEvent, SubscriptionID: sr.SubscriptionID, Payload: ev})
		for range 500 {
			tr.toClient <- pipeMsg{websocket.TextMessage, raw}
		}
		reply(t, tr, next(), protocol.DaemonStatusResponse{Version: "v"})
	}()
	_, ch, err := c.Subscribe(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing reads ch while the RPC runs.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	st, err := c.Status(ctx)
	if err != nil || st.Version != "v" {
		t.Fatalf("status with unread events = %+v, %v", st, err)
	}
	n := 0
	for range 500 {
		<-ch
		n++
	}
	if n != 500 {
		t.Fatalf("received %d events, want 500", n)
	}
}

func TestSilentServerClosesConnection(t *testing.T) {
	tr := newPipeTransport()
	fakeServer(t, tr)
	go func() {
		// Swallow pings without answering, like a half-open connection.
		for {
			select {
			case <-tr.toServer:
			case <-tr.closed:
				return
			}
		}
	}()
	c, err := dialTransport(t.Context(), tr, Options{PingInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection with no answers stayed open")
	}
}
