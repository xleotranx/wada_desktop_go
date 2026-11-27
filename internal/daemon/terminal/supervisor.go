package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/protocol"
)

// SupervisorOptions configures wadad's connection to the detached wadad-term worker.
type SupervisorOptions struct {
	DataDir     string
	WorkerBin   string
	WorkerArgs  []string
	WorkerEnv   []string
	KeepOnClose bool
}

type supSub struct {
	ch     chan []byte
	done   chan struct{}
	closed bool
}

// Supervisor manages a persistent IPC connection to the detached terminal worker,
// spawning it lazily on first terminal operation and re-attaching after restarts.
//
// The worker tracks attachments per IPC connection, and every wadad client shares
// this one connection, so the Supervisor keeps the worker attached to a terminal
// while any local subscription for it remains and re-attaches them all whenever
// it opens a new connection.
type Supervisor struct {
	opts   SupervisorOptions
	events chan protocol.Event

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once

	connMu sync.Mutex
	conn   *ipcClient
	subMu  sync.Mutex
	subs   map[string]map[string]*supSub // termID -> clientSubKey -> stream

	// attachMu orders worker attach/detach calls against changes to subs, so a
	// detach for a terminal's last subscription never overtakes a newer attach.
	attachMu sync.Mutex
}

// retryableMethods are worker calls that are safe to resend on a new connection
// after the previous one dropped. Every other method changes worker state and
// has an unknown outcome once its connection is lost.
var retryableMethods = map[string]bool{
	"ping":    true,
	"list":    true,
	"capture": true,
	"attach":  true,
}

var errOutcomeUnknown = errors.New("terminal worker connection lost; the request's outcome is unknown")

type ipcClient struct {
	wc      *workerConn
	mu      sync.Mutex
	pending map[string]chan ipcFrame
	done    chan struct{}
	closed  bool
}

// NewSupervisor creates a Supervisor for wadad-term under opts.DataDir.
func NewSupervisor(opts SupervisorOptions) *Supervisor {
	if opts.DataDir != "" {
		opts.DataDir = filepath.Clean(opts.DataDir)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		opts:   opts,
		events: make(chan protocol.Event, 128),
		ctx:    ctx,
		cancel: cancel,
		subs:   map[string]map[string]*supSub{},
	}
}

func (s *Supervisor) Events() <-chan protocol.Event { return s.events }

func (s *Supervisor) Close() error {
	s.closeOnce.Do(func() {
		if !s.opts.KeepOnClose {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.ShutdownWorker(ctx)
			cancel()
		}
		s.cancel()

		s.connMu.Lock()
		c := s.conn
		s.conn = nil
		s.connMu.Unlock()
		if c != nil {
			c.closeNow()
		}

		s.subMu.Lock()
		for _, group := range s.subs {
			for _, sub := range group {
				sub.close()
			}
		}
		clear(s.subs)
		s.subMu.Unlock()

		s.wg.Wait()
		close(s.events)
	})
	return nil
}

// ShutdownWorker cleanly stops the detached wadad-term worker process (and its PTYs).
func (s *Supervisor) ShutdownWorker(ctx context.Context) error {
	s.connMu.Lock()
	c := s.conn
	s.connMu.Unlock()
	if c == nil {
		var err error
		c, err = s.tryDialExisting(ctx)
		if err != nil {
			return nil
		}
		defer c.closeNow()
	}
	return c.call(ctx, "shutdown", nil, nil)
}

func (s *Supervisor) WorkerPID(ctx context.Context) (int, error) {
	var out struct {
		PID int `json:"pid"`
	}
	if err := s.call(ctx, "ping", nil, &out); err != nil {
		return 0, err
	}
	return out.PID, nil
}

func (s *Supervisor) List(ctx context.Context) ([]protocol.TerminalInfo, error) {
	var out []protocol.TerminalInfo
	return out, s.call(ctx, "list", nil, &out)
}

func (s *Supervisor) Create(ctx context.Context, req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	var out protocol.TerminalInfo
	return out, s.call(ctx, "create", req, &out)
}

