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

	"wada-desktop-go/internal/protocol"
)

// ClaudeConfig configures the Claude Code CLI stream-json harness.
type ClaudeConfig struct {
	ID          string
	Name        string
	Binary      string
	Args        []string
	Description string
}

type ClaudeProvider struct {
	cfg ClaudeConfig
}

// NewClaudeProvider returns a Provider driving Claude Code CLI over stream-json.
func NewClaudeProvider(cfg ClaudeConfig) *ClaudeProvider {
	return &ClaudeProvider{cfg: cfg}
}

func (p *ClaudeProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{
		ID:          p.cfg.ID,
		Name:        p.cfg.Name,
		Kind:        protocol.HarnessClaude,
		Binary:      p.cfg.Binary,
		Description: p.cfg.Description,
	}
}

type claudeSession struct {
	id     string
	proc   *ManagedProcess
	events chan protocol.Event

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	writeMu   sync.Mutex
	mu        sync.Mutex
	busy      bool
	closed    bool
	pending   map[string]struct{}
	closeOnce sync.Once
}

func (p *ClaudeProvider) Start(_ context.Context, opts SessionOptions) (Session, error) {
	bin := p.cfg.Binary
	if opts.Command != "" {
		bin = opts.Command
	}
	var args []string
	if len(opts.Args) > 0 {
		args = slices.Clone(opts.Args)
	} else if len(p.cfg.Args) > 0 {
		args = slices.Clone(p.cfg.Args)
	} else {
		args = []string{"-p", "--verbose", "--output-format", "stream-json", "--input-format", "stream-json"}
		if opts.Mode != "full" {
			args = append(args, "--permission-prompt-tool", "stdio")
		}
	}

	sctx, cancel := context.WithCancel(context.Background())
	proc, err := StartProcess(sctx, opts.Directory, bin, args, opts.Env)
	if err != nil {
		cancel()
		return nil, err
	}

	s := &claudeSession{
		id:      opts.SessionID,
		proc:    proc,
		events:  make(chan protocol.Event, 128),
		ctx:     sctx,
		cancel:  cancel,
		pending: map[string]struct{}{},
	}

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.readLoop()
	}()
	go func() {
		defer s.wg.Done()
		_, _ = io.Copy(io.Discard, proc.Stderr)
	}()
	return s, nil
}

func (s *claudeSession) emit(ev protocol.Event) {
	ev.Session = s.id
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *claudeSession) writeJSON(v any) error {
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

func (s *claudeSession) Send(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("claude session is closed")
	}
	if s.busy {
		s.mu.Unlock()
		return errors.New("claude session is already busy")
	}
	s.busy = true
	s.mu.Unlock()

	s.emit(protocol.Event{Kind: protocol.EventTurnStarted})
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
	if err := s.writeJSON(msg); err != nil {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
		s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: err.Error()})
		return err
	}
	return nil
}

func (s *claudeSession) Abort(_ context.Context) error {
	s.mu.Lock()
	wasBusy := s.busy
	s.busy = false
	s.closed = true
	s.mu.Unlock()
	_ = s.proc.Kill()
	if wasBusy {
		s.emit(protocol.Event{Kind: protocol.EventTurnInterrupted})
	}
	return nil
}

func (s *claudeSession) Reply(ctx context.Context, approvalID string, decision protocol.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	_, ok := s.pending[approvalID]
	if ok {
		delete(s.pending, approvalID)
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("claude control request not found")
	}

	behavior := "deny"
	if decision == protocol.DecisionOnce || decision == protocol.DecisionAlways {
		behavior = "allow"
	}
	resp := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": approvalID,
			"response": map[string]any{
				"behavior": behavior,
			},
		},
	}
	return s.writeJSON(resp)
}

func (s *claudeSession) Events() <-chan protocol.Event { return s.events }

