// Package orchestrator unifies embedded PiG (Chief Agent) with external CLI
// coding harnesses (ACP, Claude Code, Codex) and Git worktrees inside wadad.
package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/terminal"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

// Options configures a multi-provider Orchestrator.
type Options struct {
	DataDir   string
	PiG       *pig.Runtime
	Worktrees *worktree.Manager
	Registry  *harness.Registry
	Terminals *terminal.Supervisor
}

type managedWorker struct {
	mu          sync.Mutex
	info        protocol.WorkerInfo
	sess        harness.Session
	msgs        []protocol.Message
	activeText  strings.Builder
	activeTools []protocol.Part
	turnOpen    bool
	msgSeq      int
	// approvals are the ones still waiting for a reply, by ID.
	approvals map[string]protocol.Approval
}

// Orchestrator implements server.Backend and server.OrchestrationBackend across
// embedded PiG sessions and supervised external CLI harness workers.
type Orchestrator struct {
	pig       *pig.Runtime
	worktrees *worktree.Manager
	registry  *harness.Registry
	terminals *terminal.Supervisor

	ctx    context.Context
	cancel context.CancelFunc
	events chan protocol.Event
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool // set by Close; no worker is added after it
	workers map[string]*managedWorker
	waiters map[string][]chan struct{}
}

// New creates an Orchestrator and starts forwarding PiG events into its merged stream.
func New(ctx context.Context, opts Options) (*Orchestrator, error) {
	if opts.PiG == nil {
		return nil, errors.New("orchestrator: PiG runtime is required")
	}
	if opts.Worktrees == nil {
		return nil, errors.New("orchestrator: worktree manager is required")
	}
	if opts.Registry == nil {
		opts.Registry = harness.DefaultRegistry()
	}
	octx, cancel := context.WithCancel(ctx)
	o := &Orchestrator{
		pig:       opts.PiG,
		worktrees: opts.Worktrees,
		registry:  opts.Registry,
		terminals: opts.Terminals,
		ctx:       octx,
		cancel:    cancel,
		events:    make(chan protocol.Event, 512),
		workers:   map[string]*managedWorker{},
		waiters:   map[string][]chan struct{}{},
	}

	pigEvents := o.pig.Events()
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.pumpPiG(pigEvents)
	}()
	if o.terminals != nil {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			o.pumpTerminals()
		}()
	}
	return o, nil
}

func (o *Orchestrator) Origin() string { return o.pig.Origin() }

func (o *Orchestrator) Events() <-chan protocol.Event { return o.events }

func (o *Orchestrator) Close() error {
	o.cancel()

	o.mu.Lock()
	o.closed = true
	workers := make([]*managedWorker, 0, len(o.workers))
	for _, w := range o.workers {
		workers = append(workers, w)
	}
	o.mu.Unlock()

	for _, w := range workers {
		_ = w.sess.Close()
		o.notifyWaiters(w.id())
	}

	err := o.pig.Close()
	if o.terminals != nil {
		if terr := o.terminals.Close(); err == nil {
			err = terr
		}
	}
	o.wg.Wait()
	close(o.events)
	return err
}

func (o *Orchestrator) pumpTerminals() {
	ch := o.terminals.Events()
	for {
		select {
		case <-o.ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			o.emit(ev)
		}
	}
}

func (o *Orchestrator) emit(ev protocol.Event) {
	select {
	case o.events <- ev:
	case <-o.ctx.Done():
	}
}

func (o *Orchestrator) pumpPiG(ch <-chan protocol.Event) {
	for {
		select {
		case <-o.ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			o.emit(ev)
		}
	}
}

func (w *managedWorker) id() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.info.ID
}

func (w *managedWorker) snapshot() protocol.WorkerInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.info
}