func (s *Supervisor) Attach(ctx context.Context, clientKey string, req protocol.TerminalAttachRequest) (protocol.TerminalInfo, []byte, <-chan []byte, func(), error) {
	subKey := clientKey + ":" + uuid.NewString()
	ch := make(chan []byte, 128)
	sub := &supSub{ch: ch, done: make(chan struct{})}

	s.attachMu.Lock()
	defer s.attachMu.Unlock()

	s.subMu.Lock()
	group := s.subs[req.ID]
	if group == nil {
		group = map[string]*supSub{}
		s.subs[req.ID] = group
	}
	group[subKey] = sub
	s.subMu.Unlock()

	var resp struct {
		Info     protocol.TerminalInfo `json:"info"`
		Snapshot []byte                `json:"snapshot"`
	}
	if err := s.call(ctx, "attach", req, &resp); err != nil {
		s.removeSub(req.ID, subKey)
		return protocol.TerminalInfo{}, nil, nil, nil, err
	}

	detach := func() {
		s.attachMu.Lock()
		defer s.attachMu.Unlock()
		if !s.removeSub(req.ID, subKey) {
			return
		}
		dctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = s.callExisting(dctx, "detach", protocol.TerminalDetachRequest{ID: req.ID}, nil)
	}
	return resp.Info, resp.Snapshot, ch, detach, nil
}

// Detach stops the worker streaming termID to wadad, unless a local
// subscription still needs it.
func (s *Supervisor) Detach(ctx context.Context, termID string) error {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.subMu.Lock()
	inUse := len(s.subs[termID]) > 0
	s.subMu.Unlock()
	if inUse {
		return nil
	}
	return s.callExisting(ctx, "detach", protocol.TerminalDetachRequest{ID: termID}, nil)
}

func (s *Supervisor) Input(ctx context.Context, termID string, data []byte) error {
	return s.call(ctx, "input", protocol.TerminalInputRequest{ID: termID, Data: data}, nil)
}

func (s *Supervisor) Resize(ctx context.Context, termID string, cols, rows int) error {
	return s.call(ctx, "resize", protocol.TerminalResizeRequest{ID: termID, Cols: cols, Rows: rows}, nil)
}

func (s *Supervisor) Capture(ctx context.Context, termID string, lines int) (protocol.TerminalCaptureResponse, error) {
	var out protocol.TerminalCaptureResponse
	return out, s.call(ctx, "capture", protocol.TerminalCaptureRequest{ID: termID, Lines: lines}, &out)
}

func (s *Supervisor) Kill(ctx context.Context, termID string) error {
	err := s.call(ctx, "kill", protocol.TerminalKillRequest{ID: termID}, nil)
	if err == nil {
		s.closeSubs(termID)
	}
	return err
}

func (sub *supSub) close() {
	if !sub.closed {
		sub.closed = true
		close(sub.done)
		close(sub.ch)
	}
}

// closeSubs ends every local stream of termID.
func (s *Supervisor) closeSubs(termID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, sub := range s.subs[termID] {
		sub.close()
	}
	delete(s.subs, termID)
}

// removeSub ends one local stream and reports whether it was the last one for
// termID, so the caller should detach the worker.
func (s *Supervisor) removeSub(termID, subKey string) bool {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	group := s.subs[termID]
	sub := group[subKey]
	if sub == nil {
		return false
	}
	sub.close()
	delete(group, subKey)
	if len(group) > 0 {
		return false
	}
	delete(s.subs, termID)
	return true
}

func (s *Supervisor) callExisting(ctx context.Context, method string, req, resp any) error {
	s.connMu.Lock()
	c := s.conn
	s.connMu.Unlock()
	if c == nil {
		return nil
	}
	return c.call(ctx, method, req, resp)
}

func (s *Supervisor) call(ctx context.Context, method string, req, resp any) error {
	c, err := s.ensureConn(ctx)
	if err != nil {
		return err
	}
	err = c.call(ctx, method, req, resp)
	if err != nil {
		select {
		case <-c.done:
			s.connMu.Lock()
			if s.conn == c {
				s.conn = nil
			}
			s.connMu.Unlock()
			if !retryableMethods[method] {
				// The worker may have applied the request before the connection
				// dropped; resending it could repeat input or create a second
				// terminal, so report the unknown outcome instead.
				return errOutcomeUnknown
			}
			c2, err2 := s.ensureConn(ctx)
			if err2 == nil {
				return c2.call(ctx, method, req, resp)
			}
		default:
		}
	}
	return err
}

