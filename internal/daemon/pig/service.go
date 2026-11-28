package pig

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/protocol"
)

func (r *Runtime) Modes(context.Context) ([]protocol.Mode, error) {
	if _, release, err := r.acquire(); err != nil {
		release()
		return nil, err
	} else {
		release()
	}
	return []protocol.Mode{
		{ID: modeAsk, Name: "Ask before changes", Description: "Read and search code freely; ask before editing files or running shell commands."},
		{ID: modeFull, Name: "Work independently", Description: "Read, edit files and run commands in the selected workspace without individual approvals."},
	}, nil
}

func validMode(mode string) bool { return mode == "" || mode == modeAsk || mode == modeFull }

func (r *Runtime) Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return protocol.Session{}, err
	}
	return e.create(ctx, req, "", nil)
}

func (r *Runtime) Spawn(ctx context.Context, req protocol.SpawnRequest) (protocol.Session, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return protocol.Session{}, err
	}
	if _, err = e.get(req.Parent); err != nil {
		return protocol.Session{}, err
	}
	policy := protocol.ToolPolicy{Allow: slices.Clone(req.Policy.Allow), Deny: slices.Clone(req.Policy.Deny)}
	return e.create(ctx, protocol.CreateSessionRequest{Title: req.Name, Directory: req.Directory, Model: req.Model, Mode: req.Mode}, req.Parent, &policy)
}

func (e *engine) create(ctx context.Context, req protocol.CreateSessionRequest, parent string, policy *protocol.ToolPolicy) (protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Session{}, err
	}
	if !validMode(req.Mode) {
		return protocol.Session{}, errors.New("unknown access mode")
	}
	dir := req.Directory
	if dir == "" {
		dir = filepath.Join(filepath.Dir(e.root), "workspaces", "inbox")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return protocol.Session{}, err
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return protocol.Session{}, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return protocol.Session{}, errors.New("the conversation folder is not available")
	}
	mode := req.Mode
	if mode == "" {
		mode = modeAsk
	}
	c := &conversation{
		rec:     record{Session: protocol.Session{Title: req.Title, Directory: dir, Updated: time.Now().UnixMilli()}, Mode: mode, Parent: parent, Policy: policy, Attachments: map[string]attachment{}},
		allowed: map[string]bool{},
	}
	c.lifetime.RLock()
	defer c.lifetime.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	model := e.resolveModel(req.Model)
	if model != nil {
		c.rec.Session.Model = modelRef(model)
	} else if !req.Model.IsZero() {
		return protocol.Session{}, errors.New("the selected model is not available")
	}
	if err = e.openLocked(c); err != nil {
		return protocol.Session{}, err
	}
	c.rec.Session.ID = c.session.ID()
	c.rec.Path = c.session.Path()
	e.mu.Lock()
	e.sessions[c.rec.Session.ID] = c
	e.mu.Unlock()
	if req.Title != "" {
		if err = c.session.SetName(req.Title); err != nil {
			return c.rec.Session, err
		}
	}
	if req.Model.Variant != "" {
		if err = c.session.SetThinkingLevel(ai.ModelThinkingLevel(req.Model.Variant)); err != nil {
			return c.rec.Session, err
		}
		c.rec.Session.Model.Variant = req.Model.Variant
	}
	err = e.saveLocked(c)
	return c.rec.Session, err
}

