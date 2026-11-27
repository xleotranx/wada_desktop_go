package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wada-desktop-go/internal/protocol"
)

// ACPConfig configures a generic Agent Client Protocol (ACP) stdio JSON-RPC provider.
type ACPConfig struct {
	ID          string
	Name        string
	Binary      string
	Args        []string
	Description string
}

// acpHandshakeTimeout bounds initialize and session/new, so an agent that never
// answers fails the spawn instead of hanging it.
var acpHandshakeTimeout = 30 * time.Second

type ACPProvider struct {
	cfg ACPConfig
}

// NewACPProvider returns a Provider communicating over ACP stdio JSON-RPC 2.0.
func NewACPProvider(cfg ACPConfig) *ACPProvider {
	return &ACPProvider{cfg: cfg}
}

func (p *ACPProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{
		ID:          p.cfg.ID,
		Name:        p.cfg.Name,
		Kind:        protocol.HarnessACP,
		Binary:      p.cfg.Binary,
		Description: p.cfg.Description,
	}
}

type acpPermissionWait struct {
	rawID      json.RawMessage
	allowOnce  string
	allowAll   string
	rejectOnce string
}

type acpSession struct {
	id           string
	acpSessionID string
	proc         *ManagedProcess
	events       chan protocol.Event

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	writeMu   sync.Mutex
	mu        sync.Mutex
	nextID    atomic.Int64
	activeReq string
	pending   map[string]acpPermissionWait
	closed    bool
	closeOnce sync.Once
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorObj    `json:"error,omitempty"`
}

type rpcErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (p *ACPProvider) Start(ctx context.Context, opts SessionOptions) (Session, error) {
	bin := p.cfg.Binary
	if opts.Command != "" {
		bin = opts.Command
	}
	args := slices.Clone(p.cfg.Args)
	if len(opts.Args) > 0 {
		args = slices.Clone(opts.Args)
	}
	sctx, cancel := context.WithCancel(context.Background())
	proc, err := StartProcess(sctx, opts.Directory, bin, args, opts.Env)
	if err != nil {
		cancel()
		return nil, err
	}

	s := &acpSession{
		id:      opts.SessionID,
		proc:    proc,
		events:  make(chan protocol.Event, 128),
		ctx:     sctx,
		cancel:  cancel,
		pending: map[string]acpPermissionWait{},
	}
	s.nextID.Store(2)

	reader := bufio.NewReaderSize(proc.Stdout, 64<<10)
	hctx, hcancel := context.WithTimeout(ctx, acpHandshakeTimeout)
	err = s.handshake(hctx, reader, opts.Directory)
	hcancel()
	if err != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		cancel()
		return nil, err
	}

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.readLoop(reader)
	}()
	go func() {
		defer s.wg.Done()
		_, _ = io.Copy(io.Discard, proc.Stderr)
	}()

	return s, nil
}

func (s *acpSession) handshake(ctx context.Context, reader *bufio.Reader, cwd string) error {
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
		},
	}
	if err := s.writeFrame(initReq); err != nil {
		return err
	}
	if _, err := readRPCResponseForID(ctx, reader, "1"); err != nil {
		return fmt.Errorf("acp initialize: %w", err)
	}

	newReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "session/new",
		"params": map[string]any{
			"cwd":        cwd,
			"mcpServers": []any{},
		},
	}
	if err := s.writeFrame(newReq); err != nil {
		return err
	}
	res, err := readRPCResponseForID(ctx, reader, "2")
	if err != nil {
		return fmt.Errorf("acp session/new: %w", err)
	}
	var newOut struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &newOut)
	if newOut.SessionID == "" {
		newOut.SessionID = s.id
	}
	s.acpSessionID = newOut.SessionID
	return nil
}

func readRPCResponseForID(ctx context.Context, reader *bufio.Reader, wantID string) (json.RawMessage, error) {
	type resErr struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan resErr, 1)
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				ch <- resErr{err: err}
				return
			}
			line = []byte(strings.TrimSpace(string(line)))
			if len(line) == 0 {
				continue
			}
			var env rpcEnvelope
			if json.Unmarshal(line, &env) != nil {
				continue
			}
			if string(env.ID) == wantID {
				if env.Error != nil {
					ch <- resErr{err: errors.New(env.Error.Message)}
					return
				}
				ch <- resErr{raw: env.Result}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-ch:
		return out.raw, out.err
	}
}

