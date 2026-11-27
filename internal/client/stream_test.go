package client

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"wada-desktop-go/internal/protocol"
)

// pipeTransport is an in-memory relay.Transport whose peer is the test.
type pipeTransport struct {
	toServer chan []byte
	toClient chan []byte
	once     sync.Once
	closed   chan struct{}
}

func newPipeTransport() *pipeTransport {
	return &pipeTransport{
		toServer: make(chan []byte, 64),
		toClient: make(chan []byte, 64),
		closed:   make(chan struct{}),
	}
}

func (p *pipeTransport) ReadMessage() (int, []byte, error) {
	select {
	case raw := <-p.toClient:
		return websocket.TextMessage, raw, nil
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
		tr.toClient <- raw
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
	tr.toClient <- raw
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
		tr.toClient <- raw
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