func (r *Runtime) Sessions(ctx context.Context, q protocol.SessionQuery) (protocol.SessionPage, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return protocol.SessionPage{}, err
	}
	if err = ctx.Err(); err != nil {
		return protocol.SessionPage{}, err
	}
	e.mu.Lock()
	all := make([]*conversation, 0, len(e.sessions))
	for _, c := range e.sessions {
		all = append(all, c)
	}
	e.mu.Unlock()
	var out []protocol.Session
	for _, c := range all {
		c.mu.Lock()
		rec := c.rec
		c.mu.Unlock()
		if rec.Parent != "" {
			continue
		}
		if q.Directory != "" && filepath.Clean(q.Directory) != rec.Session.Directory {
			continue
		}
		if !strings.Contains(strings.ToLower(rec.Session.Title), strings.ToLower(q.Search)) {
			continue
		}
		out = append(out, rec.Session)
	}
	slices.SortFunc(out, func(a, b protocol.Session) int {
		if a.Updated != b.Updated {
			if a.Updated > b.Updated {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	start := 0
	if q.Cursor != "" {
		start, err = strconv.Atoi(q.Cursor)
		if err != nil || start < 0 || start > len(out) {
			return protocol.SessionPage{}, errors.New("invalid conversation cursor")
		}
	}
	end := min(start+100, len(out))
	page := protocol.SessionPage{Sessions: out[start:end]}
	if end < len(out) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}

func (r *Runtime) Messages(ctx context.Context, id string) ([]protocol.Message, error) {
	e, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = e.openLocked(c); err != nil {
		return nil, err
	}
	return storedMessages(c), nil
}

func (r *Runtime) Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	e, c, release, err := r.acquireConversation(req.Session)
	defer release()
	if err != nil {
		return "", err
	}
	if !validMode(req.Mode) {
		return "", fmt.Errorf("%w: unknown access mode", ErrNotSent)
	}
	text, images, err := promptContent(req)
	if err != nil {
		return "", err
	}

	if req.Deliver == protocol.DeliverInterrupt {
		if err := r.interrupt(ctx, c); err != nil {
			return "", err
		}
	}
	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	if c.busy {
		s := c.session
		c.mu.Unlock()
		return queueInput(ctx, s, req, text, images)
	}
	if err = e.openLocked(c); err != nil {
		c.mu.Unlock()
		return "", err
	}
	if req.Mode != "" {
		c.rec.Mode = req.Mode
	}
	if len(req.Skills) > 0 {
		skills := c.session.ResourceLoader().GetSkills().Skills
		for _, ref := range req.Skills {
			found := false
			for _, sk := range skills {
				if sk.FilePath == ref.ID {
					body, readErr := os.ReadFile(sk.FilePath)
					if readErr != nil {
						c.mu.Unlock()
						return "", fmt.Errorf("%w: could not read skill", ErrNotSent)
					}
					text += "\n\n<skill name=" + strconv.Quote(sk.Name) + ">\n" + string(body) + "\n</skill>"
					found = true
					break
				}
			}
			if !found {
				c.mu.Unlock()
				return "", fmt.Errorf("%w: skill is no longer available", ErrNotSent)
			}
		}
	}
	if c.rec.Attachments == nil {
		c.rec.Attachments = map[string]attachment{}
	}
	if c.rec.Session.Title == "" && len(c.session.UserMessagesForForking()) == 0 {
		title := strings.TrimSpace(strings.SplitN(req.Text, "\n", 2)[0])
		runes := []rune(title)
		if len(runes) > 80 {
			title = string(runes[:80]) + "…"
		}
		if title != "" {
			if err = c.session.SetName(title); err != nil {
				c.mu.Unlock()
				return "", fmt.Errorf("%w: could not name the conversation", ErrNotSent)
			}
			c.rec.Session.Title = title
		}
	}
	index := strconv.Itoa(len(c.session.UserMessagesForForking()))
	files := slices.Clone(req.Files)
	for i := range files {
		files[i].Data = nil
	}
	c.rec.Attachments[index] = attachment{Text: req.Text, Files: files}
	if err = e.saveLocked(c); err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("%w: could not save prompt metadata", ErrNotSent)
	}
	turn, cancel := context.WithCancel(e.ctx)
	c.cancel = cancel
	c.busy = true
	c.lifetime.RLock()
	s := c.session
	bind := !c.bound
	c.mu.Unlock()
	type receipt struct {
		disposition protocol.SendDisposition
		err         error
	}
	admitted := make(chan receipt, 1)
	if !e.spawn(func(context.Context) {
		defer func() { c.lifetime.RUnlock(); e.trimIdle() }()
		defer cancel()
		c.translator.mu.Lock()
		c.translator.managed = true
		c.translator.mu.Unlock()
		var started atomic.Bool
		opts := &coding.PromptOptions{Images: images, PreflightResult: func(d coding.PromptDisposition) {
			result := protocol.SendAccepted
			if d == coding.DispositionStarted {
				started.Store(true)
			}
			switch d {
			case coding.DispositionStarted:
				result = protocol.SendAccepted
			case coding.DispositionQueued:
				result = protocol.SendQueued
			case coding.DispositionHandled:
				result = protocol.SendHandled
			}
			select {
			case admitted <- receipt{disposition: result}:
			default:
			}
		}}
		var runErr error
		if bind {
			runErr = s.BindExtensions(turn, coding.ExtensionBindings{UIContext: &dialogUI{UIContext: noopUI(), engine: e, conversation: c}})
			if runErr == nil {
				c.mu.Lock()
				c.bound = true
				c.mu.Unlock()
			}
		}
		if runErr == nil {
			runErr = s.Prompt(turn, text, opts)
		}
		select {
		case admitted <- receipt{disposition: protocol.SendHandled, err: runErr}:
		default:
		}
		c.ops.Lock()
		c.mu.Lock()
		c.busy = false
		c.cancel = nil
		c.rec.Session.Updated = time.Now().UnixMilli()
		c.rec.Session.Model = sessionModel(s)
		c.rec.Session.Cost = messageCost(s.Messages())
		c.rec.HasMessages = len(s.UserMessagesForForking()) > 0
		saveErr := e.saveLocked(c)
		c.mu.Unlock()
		e.finishTurn(c, started.Load(), turn.Err() != nil, runErr, saveErr)
		c.ops.Unlock()
	}) {
		c.mu.Lock()
		c.busy = false
		c.cancel = nil
		c.mu.Unlock()
		cancel()
		c.lifetime.RUnlock()
		return "", ErrNotConnected
	}
	select {
	case rec := <-admitted:
		return rec.disposition, rec.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-e.ctx.Done():
		return "", ErrNotConnected
	}
}

func promptContent(req protocol.SendRequest) (string, []ai.ImageContent, error) {
	text := req.Text
	var images []ai.ImageContent
	for _, f := range req.Files {
		switch {
		case strings.HasPrefix(f.MIME, "image/") && acceptsMIME(f.MIME):
			images = append(images, ai.ImageContent{Data: base64.StdEncoding.EncodeToString(f.Data), MimeType: f.MIME})
		case strings.HasPrefix(f.MIME, "text/"):
			text += "\n\n(file: " + filepath.Base(f.Name) + ")\n```\n" + string(f.Data) + "\n```"
		default:
			return "", nil, fmt.Errorf("%w: PiG takes images and text files, not %s", ErrNotSent, f.Name)
		}
	}
	return text, images, nil
}

func (r *Runtime) Abort(ctx context.Context, id string) error {
	_, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.session != nil {
		c.session.RequestAbort()
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	return nil
}

func (r *Runtime) Rename(ctx context.Context, id, title string) error {
	e, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return err
	}
	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	if err = ctx.Err(); err == nil {
		err = e.openLocked(c)
	}
	session := c.session
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err = session.SetName(title); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.Session.Title = title
	c.rec.Session.Updated = time.Now().UnixMilli()
	return e.saveLocked(c)
}

func (r *Runtime) Delete(ctx context.Context, id string) error {
	e, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return err
	}
	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	if err = ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.busy {
		c.mu.Unlock()
		return errors.New("stop the conversation before deleting it")
	}
	c.deleted = true
	path := c.rec.Path
	c.mu.Unlock()
	// The metadata is what lists the conversation, so it goes first: once it
	// is gone the conversation is deleted even if its transcript lingers.
	if err = os.Remove(filepath.Join(e.root, "metadata", id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.mu.Lock()
		c.deleted = false
		c.mu.Unlock()
		return err
	}
	e.forget(c)
	c.closeResources()
	if path != "" {
		_ = os.Remove(path)
	}
	e.removeBaselines(id)
	e.mu.Lock()
	delete(e.sessions, id)
	e.mu.Unlock()
	e.emit(protocol.Event{Kind: protocol.EventSessionDeleted, Session: id})
	return nil
}

func (r *Runtime) Models(ctx context.Context) ([]protocol.Model, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	available, err := e.models.GetAllAvailable(ctx)
	if err != nil {
		return nil, err
	}
	var out []protocol.Model
	for _, entry := range available {
		m, ok := entry.(*ai.Model)
		if !ok {
			continue
		}
		out = append(out, normalizeModel(m))
	}
	return out, nil
}

func (r *Runtime) DefaultModel(ctx context.Context) (protocol.ModelRef, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return protocol.ModelRef{}, err
	}
	if err = ctx.Err(); err != nil {
		return protocol.ModelRef{}, err
	}
	if m := e.resolveModel(protocol.ModelRef{}); m != nil {
		return modelRef(m), nil
	}
	return protocol.ModelRef{}, nil
}