func (s *claudeSession) Close() error {
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

func (s *claudeSession) readLoop() {
	defer func() {
		_ = s.proc.Wait()
		s.mu.Lock()
		wasBusy := s.busy
		s.busy = false
		closed := s.closed
		s.mu.Unlock()
		if wasBusy && !closed {
			s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: "claude process exited unexpectedly"})
		}
	}()

	scanner := bufio.NewScanner(s.proc.Stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	msgSeq := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			IsError   bool   `json:"is_error"`
			Result    string `json:"result"`
			Event     struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
			Message struct {
				Content []struct {
					Type      string         `json:"type"`
					ID        string         `json:"id"`
					Name      string         `json:"name"`
					Text      string         `json:"text"`
					ToolUseID string         `json:"tool_use_id"`
					Content   any            `json:"content"`
					Input     map[string]any `json:"input"`
					IsError   bool           `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
			Request struct {
				Subtype  string         `json:"subtype"`
				ToolName string         `json:"tool_name"`
				Input    map[string]any `json:"input"`
			} `json:"request"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}

		switch raw.Type {
		case "stream_event":
			if raw.Event.Type == "content_block_delta" && raw.Event.Delta.Type == "text_delta" && raw.Event.Delta.Text != "" {
				s.emit(protocol.Event{
					Kind: protocol.EventTextDelta,
					Text: &protocol.TextEvent{
						Message: fmt.Sprintf("claude_msg_%d", msgSeq+1),
						Delta:   raw.Event.Delta.Text,
					},
				})
			}
		case "assistant":
			msgSeq++
			msgID := fmt.Sprintf("claude_msg_%d", msgSeq)
			for _, block := range raw.Message.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						s.emit(protocol.Event{
							Kind: protocol.EventTextDelta,
							Text: &protocol.TextEvent{Message: msgID, Delta: block.Text},
						})
						s.emit(protocol.Event{
							Kind: protocol.EventTextEnded,
							Text: &protocol.TextEvent{Message: msgID, Text: block.Text},
						})
					}
				case "tool_use":
					cmdStr, _ := block.Input["command"].(string)
					if cmdStr == "" {
						cmdStr, _ = block.Input["file_path"].(string)
					}
					if cmdStr == "" {
						cmdStr, _ = block.Input["path"].(string)
					}
					s.emit(protocol.Event{
						Kind: protocol.EventToolCalled,
						Tool: &protocol.Tool{
							ID:      block.ID,
							Name:    block.Name,
							Kind:    normalizeToolKind(block.Name),
							Command: cmdStr,
							Status:  "running",
						},
					})
				}
			}
		case "user":
			for _, block := range raw.Message.Content {
				if block.Type == "tool_result" {
					status := "completed"
					if block.IsError {
						status = "error"
					}
					var out []string
					if str, ok := block.Content.(string); ok && str != "" {
						out = []string{str}
					}
					s.emit(protocol.Event{
						Kind: protocol.EventToolFinished,
						Tool: &protocol.Tool{
							ID:     block.ToolUseID,
							Kind:   protocol.ToolOther,
							Status: status,
							Output: out,
						},
					})
				}
			}
		case "control_request":
			if raw.Request.Subtype == "can_use_tool" && raw.RequestID != "" {
				var resources []string
				for _, k := range []string{"command", "file_path", "path", "pattern"} {
					if v, ok := raw.Request.Input[k].(string); ok && v != "" {
						resources = append(resources, v)
					}
				}
				s.mu.Lock()
				s.pending[raw.RequestID] = struct{}{}
				s.mu.Unlock()
				s.emit(protocol.Event{
					Kind: protocol.EventApprovalRequested,
					Approval: &protocol.Approval{
						Session:   s.id,
						ID:        raw.RequestID,
						Kind:      normalizeToolKind(raw.Request.ToolName),
						Action:    raw.Request.ToolName,
						Resources: resources,
					},
				})
			}
		case "result":
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
			if raw.IsError {
				msg := raw.Result
				if msg == "" {
					msg = "claude turn failed"
				}
				s.emit(protocol.Event{Kind: protocol.EventTurnFailed, Error: msg})
			} else {
				s.emit(protocol.Event{Kind: protocol.EventTurnSucceeded})
			}
		}
	}
}