func (s *Supervisor) ensureConn(ctx context.Context) (*ipcClient, error) {
	s.connMu.Lock()
	defer s.connMu.Unlock()

	if s.ctx.Err() != nil {
		return nil, errors.New("terminal supervisor closed")
	}
	if s.conn != nil {
		select {
		case <-s.conn.done:
			s.conn = nil
		default:
			return s.conn, nil
		}
	}

	if c, err := s.tryDialExisting(ctx); err == nil {
		s.conn = c
		s.reattach(c)
		return c, nil
	}

	if err := s.spawnWorker(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		c, err := s.tryDialExisting(dctx)
		cancel()
		if err == nil {
			s.conn = c
			s.reattach(c)
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
	return nil, errors.New("timed out waiting for wadad-term IPC worker")
}

// reattach asks the worker to stream every terminal that still has a local
// subscription to the new connection c. A terminal the worker no longer has
// ends its local streams, so their clients see the stream close.
func (s *Supervisor) reattach(c *ipcClient) {
	s.subMu.Lock()
	ids := make([]string, 0, len(s.subs))
	for id, group := range s.subs {
		if len(group) > 0 {
			ids = append(ids, id)
		}
	}
	s.subMu.Unlock()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
		err := c.call(ctx, "attach", protocol.TerminalAttachRequest{ID: id}, nil)
		cancel()
		if err != nil {
			s.closeSubs(id)
		}
	}
}

func (s *Supervisor) tryDialExisting(ctx context.Context) (*ipcClient, error) {
	rec, err := readWorkerRecord(s.opts.DataDir)
	if err != nil {
		return nil, err
	}
	conn, err := dialIPC(ctx, rec.Endpoint)
	if err != nil {
		return nil, err
	}
	wc := &workerConn{conn: conn}
	if err := wc.send(ipcFrame{Type: "hello", Secret: rec.Secret}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	ack, err := readIPCFrame(conn)
	if err != nil || ack.Type != "hello" {
		_ = conn.Close()
		if err == nil {
			err = errors.New("terminal worker rejected handshake")
		}
		return nil, err
	}

	ic := &ipcClient{
		wc:      wc,
		pending: map[string]chan ipcFrame{},
		done:    make(chan struct{}),
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.readClientLoop(ic)
	}()
	return ic, nil
}

func (s *Supervisor) spawnWorker() error {
	dir := workerDir(s.opts.DataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	bin := s.opts.WorkerBin
	if bin == "" {
		var err error
		bin, err = os.Executable()
		if err != nil {
			return err
		}
	}
	var args []string
	if len(s.opts.WorkerArgs) > 0 {
		args = append(args, s.opts.WorkerArgs...)
	} else {
		args = []string{"--terminal-worker", "--data-dir", s.opts.DataDir}
	}

	logFile, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	if len(s.opts.WorkerEnv) > 0 {
		cmd.Env = s.opts.WorkerEnv
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	go func() { _ = cmd.Wait() }()
	return nil
}

func (s *Supervisor) readClientLoop(ic *ipcClient) {
	defer ic.closeNow()
	for {
		f, err := readIPCFrame(ic.wc.conn)
		if err != nil {
			return
		}
		switch f.Type {
		case "resp":
			ic.mu.Lock()
			ch := ic.pending[f.ID]
			delete(ic.pending, f.ID)
			ic.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "output":
			s.subMu.Lock()
			for _, sub := range s.subs[f.TermID] {
				if !sub.closed {
					select {
					case sub.ch <- f.Data:
					default:
					}
				}
			}
			s.subMu.Unlock()
		case "updated":
			if f.Info != nil {
				cp := *f.Info
				select {
				case s.events <- protocol.Event{Kind: protocol.EventTerminalUpdated, Terminal: &cp}:
				case <-s.ctx.Done():
				}
			}
		}
	}
}

func (ic *ipcClient) closeNow() {
	ic.mu.Lock()
	if !ic.closed {
		ic.closed = true
		close(ic.done)
		for id, ch := range ic.pending {
			close(ch)
			delete(ic.pending, id)
		}
	}
	ic.mu.Unlock()
	_ = ic.wc.conn.Close()
}

func (ic *ipcClient) call(ctx context.Context, method string, req, resp any) error {
	var raw json.RawMessage
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return err
		}
		raw = b
	}
	id := uuid.NewString()
	ch := make(chan ipcFrame, 1)

	ic.mu.Lock()
	if ic.closed {
		ic.mu.Unlock()
		return errors.New("terminal ipc connection closed")
	}
	ic.pending[id] = ch
	ic.mu.Unlock()

	if err := ic.wc.send(ipcFrame{Type: "req", ID: id, Method: method, Payload: raw}); err != nil {
		ic.mu.Lock()
		delete(ic.pending, id)
		ic.mu.Unlock()
		return err
	}

	select {
	case out, ok := <-ch:
		if !ok {
			return errors.New("terminal ipc connection closed")
		}
		if out.Error != "" {
			return errors.New(out.Error)
		}
		if resp != nil && len(out.Payload) > 0 {
			return json.Unmarshal(out.Payload, resp)
		}
		return nil
	case <-ctx.Done():
		ic.mu.Lock()
		delete(ic.pending, id)
		ic.mu.Unlock()
		return ctx.Err()
	case <-ic.done:
		return errors.New("terminal ipc connection closed")
	}
}