func (o *Orchestrator) pumpWorker(w *managedWorker) {
	ch := w.sess.Events()
	for ev := range ch {
		var (
			forward      []protocol.Event
			workerUpdate *protocol.WorkerInfo
			settled      bool
		)

		w.mu.Lock()
		ev.Session = w.info.ID
		now := time.Now().UnixMilli()
		w.info.Updated = now
		parentID := w.info.ParentSession

		switch ev.Kind {
		case protocol.EventTurnStarted:
			w.info.Status = protocol.WorkerRunning
			w.info.LastError = ""
			w.turnOpen = true
			cp := w.info
			workerUpdate = &cp
			forward = append(forward, ev)

		case protocol.EventTextDelta:
			if ev.Text != nil {
				w.activeText.WriteString(ev.Text.Delta)
			}
			forward = append(forward, ev)

		case protocol.EventToolCalled:
			if ev.Tool != nil {
				tCopy := *ev.Tool
				w.activeTools = append(w.activeTools, protocol.Part{Kind: protocol.PartTool, Tool: &tCopy})
			}
			forward = append(forward, ev)

		case protocol.EventToolFinished:
			if ev.Tool != nil {
				updated := false
				for i := len(w.activeTools) - 1; i >= 0; i-- {
					t := w.activeTools[i].Tool
					if t != nil && t.ID == ev.Tool.ID {
						t.Status = ev.Tool.Status
						if len(ev.Tool.Output) > 0 {
							t.Output = slices.Clone(ev.Tool.Output)
						}
						t.Exit = ev.Tool.Exit
						updated = true
						break
					}
				}
				if !updated {
					tCopy := *ev.Tool
					w.activeTools = append(w.activeTools, protocol.Part{Kind: protocol.PartTool, Tool: &tCopy})
				}
			}
			forward = append(forward, ev)

		case protocol.EventApprovalRequested:
			w.info.Status = protocol.WorkerWaitingApproval
			cp := w.info
			workerUpdate = &cp
			if ev.Approval != nil {
				apCopy := *ev.Approval
				apCopy.Session = w.info.ID
				ev.Approval = &apCopy
				if w.approvals == nil {
					w.approvals = map[string]protocol.Approval{}
				}
				w.approvals[apCopy.ID] = apCopy
				if parentID != "" {
					parentEv := ev
					parentEv.Session = parentID
					forward = append(forward, parentEv)
				} else {
					forward = append(forward, ev)
				}
			} else {
				forward = append(forward, ev)
			}

		case protocol.EventTurnSucceeded:
			w.finalizeTurnLocked(protocol.WorkerCompleted, "")
			cp := w.info
			workerUpdate = &cp
			settled = true
			forward = append(forward, ev)

		case protocol.EventTurnFailed:
			errMsg := ev.Error
			if errMsg == "" {
				errMsg = "turn failed"
			}
			w.finalizeTurnLocked(protocol.WorkerFailed, errMsg)
			cp := w.info
			workerUpdate = &cp
			settled = true
			forward = append(forward, ev)

		case protocol.EventTurnInterrupted:
			w.finalizeTurnLocked(protocol.WorkerStopped, "interrupted")
			cp := w.info
			workerUpdate = &cp
			settled = true
			forward = append(forward, ev)

		case protocol.EventTextStarted, protocol.EventTextEnded, protocol.EventToolStarted,
			protocol.EventInteractionRequested, protocol.EventModelChanged, protocol.EventSessionRenamed,
			protocol.EventSessionDeleted, protocol.EventModelsChanged, protocol.EventProviderAuth,
			protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
			forward = append(forward, ev)
		}
		w.mu.Unlock()

		for _, fev := range forward {
			o.emit(fev)
		}
		if workerUpdate != nil {
			updateSess := workerUpdate.ParentSession
			if updateSess == "" {
				updateSess = workerUpdate.ID
			}
			o.emit(protocol.Event{
				Kind:    protocol.EventWorkerUpdated,
				Session: updateSess,
				Worker:  workerUpdate,
			})
		}
		if settled {
			o.notifyWaiters(w.id())
		}
	}

	// Stream closed: if turn stayed open, mark stopped and unblock waiters.
	w.mu.Lock()
	var workerUpdate *protocol.WorkerInfo
	if w.turnOpen {
		w.finalizeTurnLocked(protocol.WorkerStopped, "")
		cp := w.info
		workerUpdate = &cp
	}
	w.mu.Unlock()
	if workerUpdate != nil {
		updateSess := workerUpdate.ParentSession
		if updateSess == "" {
			updateSess = workerUpdate.ID
		}
		o.emit(protocol.Event{
			Kind:    protocol.EventWorkerUpdated,
			Session: updateSess,
			Worker:  workerUpdate,
		})
	}
	o.notifyWaiters(w.id())
}

