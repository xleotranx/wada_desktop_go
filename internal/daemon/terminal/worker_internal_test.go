package terminal

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/protocol"
)

// chanPTY is a PTY whose output comes from out and whose writes block until
// unblock closes.
type chanPTY struct {
	out     chan []byte
	unblock chan struct{}
	closed  chan struct{}
}

func newChanPTY() *chanPTY {
	return &chanPTY{out: make(chan []byte), unblock: make(chan struct{}), closed: make(chan struct{})}
}

func (p *chanPTY) Read(b []byte) (int, error) {
	select {
	case chunk := <-p.out:
		return copy(b, chunk), nil
	case <-p.closed:
		return 0, io.EOF
	}
}

func (p *chanPTY) Write(b []byte) (int, error) {
	select {
	case <-p.unblock:
		return len(b), nil
	case <-p.closed:
		return 0, io.ErrClosedPipe
	}
}

func (p *chanPTY) Close() error {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	return nil
}
func (*chanPTY) Resize(int, int) error { return nil }
func (*chanPTY) PID() int              { return 1 }
func (*chanPTY) Wait() (int, error)    { return 0, nil }

func startFakeTerm(t *testing.T, w *Worker, p PTY) *termProc {
	t.Helper()
	tp := &termProc{
		info:  protocol.TerminalInfo{ID: "term_fake"},
		pty:   p,
		vt:    NewVTBuffer(80, 24),
		subs:  map[*workerConn]*termSub{},
		input: make(chan []byte, inputQueueLen),
	}
	w.terms[tp.info.ID] = tp
	w.wg.Add(2)
	go func() { defer w.wg.Done(); w.pumpPTY(tp) }()
	go func() { defer w.wg.Done(); tp.writeInput() }()
	t.Cleanup(func() { _ = p.Close(); w.wg.Wait() })
	return tp
}

func TestAttachSnapshotPrecedesLaterOutput(t *testing.T) {
	w := &Worker{conns: map[*workerConn]struct{}{}, terms: map[string]*termProc{}}
	p := newChanPTY()
	tp := startFakeTerm(t, w, p)

	p.out <- []byte("before ")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(tp.vt.CaptureText(0), "before") {
		if time.Now().After(deadline) {
			t.Fatal("first output never reached the screen")
		}
		time.Sleep(5 * time.Millisecond)
	}

	server, clientEnd := net.Pipe()
	defer server.Close()
	defer clientEnd.Close()
	frames := make(chan ipcFrame, 16)
	go func() {
		for {
			f, err := readIPCFrame(clientEnd)
			if err != nil {
				close(frames)
				return
			}
			frames <- f
		}
	}()
	wc := &workerConn{conn: server}

	raw, _ := json.Marshal(protocol.TerminalAttachRequest{ID: tp.info.ID})
	payload, err := w.handleRPC(wc, "attach", raw)
	if err != nil {
		t.Fatal(err)
	}
	p.out <- []byte("after")
	// The pump has taken "after"; it must be held until the response is out.
	select {
	case f := <-frames:
		t.Fatalf("got %s frame before the attach response", f.Type)
	case <-time.After(100 * time.Millisecond):
	}

	body, _ := json.Marshal(payload)
	if err := wc.send(ipcFrame{Type: "resp", ID: "1", Payload: body}); err != nil {
		t.Fatal(err)
	}
	payload.(attachResult).term.release(wc)

	resp := <-frames
	if resp.Type != "resp" {
		t.Fatalf("first frame = %s, want resp", resp.Type)
	}
	var att attachResult
	_ = json.Unmarshal(resp.Payload, &att)
	if !strings.Contains(string(att.Snapshot), "before") || strings.Contains(string(att.Snapshot), "after") {
		t.Fatalf("snapshot = %q", att.Snapshot)
	}
	out := <-frames
	if out.Type != "output" || string(out.Data) != "after" {
		t.Fatalf("second frame = %s %q, want output \"after\"", out.Type, out.Data)
	}
}

func TestInputToStalledTerminalDoesNotBlock(t *testing.T) {
	w := &Worker{conns: map[*workerConn]struct{}{}, terms: map[string]*termProc{}}
	tp := startFakeTerm(t, w, newChanPTY())
	done := make(chan error, 1)
	go func() {
		for range inputQueueLen + 2 {
			if err := tp.queueInput([]byte("x")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("queueInput on a full queue = %v, want an error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input to a terminal that stopped reading blocked")
	}
}