func (s *acpSession) writeFrame(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.proc.Stdin.Write(data)
	return err
}

func (s *acpSession) emit(ev protocol.Event) {
	ev.Session = s.id
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *acpSession) Send(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrNotSent, err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("%w: acp session is closed", ErrNotSent)
	}
	if s.activeReq != "" {
		s.mu.Unlock()
		return fmt.Errorf("%w: acp session is already busy", ErrNotSent)
	}
	idNum := s.nextID.Add(1)
	reqID := fmt.Sprintf("%d", idNum)
	s.activeReq = reqID
	s.mu.Unlock()

	s.emit(protocol.Event{Kind: protocol.EventTurnStarted})
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      idNum,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": s.acpSessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		},
	}
	if err := s.writeFrame(req); err != nil {
		s.mu.Lock()
		s.activeReq = ""
		s.mu.Unlock()
		s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: err.Error()})
		return err
	}
	return nil
}

// Abort cancels the current turn and keeps the session for the next prompt.
// Only an agent that ignores session/cancel is killed, which ends the session.
func (s *acpSession) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.activeReq == "" || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	err := s.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": s.acpSessionID},
	})
	if err == nil {
		ended, werr := waitTurnEnd(ctx, func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.activeReq != ""
		})
		if werr != nil || ended {
			return werr
		}
	}
	s.mu.Lock()
	was := s.activeReq
	s.activeReq = ""
	s.closed = true
	s.mu.Unlock()
	_ = s.proc.Kill()
	if was != "" {
		s.emit(protocol.Event{Kind: protocol.EventTurnInterrupted})
	}
	return nil
}

func (s *acpSession) Reply(ctx context.Context, approvalID string, decision protocol.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	wait, ok := s.pending[approvalID]
	if ok {
		delete(s.pending, approvalID)
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("acp permission request was not found")
	}

	var outcome map[string]any
	switch decision {
	case protocol.DecisionOnce:
		opt := wait.allowOnce
		if opt == "" {
			opt = "allow_once"
		}
		outcome = map[string]any{"outcome": "selected", "optionId": opt}
	case protocol.DecisionAlways:
		opt := wait.allowAll
		if opt == "" {
			opt = wait.allowOnce
		}
		if opt == "" {
			opt = "allow_always"
		}
		outcome = map[string]any{"outcome": "selected", "optionId": opt}
	case protocol.DecisionReject:
		if wait.rejectOnce != "" {
			outcome = map[string]any{"outcome": "selected", "optionId": wait.rejectOnce}
		} else {
			outcome = map[string]any{"outcome": "cancelled"}
		}
	}

	return s.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      wait.rawID,
		"result":  map[string]any{"outcome": outcome},
	})
}

func (s *acpSession) Events() <-chan protocol.Event { return s.events }

func (s *acpSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		_ = s.proc.Stdin.Close()
		_ = s.proc.Kill()
		_ = s.proc.Wait()
		s.wg.Wait()
		close(s.events)
	})
	return nil
}