func (w *managedWorker) finalizeTurnLocked(status protocol.WorkerStatus, lastErr string) {
	w.info.Status = status
	w.info.LastError = lastErr
	w.turnOpen = false
	clear(w.approvals) // nothing waits on a finished turn's approvals
	parts := slices.Clone(w.activeTools)
	if text := strings.TrimSpace(w.activeText.String()); text != "" {
		parts = append(parts, protocol.Part{Kind: protocol.PartText, Text: text})
	}
	if len(parts) > 0 {
		w.msgSeq++
		w.msgs = append(w.msgs, protocol.Message{
			ID:    fmt.Sprintf("%s_m%d", w.info.ID, w.msgSeq),
			Role:  protocol.RoleAssistant,
			Parts: parts,
			Model: protocol.ModelRef{Provider: w.info.Provider, ID: w.info.Provider},
		})
	}
	w.activeText.Reset()
	w.activeTools = nil
}

func (o *Orchestrator) notifyWaiters(workerID string) {
	o.mu.Lock()
	chans := o.waiters[workerID]
	delete(o.waiters, workerID)
	o.mu.Unlock()
	for _, ch := range chans {
		close(ch)
	}
}

func (o *Orchestrator) subscribeWaiter(workerID string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	o.mu.Lock()
	o.waiters[workerID] = append(o.waiters[workerID], ch)
	o.mu.Unlock()
	return ch, func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		list := o.waiters[workerID]
		for i, item := range list {
			if item == ch {
				o.waiters[workerID] = append(list[:i], list[i+1:]...)
				break
			}
		}
	}
}

func (o *Orchestrator) getWorker(id string) *managedWorker {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.workers[id]
}

type spawnWorkerArgs struct {
	ParentSession string
	Provider      string
	Name          string
	Directory     string
	Branch        string
	WorktreePath  string
	Mode          string
	Command       string
	Args          []string
	Env           []string
}

func (o *Orchestrator) spawnWorker(ctx context.Context, args spawnWorkerArgs) (protocol.WorkerInfo, error) {
	prov, ok := o.registry.Get(args.Provider)
	if !ok {
		return protocol.WorkerInfo{}, fmt.Errorf("unknown harness provider %q", args.Provider)
	}
	dir := args.Directory
	if dir == "" {
		dir = args.WorktreePath
	}
	if dir == "" {
		return protocol.WorkerInfo{}, errors.New("worker directory is required")
	}
	mode := args.Mode
	if mode == "" {
		mode = "ask"
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		name = prov.Info().Name
	}

	workerID := "wrk_" + uuid.NewString()[:12]
	sess, err := prov.Start(ctx, harness.SessionOptions{
		SessionID: workerID,
		Directory: dir,
		Mode:      mode,
		Command:   args.Command,
		Args:      args.Args,
		Env:       args.Env,
	})
	if err != nil {
		return protocol.WorkerInfo{}, err
	}

	info := protocol.WorkerInfo{
		ID:            workerID,
		ParentSession: args.ParentSession,
		Provider:      args.Provider,
		Name:          name,
		Directory:     dir,
		Branch:        args.Branch,
		WorktreePath:  args.WorktreePath,
		Mode:          mode,
		Status:        protocol.WorkerIdle,
		Updated:       time.Now().UnixMilli(),
	}
	w := &managedWorker{info: info, sess: sess}

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		_ = sess.Close()
		return protocol.WorkerInfo{}, errors.New("orchestrator is shutting down")
	}
	o.workers[workerID] = w
	o.wg.Add(1)
	o.mu.Unlock()

	go func() {
		defer o.wg.Done()
		o.pumpWorker(w)
	}()

	updateSess := info.ParentSession
	if updateSess == "" {
		updateSess = info.ID
	}
	o.emit(protocol.Event{
		Kind:    protocol.EventWorkerUpdated,
		Session: updateSess,
		Worker:  &info,
	})
	return info, nil
}