func (r *Runtime) SetModel(ctx context.Context, id string, ref protocol.ModelRef) error {
	e, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return err
	}
	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return errors.New("stop the turn before changing its model")
	}
	if err = ctx.Err(); err == nil {
		err = e.openLocked(c)
	}
	s := c.session
	c.mu.Unlock()
	if err != nil {
		return err
	}
	m := e.resolveModel(ref)
	if m == nil {
		return errors.New("the selected model is not available")
	}
	if err = s.SetModel(m); err != nil {
		return err
	}
	if ref.Variant != "" {
		if err = s.SetThinkingLevel(ai.ModelThinkingLevel(ref.Variant)); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.Session.Model = sessionModel(s)
	err = e.saveLocked(c)
	e.emit(protocol.Event{Kind: protocol.EventModelChanged, Session: id, Model: c.rec.Session.Model})
	return err
}

func (r *Runtime) Rewind(ctx context.Context, id, msg string) (string, error) {
	e, c, release, err := r.acquireConversation(id)
	defer release()
	if err != nil {
		return "", err
	}
	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy {
		return "", errors.New("stop the turn before editing its history")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = e.openLocked(c); err != nil {
		return "", err
	}
	copyRec := c.rec
	copyRec.Attachments = maps.Clone(c.rec.Attachments)
	copyRec.Path = c.session.Path()
	fork := &conversation{rec: copyRec, allowed: map[string]bool{}}
	fork.lifetime.RLock()
	defer fork.lifetime.RUnlock()
	if err = e.openLocked(fork); err != nil {
		return "", err
	}
	if err = fork.session.ForkToNewSession(msg); err != nil {
		e.forget(fork)
		fork.closeResources()
		return "", err
	}
	fork.rec.Session.ID = fork.session.ID()
	fork.rec.Path = fork.session.Path()
	fork.rec.HasMessages = len(fork.session.UserMessagesForForking()) > 0
	fork.rec.Session.Updated = time.Now().UnixMilli()
	fork.translator.mu.Lock()
	fork.translator.session = fork.rec.Session.ID
	fork.translator.mu.Unlock()
	if err = e.copyBaselines(c.rec.Session.ID, fork.rec.Session.ID); err != nil {
		e.forget(fork)
		fork.closeResources()
		return "", err
	}
	e.mu.Lock()
	e.sessions[fork.rec.Session.ID] = fork
	e.mu.Unlock()
	return fork.rec.Session.ID, e.saveLocked(fork)
}

// Generate asks the default model for a short piece of text outside any
// conversation, such as a branch name or a commit message.
func (r *Runtime) Generate(ctx context.Context, system, prompt string) (string, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return "", err
	}
	model := e.resolveModel(protocol.ModelRef{})
	if model == nil {
		return "", errors.New("pig: no model is available")
	}
	// Some providers route by session; each request is a session of its own.
	opts := ai.StreamOptions{SessionID: "wada-generate-" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	msg := e.models.CompleteSimple(ctx, model, ai.Context{SystemPrompt: system,
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(prompt), Timestamp: time.Now().UnixMilli()}}}, opts)
	if msg == nil {
		return "", errors.New("pig: the model returned nothing")
	}
	if msg.ErrorMessage != "" {
		return "", errors.New(msg.ErrorMessage)
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.(ai.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// queueInput delivers a message to a running turn: steered into it, or
// queued as the next turn. Files and skills go only with a turn of their
// own.
func queueInput(ctx context.Context, s *coding.Session, req protocol.SendRequest, text string, images []ai.ImageContent) (protocol.SendDisposition, error) {
	if req.Deliver != protocol.DeliverSteer && req.Deliver != protocol.DeliverQueue {
		return "", fmt.Errorf("%w: this conversation is already running", ErrNotSent)
	}
	if len(req.Files) > 0 || len(req.Skills) > 0 || s == nil {
		return "", fmt.Errorf("%w: a message for a running turn takes text only", ErrNotSent)
	}
	var d coding.QueuedInputDisposition
	var err error
	if req.Deliver == protocol.DeliverSteer {
		d, err = s.Steer(ctx, text, images, nil)
	} else {
		d, err = s.FollowUp(ctx, text, images, nil)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotSent, err)
	}
	if d == coding.DispositionHandled {
		return protocol.SendHandled, nil
	}
	return protocol.SendQueued, nil
}

// interruptWait bounds how long an interrupting send waits for the turn to
// stop.
const interruptWait = 15 * time.Second

// interrupt stops a running turn and waits for it to end.
func (r *Runtime) interrupt(ctx context.Context, c *conversation) error {
	c.mu.Lock()
	busy := c.busy
	if busy {
		if c.session != nil {
			c.session.RequestAbort()
		}
		if c.cancel != nil {
			c.cancel()
		}
	}
	c.mu.Unlock()
	deadline := time.Now().Add(interruptWait)
	for busy {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		c.mu.Lock()
		busy = c.busy
		c.mu.Unlock()
		if busy && time.Now().After(deadline) {
			return fmt.Errorf("%w: the running turn didn't stop", ErrNotSent)
		}
	}
	return nil
}