func (s *acpSession) readLoop(reader *bufio.Reader) {
	defer func() {
		_ = s.proc.Wait()
		s.mu.Lock()
		wasActive := s.activeReq != ""
		s.activeReq = ""
		closed := s.closed
		s.mu.Unlock()
		if wasActive && !closed {
			s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: "acp process exited unexpectedly"})
		}
	}()

	var textBuf strings.Builder
	msgCounter := 0
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}
		var env rpcEnvelope
		if json.Unmarshal([]byte(trimmed), &env) != nil {
			continue
		}

		if env.Method == "" && len(env.ID) > 0 {
			s.mu.Lock()
			isActive := strings.Trim(string(env.ID), `"`) == s.activeReq
			if isActive {
				s.activeReq = ""
			}
			s.mu.Unlock()
			if isActive {
				if textBuf.Len() > 0 {
					msgCounter++
					s.emit(protocol.Event{
						Kind: protocol.EventTextEnded,
						Text: &protocol.TextEvent{
							Message: fmt.Sprintf("acp_msg_%d", msgCounter),
							Text:    textBuf.String(),
						},
					})
					textBuf.Reset()
				}
				if env.Error != nil {
					s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: env.Error.Message})
					continue
				}
				var promptRes struct {
					StopReason string `json:"stopReason"`
				}
				_ = json.Unmarshal(env.Result, &promptRes)
				if promptRes.StopReason == "cancelled" {
					s.emit(protocol.Event{Kind: protocol.EventTurnInterrupted})
				} else {
					s.emit(protocol.Event{Kind: protocol.EventTurnSucceeded})
				}
			}
			continue
		}

		switch env.Method {
		case "session/update":
			var p struct {
				Update struct {
					SessionUpdate string `json:"sessionUpdate"`
					ToolCallID    string `json:"toolCallId"`
					Title         string `json:"title"`
					Kind          string `json:"kind"`
					Status        string `json:"status"`
					Content       struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					RawOutput any `json:"rawOutput"`
				} `json:"update"`
			}
			if json.Unmarshal(env.Params, &p) != nil {
				continue
			}
			u := p.Update
			switch u.SessionUpdate {
			case "agent_message_chunk":
				if u.Content.Text != "" {
					if textBuf.Len() == 0 {
						s.emit(protocol.Event{
							Kind: protocol.EventTextStarted,
							Text: &protocol.TextEvent{Message: fmt.Sprintf("acp_msg_%d", msgCounter+1)},
						})
					}
					textBuf.WriteString(u.Content.Text)
					s.emit(protocol.Event{
						Kind: protocol.EventTextDelta,
						Text: &protocol.TextEvent{
							Message: fmt.Sprintf("acp_msg_%d", msgCounter+1),
							Delta:   u.Content.Text,
						},
					})
				}
			case "tool_call":
				name := u.Kind
				if name == "" {
					name = u.Title
				}
				s.emit(protocol.Event{
					Kind: protocol.EventToolCalled,
					Tool: &protocol.Tool{
						ID:      u.ToolCallID,
						Name:    u.Title,
						Kind:    normalizeToolKind(name),
						Command: u.Title,
						Status:  "running",
					},
				})
			case "tool_call_update":
				if u.Status == "completed" || u.Status == "failed" {
					var out []string
					if sOut, ok := u.RawOutput.(string); ok && sOut != "" {
						out = []string{sOut}
					}
					s.emit(protocol.Event{
						Kind: protocol.EventToolFinished,
						Tool: &protocol.Tool{
							ID:     u.ToolCallID,
							Name:   u.Title,
							Kind:   normalizeToolKind(u.Kind),
							Status: u.Status,
							Output: out,
						},
					})
				}
			}
		case "session/request_permission":
			var p struct {
				ToolCall struct {
					ToolCallID string `json:"toolCallId"`
					Title      string `json:"title"`
					Kind       string `json:"kind"`
				} `json:"toolCall"`
				Options []struct {
					OptionID string `json:"optionId"`
					Kind     string `json:"kind"`
				} `json:"options"`
			}
			if json.Unmarshal(env.Params, &p) != nil {
				continue
			}
			wait := acpPermissionWait{rawID: env.ID}
			for _, o := range p.Options {
				switch o.Kind {
				case "allow_once":
					wait.allowOnce = o.OptionID
				case "allow_always":
					wait.allowAll = o.OptionID
				case "reject_once":
					wait.rejectOnce = o.OptionID
				}
			}
			approvalID := "acp_perm_" + strings.Trim(string(env.ID), `"`)
			s.mu.Lock()
			s.pending[approvalID] = wait
			s.mu.Unlock()
			s.emit(protocol.Event{
				Kind: protocol.EventApprovalRequested,
				Approval: &protocol.Approval{
					Session:   s.id,
					ID:        approvalID,
					Kind:      normalizeToolKind(p.ToolCall.Kind),
					Action:    p.ToolCall.Title,
					Resources: []string{p.ToolCall.Title},
				},
			})
		}
	}
}