// ---- server.OrchestrationBackend methods ----

func (o *Orchestrator) Harnesses(_ context.Context) ([]protocol.HarnessInfo, error) {
	out := []protocol.HarnessInfo{{
		ID:          "pig",
		Name:        "PiG (Chief Agent)",
		Kind:        protocol.HarnessEmbedded,
		Available:   true,
		Description: "Built-in coding agent and multi-harness orchestrator with Wada-managed credentials.",
	}}
	out = append(out, o.registry.List()...)
	return out, nil
}

func (o *Orchestrator) Workers(_ context.Context, parentSession string) ([]protocol.WorkerInfo, error) {
	o.mu.Lock()
	list := make([]*managedWorker, 0, len(o.workers))
	for _, w := range o.workers {
		list = append(list, w)
	}
	o.mu.Unlock()

	var out []protocol.WorkerInfo
	for _, w := range list {
		info := w.snapshot()
		if parentSession != "" && info.ParentSession != parentSession {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated != out[j].Updated {
			return out[i].Updated > out[j].Updated
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (o *Orchestrator) ListWorktrees(ctx context.Context, dir string) ([]protocol.WorktreeInfo, error) {
	return o.worktrees.List(ctx, dir)
}

func (o *Orchestrator) CreateWorktree(ctx context.Context, dir, name, baseRef string) (protocol.WorktreeInfo, error) {
	return o.worktrees.Create(ctx, dir, name, baseRef)
}

func (o *Orchestrator) RemoveWorktree(ctx context.Context, dir, wtPath string, force bool) error {
	return o.worktrees.Remove(ctx, dir, wtPath, force)
}

func (o *Orchestrator) WorktreePR(ctx context.Context, dir string) (*protocol.PRStatus, error) {
	return o.worktrees.PRStatus(ctx, dir)
}

func (o *Orchestrator) CreateWorktreePR(ctx context.Context, req protocol.WorktreeCreatePRRequest) (*protocol.PRStatus, error) {
	return o.worktrees.CreatePR(ctx, req)
}

// ---- server.TerminalBackend methods ----

func (o *Orchestrator) ListTerminals(ctx context.Context) ([]protocol.TerminalInfo, error) {
	if o.terminals == nil {
		return nil, errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.List(ctx)
}

func (o *Orchestrator) CreateTerminal(ctx context.Context, req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	if o.terminals == nil {
		return protocol.TerminalInfo{}, errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Create(ctx, req)
}

func (o *Orchestrator) AttachTerminal(ctx context.Context, clientID string, req protocol.TerminalAttachRequest) (protocol.TerminalInfo, []byte, <-chan []byte, func(), error) {
	if o.terminals == nil {
		return protocol.TerminalInfo{}, nil, nil, nil, errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Attach(ctx, clientID, req)
}

func (o *Orchestrator) DetachTerminal(ctx context.Context, _, id string) error {
	if o.terminals == nil {
		return errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Detach(ctx, id)
}

func (o *Orchestrator) TerminalInput(ctx context.Context, id string, data []byte) error {
	if o.terminals == nil {
		return errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Input(ctx, id, data)
}

func (o *Orchestrator) TerminalResize(ctx context.Context, id string, cols, rows int) error {
	if o.terminals == nil {
		return errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Resize(ctx, id, cols, rows)
}

func (o *Orchestrator) CaptureTerminal(ctx context.Context, id string, lines int) (protocol.TerminalCaptureResponse, error) {
	if o.terminals == nil {
		return protocol.TerminalCaptureResponse{}, errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Capture(ctx, id, lines)
}

func (o *Orchestrator) KillTerminal(ctx context.Context, id string) error {
	if o.terminals == nil {
		return errors.New("terminal subsystem is unavailable")
	}
	return o.terminals.Kill(ctx, id)
}

// ---- server.Backend delegation + worker routing ----

func (o *Orchestrator) Sessions(ctx context.Context, q protocol.SessionQuery) (protocol.SessionPage, error) {
	page, err := o.pig.Sessions(ctx, q)
	if err != nil {
		return protocol.SessionPage{}, err
	}
	for i := range page.Sessions {
		if page.Sessions[i].Provider == "" {
			page.Sessions[i].Provider = "pig"
		}
	}

	// On first page, include standalone (non-child) external worker sessions.
	if q.Cursor == "" {
		workers, _ := o.Workers(ctx, "")
		for _, w := range workers {
			if w.ParentSession != "" {
				continue
			}
			if q.Directory != "" && q.Directory != w.Directory {
				continue
			}
			if q.Search != "" && !strings.Contains(strings.ToLower(w.Name), strings.ToLower(q.Search)) {
				continue
			}
			page.Sessions = append(page.Sessions, protocol.Session{
				ID:        w.ID,
				Title:     w.Name,
				Directory: w.Directory,
				Provider:  w.Provider,
				Updated:   w.Updated,
				Model:     protocol.ModelRef{Provider: w.Provider, ID: w.Provider},
			})
		}
		sort.Slice(page.Sessions, func(i, j int) bool {
			if page.Sessions[i].Updated != page.Sessions[j].Updated {
				return page.Sessions[i].Updated > page.Sessions[j].Updated
			}
			return page.Sessions[i].ID < page.Sessions[j].ID
		})
	}
	return page, nil
}

func (o *Orchestrator) Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	if req.Provider == "" || req.Provider == "pig" {
		s, err := o.pig.Create(ctx, req)
		if err == nil && s.Provider == "" {
			s.Provider = "pig"
		}
		return s, err
	}
	info, err := o.spawnWorker(ctx, spawnWorkerArgs{
		Provider:  req.Provider,
		Name:      req.Title,
		Directory: req.Directory,
		Mode:      req.Mode,
	})
	if err != nil {
		return protocol.Session{}, err
	}
	return protocol.Session{
		ID:        info.ID,
		Title:     info.Name,
		Directory: info.Directory,
		Provider:  info.Provider,
		Updated:   info.Updated,
		Model:     protocol.ModelRef{Provider: info.Provider, ID: info.Provider},
	}, nil
}

func (o *Orchestrator) Messages(ctx context.Context, id string) ([]protocol.Message, error) {
	if w := o.getWorker(id); w != nil {
		w.mu.Lock()
		defer w.mu.Unlock()
		return slices.Clone(w.msgs), nil
	}
	return o.pig.Messages(ctx, id)
}

func (o *Orchestrator) Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	if w := o.getWorker(req.Session); w != nil {
		if err := harnessCanTake(req); err != nil {
			return "", fmt.Errorf("%w: %s %w", pig.ErrNotSent, w.snapshot().Provider, err)
		}
		// Mark the worker running before the prompt goes out, so a wait right
		// after this send cannot see the previous turn's result.
		w.mu.Lock()
		prevStatus, prevErr := w.info.Status, w.info.LastError
		w.msgSeq++
		msgID := fmt.Sprintf("%s_m%d", w.info.ID, w.msgSeq)
		w.msgs = append(w.msgs, protocol.Message{ID: msgID, Role: protocol.RoleUser, Text: req.Text})
		w.info.Status = protocol.WorkerRunning
		w.info.LastError = ""
		w.info.Updated = time.Now().UnixMilli()
		running := w.info
		w.mu.Unlock()
		o.emitWorker(running)

		err := w.sess.Send(ctx, req.Text)
		if err == nil {
			return protocol.SendAccepted, nil
		}
		if !errors.Is(err, harness.ErrNotSent) {
			// Part of the prompt may have reached the harness; the outcome is
			// unknown, and the harness reports the turn's end itself.
			return "", err
		}
		w.mu.Lock()
		w.msgs = slices.DeleteFunc(w.msgs, func(m protocol.Message) bool { return m.ID == msgID })
		var restored *protocol.WorkerInfo
		if !w.turnOpen && w.info.Status == protocol.WorkerRunning {
			w.info.Status, w.info.LastError = prevStatus, prevErr
			cp := w.info
			restored = &cp
		}
		w.mu.Unlock()
		if restored != nil {
			o.emitWorker(*restored)
		}
		return "", fmt.Errorf("%w: %w", pig.ErrNotSent, err)
	}
	return o.pig.Send(ctx, req)
}

// harnessCanTake refuses what an external CLI harness cannot be given: it
// takes a prompt's text, nothing else.
func harnessCanTake(req protocol.SendRequest) error {
	var unsupported []string
	if len(req.Files) > 0 {
		unsupported = append(unsupported, "attached files")
	}
	if len(req.Skills) > 0 {
		unsupported = append(unsupported, "skills")
	}
	if req.Instructions != "" || req.Internal {
		unsupported = append(unsupported, "internal instructions")
	}
	if req.Policy != nil {
		unsupported = append(unsupported, "tool policies")
	}
	if len(req.ResultSchema) > 0 {
		unsupported = append(unsupported, "structured results")
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("sessions take text only, not %s", strings.Join(unsupported, ", "))
}

// emitWorker reports info to its parent conversation, or to the worker's own
// session when it has no parent.
func (o *Orchestrator) emitWorker(info protocol.WorkerInfo) {
	updateSess := info.ParentSession
	if updateSess == "" {
		updateSess = info.ID
	}
	o.emit(protocol.Event{Kind: protocol.EventWorkerUpdated, Session: updateSess, Worker: &info})
}

func (o *Orchestrator) Abort(ctx context.Context, id string) error {
	if w := o.getWorker(id); w != nil {
		return w.sess.Abort(ctx)
	}
	return o.pig.Abort(ctx, id)
}

func (o *Orchestrator) Reply(ctx context.Context, req protocol.ReplyRequest) error {
	if w := o.getWorker(req.Session); w != nil {
		if err := w.sess.Reply(ctx, req.Approval, req.Decision); err != nil {
			return err
		}
		w.mu.Lock()
		delete(w.approvals, req.Approval)
		if w.turnOpen && w.info.Status == protocol.WorkerWaitingApproval && len(w.approvals) == 0 {
			w.info.Status = protocol.WorkerRunning
		}
		w.info.Updated = time.Now().UnixMilli()
		cp := w.info
		w.mu.Unlock()
		o.emitWorker(cp)
		return nil
	}
	return o.pig.Reply(ctx, req)
}

func (o *Orchestrator) Rename(ctx context.Context, id, title string) error {
	if w := o.getWorker(id); w != nil {
		w.mu.Lock()
		w.info.Name = title
		w.info.Updated = time.Now().UnixMilli()
		cp := w.info
		w.mu.Unlock()
		o.emit(protocol.Event{Kind: protocol.EventSessionRenamed, Session: id, Title: title})
		if cp.ParentSession != "" {
			o.emit(protocol.Event{Kind: protocol.EventWorkerUpdated, Session: cp.ParentSession, Worker: &cp})
		}
		return nil
	}
	return o.pig.Rename(ctx, id, title)
}

func (o *Orchestrator) Delete(ctx context.Context, id string) error {
	if w := o.getWorker(id); w != nil {
		_ = w.sess.Close()
		o.mu.Lock()
		delete(o.workers, id)
		o.mu.Unlock()
		o.notifyWaiters(id)
		o.emit(protocol.Event{Kind: protocol.EventSessionDeleted, Session: id})
		return nil
	}
	return o.pig.Delete(ctx, id)
}

func (o *Orchestrator) Spawn(ctx context.Context, req protocol.SpawnRequest) (protocol.Session, error) {
	if w := o.getWorker(req.Parent); w != nil {
		return protocol.Session{}, fmt.Errorf("%s sessions cannot start child conversations", w.snapshot().Provider)
	}
	return o.pig.Spawn(ctx, req)
}

func (o *Orchestrator) Rewind(ctx context.Context, id, msg string) (string, error) {
	if o.getWorker(id) != nil {
		return "", errors.New("rewind is only supported on embedded PiG sessions")
	}
	return o.pig.Rewind(ctx, id, msg)
}

func (o *Orchestrator) Changes(ctx context.Context, id string) ([]protocol.FileChange, error) {
	if w := o.getWorker(id); w != nil {
		info := w.snapshot()
		diff, err := o.worktrees.Diff(ctx, info.Directory)
		if err != nil {
			// Directory may not be a Git repo; report empty diff gracefully.
			return nil, nil
		}
		return diff, nil
	}
	return o.pig.Changes(ctx, id)
}

func (o *Orchestrator) Snapshot(ctx context.Context, id string) (map[string][]byte, error) {
	return o.pig.Snapshot(ctx, id)
}

func (o *Orchestrator) Diff(ctx context.Context, id string, before map[string][]byte) ([]protocol.FileChange, error) {
	return o.pig.Diff(ctx, id, before)
}

func (o *Orchestrator) Models(ctx context.Context) ([]protocol.Model, error) {
	return o.pig.Models(ctx)
}

func (o *Orchestrator) DefaultModel(ctx context.Context) (protocol.ModelRef, error) {
	return o.pig.DefaultModel(ctx)
}

func (o *Orchestrator) SetModel(ctx context.Context, id string, ref protocol.ModelRef) error {
	if w := o.getWorker(id); w != nil {
		return fmt.Errorf("the model of a %s session is chosen by its CLI", w.snapshot().Provider)
	}
	return o.pig.SetModel(ctx, id, ref)
}

func (o *Orchestrator) Modes(ctx context.Context) ([]protocol.Mode, error) {
	return o.pig.Modes(ctx)
}

func (o *Orchestrator) FindFiles(ctx context.Context, dir, query string, limit int) ([]protocol.FileEntry, error) {
	return o.pig.FindFiles(ctx, dir, query, limit)
}

func (o *Orchestrator) ReadFile(ctx context.Context, dir, path string) ([]byte, error) {
	return o.pig.ReadFile(ctx, dir, path)
}

func (o *Orchestrator) FitImage(ctx context.Context, ref protocol.ModelRef, file protocol.File) (int, int, error) {
	return o.pig.FitImage(ctx, ref, file)
}

func (o *Orchestrator) Skills(ctx context.Context, dir string) ([]protocol.Skill, error) {
	return o.pig.Skills(ctx, dir)
}

func (o *Orchestrator) Providers(ctx context.Context) ([]protocol.ProviderInfo, error) {
	return o.pig.Providers(ctx)
}

func (o *Orchestrator) StartProviderLogin(ctx context.Context, req protocol.ProviderLoginStartRequest) (string, error) {
	return o.pig.StartProviderLogin(ctx, req)
}

func (o *Orchestrator) AnswerProviderLogin(ctx context.Context, loginID, value string) error {
	return o.pig.AnswerProviderLogin(ctx, loginID, value)
}

func (o *Orchestrator) CancelProviderLogin(ctx context.Context, loginID string) error {
	return o.pig.CancelProviderLogin(ctx, loginID)
}

func (o *Orchestrator) LogoutProvider(ctx context.Context, providerID string) error {
	return o.pig.LogoutProvider(ctx, providerID)
}

// PendingApprovals lists PiG's pending approvals and the workers'. A worker's
// is shown in its parent conversation, as its approval events are.
func (o *Orchestrator) PendingApprovals(ctx context.Context) ([]protocol.PendingApproval, error) {
	out, err := o.pig.PendingApprovals(ctx)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	workers := make([]*managedWorker, 0, len(o.workers))
	for _, w := range o.workers {
		workers = append(workers, w)
	}
	o.mu.Unlock()
	for _, w := range workers {
		w.mu.Lock()
		shownIn := cmp.Or(w.info.ParentSession, w.info.ID)
		for _, ap := range w.approvals {
			ap.Resources = slices.Clone(ap.Resources)
			out = append(out, protocol.PendingApproval{ShownIn: shownIn, Approval: ap})
		}
		w.mu.Unlock()
	}
	return out, nil
}

func (o *Orchestrator) PendingInteractions(ctx context.Context, id string) ([]protocol.Interaction, error) {
	if o.getWorker(id) != nil {
		return nil, nil
	}
	return o.pig.PendingInteractions(ctx, id)
}

func (o *Orchestrator) AnswerInteraction(ctx context.Context, req protocol.InteractionAnswer) error {
	return o.pig.AnswerInteraction(ctx, req)
}
