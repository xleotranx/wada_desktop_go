package terminal

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"wada-desktop-go/internal/protocol"
)

const maxIPCFrameBytes = 16 << 20

type ipcFrame struct {
	Type    string                 `json:"type"` // "hello" | "req" | "resp" | "output" | "updated"
	ID      string                 `json:"id,omitempty"`
	Method  string                 `json:"method,omitempty"`
	Secret  string                 `json:"secret,omitempty"`
	TermID  string                 `json:"termId,omitempty"`
	Payload json.RawMessage        `json:"payload,omitempty"`
	Data    []byte                 `json:"data,omitempty"`
	Info    *protocol.TerminalInfo `json:"info,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

func writeIPCFrame(w io.Writer, f ipcFrame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > maxIPCFrameBytes {
		return errors.New("terminal ipc frame exceeds maximum size")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func readIPCFrame(r io.Reader) (ipcFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return ipcFrame{}, err
	}
	sz := binary.BigEndian.Uint32(hdr[:])
	if sz == 0 || sz > maxIPCFrameBytes {
		return ipcFrame{}, errors.New("invalid terminal ipc frame size")
	}
	buf := make([]byte, sz)
	if _, err := io.ReadFull(r, buf); err != nil {
		return ipcFrame{}, err
	}
	var f ipcFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		return ipcFrame{}, err
	}
	return f, nil
}

type workerConn struct {
	conn    net.Conn
	writeMu sync.Mutex
}

func (c *workerConn) send(f ipcFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeIPCFrame(c.conn, f)
}

// inputQueueLen bounds a terminal's queued input writes. A shell that stops
// reading fills it, and further input is refused instead of blocking the
// connection that carries every terminal's requests.
const inputQueueLen = 256

type termProc struct {
	mu   sync.Mutex
	info protocol.TerminalInfo
	pty  PTY
	vt   *VTBuffer
	subs map[*workerConn]*termSub
	// input feeds writeInput; it is closed, under mu, when the PTY ends.
	input       chan []byte
	inputClosed bool
}

// termSub is one connection's attachment. Until the attach response with its
// snapshot is on the wire, output is held in pending so it can't arrive first.
type termSub struct {
	ready   bool
	pending [][]byte
}

type Worker struct {
	dataDir string
	secret  string
	cleanup func()

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns map[*workerConn]struct{}
	terms map[string]*termProc
	seq   atomic.Uint64
}

// RunWorkerCLI parses flags for `wadad-term` / `main --terminal-worker` and runs RunWorker.
func RunWorkerCLI(args []string) error {
	fs := flag.NewFlagSet("wadad-term", flag.ContinueOnError)
	defDir := ""
	if env := strings.TrimSpace(os.Getenv("WADA_DATA_DIR")); env != "" {
		defDir = filepath.Clean(env)
	} else if cfg, err := os.UserConfigDir(); err == nil {
		defDir = filepath.Join(cfg, "wada-desktop-go")
	}
	dataDir := fs.String("data-dir", defDir, "Wada data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunWorker(ctx, *dataDir)
}

// RunWorker hosts detached PTY sessions over authenticated local IPC until ctx cancels
// or a client sends a shutdown RPC.
func RunWorker(ctx context.Context, dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return errors.New("terminal worker: dataDir is required")
	}
	var rawSec [32]byte
	if _, err := rand.Read(rawSec[:]); err != nil {
		return err
	}
	secret := hex.EncodeToString(rawSec[:])

	ln, endpoint, cleanup, err := listenIPC(dataDir)
	if err != nil {
		return err
	}

	wctx, cancel := context.WithCancel(ctx)
	w := &Worker{
		dataDir: dataDir,
		secret:  secret,
		cleanup: cleanup,
		ctx:     wctx,
		cancel:  cancel,
		conns:   map[*workerConn]struct{}{},
		terms:   map[string]*termProc{},
	}

	rec := WorkerRecord{
		PID:       os.Getpid(),
		Endpoint:  endpoint,
		Secret:    secret,
		Version:   workerVersion,
		StartedAt: time.Now().UnixMilli(),
	}
	if err := writeWorkerRecord(dataDir, rec); err != nil {
		_ = ln.Close()
		cleanup()
		cancel()
		return err
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		<-wctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		w.wg.Add(1)
		go func(c net.Conn) {
			defer w.wg.Done()
			w.serveConn(c)
		}(conn)
	}

	w.shutdownAll()
	w.wg.Wait()
	return nil
}

func (w *Worker) shutdownAll() {
	removeWorkerRecord(w.dataDir)
	if w.cleanup != nil {
		w.cleanup()
	}

	w.mu.Lock()
	conns := make([]*workerConn, 0, len(w.conns))
	for c := range w.conns {
		conns = append(conns, c)
	}
	terms := make([]*termProc, 0, len(w.terms))
	for _, t := range w.terms {
		terms = append(terms, t)
	}
	w.mu.Unlock()

	for _, c := range conns {
		_ = c.conn.Close()
	}
	for _, t := range terms {
		_ = t.pty.Close()
	}
}

func (w *Worker) serveConn(rawConn net.Conn) {
	defer rawConn.Close()
	_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	hello, err := readIPCFrame(rawConn)
	if err != nil || hello.Type != "hello" || subtle.ConstantTimeCompare([]byte(hello.Secret), []byte(w.secret)) != 1 {
		return
	}
	_ = rawConn.SetReadDeadline(time.Time{})

	wc := &workerConn{conn: rawConn}
	if err := wc.send(ipcFrame{Type: "hello"}); err != nil {
		return
	}

	w.mu.Lock()
	w.conns[wc] = struct{}{}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.conns, wc)
		for _, t := range w.terms {
			t.mu.Lock()
			delete(t.subs, wc)
			t.mu.Unlock()
		}
		w.mu.Unlock()
	}()

	for {
		req, err := readIPCFrame(rawConn)
		if err != nil {
			return
		}
		if req.Type != "req" {
			continue
		}
		payload, rerr := w.handleRPC(wc, req.Method, req.Payload)
		resp := ipcFrame{Type: "resp", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr.Error()
		} else if payload != nil {
			resp.Payload, _ = json.Marshal(payload)
		}
		err = wc.send(resp)
		if att, ok := payload.(attachResult); ok {
			att.term.release(wc)
		}
		if err != nil {
			return
		}
	}
}

func (w *Worker) broadcastUpdated(info protocol.TerminalInfo) {
	w.mu.Lock()
	conns := make([]*workerConn, 0, len(w.conns))
	for c := range w.conns {
		conns = append(conns, c)
	}
	w.mu.Unlock()
	cp := info
	for _, c := range conns {
		_ = c.send(ipcFrame{Type: "updated", TermID: info.ID, Info: &cp})
	}
}

func (w *Worker) handleRPC(wc *workerConn, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "ping":
		return map[string]int{"pid": os.Getpid()}, nil
	case "shutdown":
		w.cancel()
		return map[string]bool{"ok": true}, nil
	case "list":
		w.mu.Lock()
		list := make([]*termProc, 0, len(w.terms))
		for _, t := range w.terms {
			list = append(list, t)
		}
		w.mu.Unlock()
		out := make([]protocol.TerminalInfo, 0, len(list))
		for _, t := range list {
			t.mu.Lock()
			out = append(out, t.info)
			t.mu.Unlock()
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].CreatedAt != out[j].CreatedAt {
				return out[i].CreatedAt > out[j].CreatedAt
			}
			return out[i].ID < out[j].ID
		})
		return out, nil
	case "create":
		var req protocol.TerminalCreateRequest
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
		}
		return w.createTerm(req)
	case "attach":
		var req protocol.TerminalAttachRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		t := w.getTerm(req.ID)
		if t == nil {
			return nil, errors.New("terminal not found")
		}
		if req.Cols > 0 && req.Rows > 0 {
			_ = t.resize(req.Cols, req.Rows)
		}
		// pumpPTY writes the screen and reads the subscribers under t.mu, so
		// each chunk is either in this snapshot or delivered after it.
		t.mu.Lock()
		t.subs[wc] = &termSub{}
		info := t.info
		snap := t.vt.Snapshot()
		t.mu.Unlock()
		return attachResult{Info: info, Snapshot: snap, term: t}, nil
	case "detach":
		var req protocol.TerminalDetachRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if t := w.getTerm(req.ID); t != nil {
			t.mu.Lock()
			delete(t.subs, wc)
			t.mu.Unlock()
		}
		return map[string]bool{"ok": true}, nil
	case "input":
		var req protocol.TerminalInputRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		t := w.getTerm(req.ID)
		if t == nil {
			return nil, errors.New("terminal not found")
		}
		if err := t.queueInput(req.Data); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "resize":
		var req protocol.TerminalResizeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		t := w.getTerm(req.ID)
		if t == nil {
			return nil, errors.New("terminal not found")
		}
		if err := t.resize(req.Cols, req.Rows); err != nil {
			return nil, err
		}
		t.mu.Lock()
		info := t.info
		t.mu.Unlock()
		w.broadcastUpdated(info)
		return map[string]bool{"ok": true}, nil
	case "capture":
		var req protocol.TerminalCaptureRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		t := w.getTerm(req.ID)
		if t == nil {
			return nil, errors.New("terminal not found")
		}
		return protocol.TerminalCaptureResponse{
			Text:     t.vt.CaptureText(req.Lines),
			Snapshot: t.vt.Snapshot(),
		}, nil
	case "kill":
		var req protocol.TerminalKillRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		t := w.getTerm(req.ID)
		if t == nil {
			return nil, errors.New("terminal not found")
		}
		writeClosingMarker(w.dataDir, req.ID)
		_ = t.pty.Close()
		removeClosingMarker(w.dataDir, req.ID)
		w.mu.Lock()
		delete(w.terms, req.ID)
		w.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	default:
		return nil, fmt.Errorf("unknown worker method %q", method)
	}
}

func (w *Worker) getTerm(id string) *termProc {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.terms[id]
}

func (w *Worker) createTerm(req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	cols, rows := normalizeSize(req.Cols, req.Rows)
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	sh := defaultShell(req.Shell)
	ptyInst, err := startPTY(StartOptions{
		Directory: dir,
		Shell:     sh,
		Cols:      cols,
		Rows:      rows,
	})
	if err != nil {
		return protocol.TerminalInfo{}, err
	}

	id := fmt.Sprintf("term_%d_%d", w.seq.Add(1), time.Now().UnixNano()%100000)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(sh)
	}
	info := protocol.TerminalInfo{
		ID:        id,
		Name:      name,
		Directory: dir,
		Shell:     sh,
		Cols:      cols,
		Rows:      rows,
		PID:       ptyInst.PID(),
		Running:   true,
		CreatedAt: time.Now().UnixMilli(),
	}

	tp := &termProc{
		info:  info,
		pty:   ptyInst,
		vt:    NewVTBuffer(cols, rows),
		subs:  map[*workerConn]*termSub{},
		input: make(chan []byte, inputQueueLen),
	}

	w.mu.Lock()
	w.terms[id] = tp
	w.wg.Add(2)
	w.mu.Unlock()

	go func() {
		defer w.wg.Done()
		w.pumpPTY(tp)
	}()
	go func() {
		defer w.wg.Done()
		tp.writeInput()
	}()

	w.broadcastUpdated(info)
	return info, nil
}

func (t *termProc) resize(cols, rows int) error {
	c, r := normalizeSize(cols, rows)
	if err := t.pty.Resize(c, r); err != nil {
		return err
	}
	t.vt.Resize(c, r)
	t.mu.Lock()
	t.info.Cols = c
	t.info.Rows = r
	t.mu.Unlock()
	return nil
}

// attachResult is the attach response; serveConn releases the held output
// once it has sent it.
type attachResult struct {
	Info     protocol.TerminalInfo `json:"info"`
	Snapshot []byte                `json:"snapshot,omitempty"`
	term     *termProc
}

// release sends wc the output held since its attach, then lets pumpPTY send to
// it directly. Output that arrives meanwhile joins the held output, so the
// order is kept.
func (t *termProc) release(wc *workerConn) {
	for {
		t.mu.Lock()
		sub := t.subs[wc]
		if sub == nil || len(sub.pending) == 0 {
			if sub != nil {
				sub.ready = true
			}
			t.mu.Unlock()
			return
		}
		held := sub.pending
		sub.pending = nil
		id := t.info.ID
		t.mu.Unlock()
		for _, chunk := range held {
			_ = wc.send(ipcFrame{Type: "output", TermID: id, Data: chunk})
		}
	}
}

func (t *termProc) queueInput(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inputClosed {
		return errors.New("terminal has exited")
	}
	select {
	case t.input <- slices.Clone(data):
		return nil
	default:
		return errors.New("terminal is not reading its input")
	}
}

func (t *termProc) closeInput() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.inputClosed {
		t.inputClosed = true
		close(t.input)
	}
}

// writeInput writes queued input to the PTY until the queue closes.
func (t *termProc) writeInput() {
	for data := range t.input {
		_, _ = t.pty.Write(data)
	}
}

func (w *Worker) pumpPTY(tp *termProc) {
	defer tp.closeInput()
	buf := make([]byte, 32<<10)
	for {
		n, err := tp.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			tp.mu.Lock()
			tp.vt.Write(chunk)
			subs := make([]*workerConn, 0, len(tp.subs))
			for c, sub := range tp.subs {
				if sub.ready {
					subs = append(subs, c)
				} else {
					sub.pending = append(sub.pending, chunk)
				}
			}
			id := tp.info.ID
			tp.mu.Unlock()

			for _, c := range subs {
				_ = c.send(ipcFrame{Type: "output", TermID: id, Data: chunk})
			}
		}
		if err != nil {
			break
		}
	}

	exitCode, _ := tp.pty.Wait()
	tp.mu.Lock()
	tp.info.Running = false
	tp.info.ExitCode = exitCode
	info := tp.info
	tp.mu.Unlock()
	w.broadcastUpdated(info)
}
