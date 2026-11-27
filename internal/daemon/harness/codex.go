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

// CodexConfig configures the OpenAI Codex CLI exec JSON harness.
type CodexConfig struct {
	ID          string
	Name        string
	Binary      string
	Args        []string
	Description string
}

type CodexProvider struct {
	cfg CodexConfig
}

// NewCodexProvider returns a Provider running turn-scoped Codex CLI exec processes.
func NewCodexProvider(cfg CodexConfig) *CodexProvider {
	return &CodexProvider{cfg: cfg}
}

func (p *CodexProvider) Info() protocol.HarnessInfo {
	return protocol.HarnessInfo{
		ID:          p.cfg.ID,
		Name:        p.cfg.Name,
		Kind:        protocol.HarnessCodex,
		Binary:      p.cfg.Binary,
		Description: p.cfg.Description,
	}
}

type codexSession struct {
	id       string
	opts     SessionOptions
	bin      string
	baseArgs []string
	events   chan protocol.Event

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	threadID  string
	active    *ManagedProcess
	closed    bool
	closeOnce sync.Once
}

func (p *CodexProvider) Start(_ context.Context, opts SessionOptions) (Session, error) {
	bin := p.cfg.Binary
	if opts.Command != "" {
		bin = opts.Command
	}
	baseArgs := slices.Clone(p.cfg.Args)
	if len(opts.Args) > 0 {
		baseArgs = slices.Clone(opts.Args)
	}
	sctx, cancel := context.WithCancel(context.Background())
	return &codexSession{
		id:       opts.SessionID,
		opts:     opts,
		bin:      bin,
		baseArgs: baseArgs,
		events:   make(chan protocol.Event, 128),
		ctx:      sctx,
		cancel:   cancel,
	}, nil
}

func (s *codexSession) emit(ev protocol.Event) {
	ev.Session = s.id
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *codexSession) Send(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("codex session is closed")
	}
	if s.active != nil {
		s.mu.Unlock()
		return errors.New("codex session is already busy")
	}
	threadID := s.threadID
	args := slices.Clone(s.baseArgs)
	if threadID != "" {
		args = append(args, "exec", "resume", threadID, "--json")
	} else {
		args = append(args, "exec", "--json")
	}
	if s.opts.Mode == "full" {
		args = append(args, "--full-auto")
	}
	// "--" ends option parsing, so a prompt that starts with "-" stays a prompt.
	args = append(args, "--", text)

	proc, err := StartProcess(s.ctx, s.opts.Directory, s.bin, args, s.opts.Env)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	_ = proc.Stdin.Close()
	s.active = proc
	s.wg.Add(1)
	s.mu.Unlock()

	s.emit(protocol.Event{Kind: protocol.EventTurnStarted})
	go func() {
		defer s.wg.Done()
		s.runTurn(proc)
	}()
	return nil
}

func (s *codexSession) Abort(_ context.Context) error {
	s.mu.Lock()
	proc := s.active
	s.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
	return nil
}

func (s *codexSession) Reply(_ context.Context, _ string, _ protocol.Decision) error {
	return errors.New("codex exec harness does not support interactive mid-turn approvals")
}

func (s *codexSession) Events() <-chan protocol.Event { return s.events }

func (s *codexSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		proc := s.active
		s.mu.Unlock()
		s.cancel()
		if proc != nil {
			_ = proc.Kill()
		}
		s.wg.Wait()
		close(s.events)
	})
	return nil
}

func (s *codexSession) runTurn(proc *ManagedProcess) {
	go func() { _, _ = io.Copy(io.Discard, proc.Stderr) }()

	scanner := bufio.NewScanner(proc.Stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var termEv *protocol.Event
	msgCount := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				ID               string `json:"id"`
				Type             string `json:"type"`
				Text             string `json:"text"`
				Command          string `json:"command"`
				AggregatedOutput string `json:"aggregated_output"`
				ExitCode         int    `json:"exit_code"`
				Status           string `json:"status"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}

		switch ev.Type {
		case "thread.started":
			if ev.ThreadID != "" {
				s.mu.Lock()
				s.threadID = ev.ThreadID
				s.mu.Unlock()
			}
		case "item.started":
			switch ev.Item.Type {
			case "command_execution":
				s.emit(protocol.Event{
					Kind: protocol.EventToolCalled,
					Tool: &protocol.Tool{
						ID:      ev.Item.ID,
						Name:    "command_execution",
						Kind:    protocol.ToolShell,
						Command: ev.Item.Command,
						Status:  "running",
					},
				})
			case "file_change":
				s.emit(protocol.Event{
					Kind: protocol.EventToolCalled,
					Tool: &protocol.Tool{
						ID:     ev.Item.ID,
						Name:   "file_change",
						Kind:   protocol.ToolEdit,
						Status: "running",
					},
				})
			}
		case "item.completed":
			switch ev.Item.Type {
			case "agent_message":
				msgCount++
				msgID := fmt.Sprintf("codex_msg_%d", msgCount)
				s.emit(protocol.Event{
					Kind: protocol.EventTextDelta,
					Text: &protocol.TextEvent{Message: msgID, Delta: ev.Item.Text},
				})
				s.emit(protocol.Event{
					Kind: protocol.EventTextEnded,
					Text: &protocol.TextEvent{Message: msgID, Text: ev.Item.Text},
				})
			case "command_execution":
				var out []string
				if ev.Item.AggregatedOutput != "" {
					out = []string{ev.Item.AggregatedOutput}
				}
				status := ev.Item.Status
				if status == "" {
					status = "completed"
				}
				s.emit(protocol.Event{
					Kind: protocol.EventToolFinished,
					Tool: &protocol.Tool{
						ID:      ev.Item.ID,
						Name:    "command_execution",
						Kind:    protocol.ToolShell,
						Command: ev.Item.Command,
						Output:  out,
						Status:  status,
						Exit:    ev.Item.ExitCode,
					},
				})
			case "file_change":
				status := ev.Item.Status
				if status == "" {
					status = "completed"
				}
				s.emit(protocol.Event{
					Kind: protocol.EventToolFinished,
					Tool: &protocol.Tool{
						ID:     ev.Item.ID,
						Name:   "file_change",
						Kind:   protocol.ToolEdit,
						Status: status,
					},
				})
			}
		case "turn.completed":
			termEv = &protocol.Event{Kind: protocol.EventTurnSucceeded}
		case "turn.failed", "error":
			errMsg := ev.Error.Message
			if errMsg == "" {
				errMsg = ev.Message
			}
			if errMsg == "" {
				errMsg = "codex turn failed"
			}
			termEv = &protocol.Event{Kind: protocol.EventTurnFailed, Error: errMsg}
		}
	}

	waitErr := proc.Wait()
	s.mu.Lock()
	if s.active == proc {
		s.active = nil
	}
	closed := s.closed
	s.mu.Unlock()

	if closed {
		return
	}
	if termEv != nil {
		s.emit(*termEv)
		return
	}
	if s.ctx.Err() != nil || waitErr != nil {
		s.emit(protocol.Event{Kind: protocol.EventTurnInterrupted})
	} else {
		s.emit(protocol.Event{Kind: protocol.EventTurnSucceeded})
	}
}
