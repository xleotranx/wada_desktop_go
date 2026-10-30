package pig

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

func (p *Plugin) Modes(context.Context) ([]agent.Mode, error) {
	if _, err := p.current(); err != nil {
		return nil, err
	}
	return []agent.Mode{{ID: modeAsk, Name: "Ask before write", Description: "Ask before edits, shell commands, web access, and unknown tools."}, {ID: modeFull, Name: "Full access", Description: "Run ordinary chat tools without approval."}}, nil
}
func validMode(mode string) bool { return mode == "" || mode == modeAsk || mode == modeFull }
func (p *Plugin) Create(ctx context.Context, req agent.CreateSession) (agent.Session, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return agent.Session{}, err
	}
	return e.create(ctx, req, "", nil)
}
func (p *Plugin) Spawn(ctx context.Context, req agent.Spawn) (agent.Session, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return agent.Session{}, err
	}
	if _, err = e.get(req.Parent); err != nil {
		return agent.Session{}, err
	}
	policy := agent.ToolPolicy{Allow: slices.Clone(req.Policy.Allow), Deny: slices.Clone(req.Policy.Deny)}
	return e.create(ctx, agent.CreateSession{Title: req.Name, Directory: req.Directory, Model: req.Model, Mode: req.Mode}, req.Parent, &policy)
}
func (e *engine) create(ctx context.Context, req agent.CreateSession, parent agent.SessionID, policy *agent.ToolPolicy) (agent.Session, error) {
	if err := ctx.Err(); err != nil {
		return agent.Session{}, err
	}
	if !validMode(req.Mode) {
		return agent.Session{}, errors.New("unknown access mode")
	}
	dir := req.Directory
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return agent.Session{}, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return agent.Session{}, errors.New("the conversation folder is not available")
	}
	mode := req.Mode
	if mode == "" {
		mode = modeAsk
	}
	c := &conversation{rec: record{Session: agent.Session{Title: req.Title, Directory: dir, Updated: time.Now().UnixMilli()}, Mode: mode, Parent: parent, Policy: policy, Attachments: map[string]attachment{}, Baselines: map[string]fileSnapshot{}}, allowed: map[string]bool{}}
	c.lifetime.RLock()
	defer c.lifetime.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	model := e.resolveModel(req.Model)
	if model != nil {
		c.rec.Session.Model = modelRef(model)
	} else if !req.Model.IsZero() {
		return agent.Session{}, errors.New("the selected model is not available")
	}
	if err = e.openLocked(c); err != nil {
		return agent.Session{}, err
	}
	c.rec.Session.ID = agent.SessionID(c.session.ID())
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
func (p *Plugin) Sessions(ctx context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return agent.SessionPage{}, err
	}
	if err = ctx.Err(); err != nil {
		return agent.SessionPage{}, err
	}
	e.mu.Lock()
	all := make([]*conversation, 0, len(e.sessions))
	for _, c := range e.sessions {
		all = append(all, c)
	}
	e.mu.Unlock()
	var out []agent.Session
	for _, c := range all {
		c.mu.Lock()
		r := c.rec
		c.mu.Unlock()
		if r.Parent != "" {
			continue
		}
		if q.Directory != "" && filepath.Clean(q.Directory) != r.Session.Directory {
			continue
		}
		if !strings.Contains(strings.ToLower(r.Session.Title), strings.ToLower(q.Search)) {
			continue
		}
		out = append(out, r.Session)
	}
	slices.SortFunc(out, func(a, b agent.Session) int {
		if a.Updated != b.Updated {
			if a.Updated > b.Updated {
				return -1
			}
			return 1
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	start := 0
	if q.Cursor != "" {
		start, err = strconv.Atoi(q.Cursor)
		if err != nil || start < 0 || start > len(out) {
			return agent.SessionPage{}, errors.New("invalid conversation cursor")
		}
	}
	end := min(start+100, len(out))
	page := agent.SessionPage{Sessions: out[start:end]}
	if end < len(out) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}
func (p *Plugin) Messages(ctx context.Context, id agent.SessionID) ([]agent.Message, error) {
	e, c, release, err := p.acquireConversation(id)
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
func (p *Plugin) Send(ctx context.Context, req agent.Send) (agent.SendDisposition, error) {
	e, c, release, err := p.acquireConversation(req.Session)
	defer release()
	if err != nil {
		return 0, err
	}
	if !validMode(req.Mode) {
		return 0, fmt.Errorf("%w: unknown access mode", agent.ErrNotSent)
	}
	if len(req.ResultSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(req.ResultSchema, &schema); err != nil || schema["type"] != "object" {
			return 0, fmt.Errorf("%w: result schema must describe an object", agent.ErrNotSent)
		}
		req.ResultSchema = slices.Clone(req.ResultSchema)
	}
	if req.Policy != nil {
		copy := agent.ToolPolicy{Allow: slices.Clone(req.Policy.Allow), Deny: slices.Clone(req.Policy.Deny)}
		req.Policy = &copy
	}
	text, images, err := promptContent(req)
	if err != nil {
		return 0, err
	}

	c.ops.Lock()
	defer c.ops.Unlock()
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return 0, fmt.Errorf("%w: this conversation is already running", agent.ErrNotSent)
	}
	if err = e.openLocked(c); err != nil {
		c.mu.Unlock()
		return 0, err
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
						return 0, fmt.Errorf("%w: could not read skill", agent.ErrNotSent)
					}
					text += "\n\n<skill name=" + strconv.Quote(sk.Name) + ">\n" + string(body) + "\n</skill>"
					found = true
					break
				}
			}
			if !found {
				c.mu.Unlock()
				return 0, fmt.Errorf("%w: skill is no longer available", agent.ErrNotSent)
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
				return 0, fmt.Errorf("%w: could not name the conversation", agent.ErrNotSent)
			}
			c.rec.Session.Title = title
		}
	}
	index := strconv.Itoa(len(c.session.UserMessagesForForking()))
	files := slices.Clone(req.Files)
	for i := range files {
		files[i].Data = nil
	}
	c.rec.Attachments[index] = attachment{Text: req.Text, Files: files, Internal: req.Internal}
	if err = e.saveLocked(c); err != nil {
		c.mu.Unlock()
		return 0, fmt.Errorf("%w: could not save prompt metadata", agent.ErrNotSent)
	}
	turn, cancel := context.WithCancel(e.ctx)
	c.cancel = cancel
	c.busy = true
	c.turnMu.Lock()
	c.turn = &turnState{id: req.ID, schema: slices.Clone(req.ResultSchema), policy: req.Policy}
	c.turnMu.Unlock()
	c.lifetime.RLock()
	s := c.session
	bind := !c.bound
	c.mu.Unlock()
	type receipt struct {
		disposition agent.SendDisposition
		err         error
	}
	admitted := make(chan receipt, 1)
	if !e.spawn(func(context.Context) {
		defer func() { c.lifetime.RUnlock(); e.trimIdle() }()
		defer cancel()
		c.translator.mu.Lock()
		c.translator.managed = true
		c.translator.request = req.ID
		c.translator.mu.Unlock()
		var started atomic.Bool
		opts := &coding.PromptOptions{Images: images, PreflightResult: func(d coding.PromptDisposition) {
			result := agent.SendAccepted
			if d == coding.DispositionStarted {
				started.Store(true)
			}
			switch d {
			case coding.DispositionQueued:
				result = agent.SendQueued
			case coding.DispositionHandled:
				result = agent.SendHandled
			}
			select {
			case admitted <- receipt{disposition: result}:
			default:
			}
		}}
		names := withoutResult(s.ActiveToolNames())
		if len(req.ResultSchema) > 0 {
			names = append(names, resultToolName)
		}
		s.SetActiveToolsByName(names)
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
		case admitted <- receipt{disposition: agent.SendHandled, err: runErr}:
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
		return 0, agent.ErrNotConnected
	}
	select {
	case r := <-admitted:
		return r.disposition, r.err
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-e.ctx.Done():
		return 0, agent.ErrNotConnected
	}
}
func promptContent(req agent.Send) (string, []ai.ImageContent, error) {
	text := req.Text
	if req.Instructions != "" {
		text += "\n\n<turn_instructions>\nThese instructions apply only to this turn, not to later messages.\n" + req.Instructions + "\n</turn_instructions>"
	}
	if len(req.ResultSchema) > 0 {
		text += "\nBefore ending this turn, call submit_result exactly once with the requested structured result. Write any user-facing explanation before calling it."
	}
	var images []ai.ImageContent
	for _, f := range req.Files {
		switch {
		case strings.HasPrefix(f.MIME, "image/") && (fileTypes{}).Accepts(f.MIME):
			images = append(images, ai.ImageContent{Data: base64.StdEncoding.EncodeToString(f.Data), MimeType: f.MIME})
		case strings.HasPrefix(f.MIME, "text/"):
			text += "\n\n(file: " + filepath.Base(f.Name) + ")\n```\n" + string(f.Data) + "\n```"
		default:
			return "", nil, fmt.Errorf("%w: PiG takes images and text files, not %s", agent.ErrNotSent, f.Name)
		}
	}
	return text, images, nil
}
func (p *Plugin) Abort(ctx context.Context, id agent.SessionID) error {
	_, c, release, err := p.acquireConversation(id)
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
func (p *Plugin) Rename(ctx context.Context, id agent.SessionID, title string) error {
	e, c, release, err := p.acquireConversation(id)
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
	// SDK notifications execute synchronously. Never hold the metadata lock
	// while publishing them: a tool-result listener may need that same lock.
	if err = session.SetName(title); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.Session.Title = title
	c.rec.Session.Updated = time.Now().UnixMilli()
	return e.saveLocked(c)
}

func (p *Plugin) Delete(ctx context.Context, id agent.SessionID) error {
	e, c, release, err := p.acquireConversation(id)
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
	e.forget(c)
	c.closeResources()
	if path != "" {
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err = os.Remove(filepath.Join(e.root, "metadata", string(id)+".json")); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.sessions, id)
	e.mu.Unlock()
	p.ctx.Post(func() { kernel.Emit(p.ctx, agent.Deleted, id) })
	return nil
}
func (p *Plugin) Models(ctx context.Context) ([]agent.Model, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	available, err := e.models.GetAllAvailable(ctx)
	if err != nil {
		return nil, err
	}
	var out []agent.Model
	for _, entry := range available {
		m, ok := entry.(*ai.Model)
		if !ok {
			continue
		}
		out = append(out, normalizeModel(m))
	}
	return out, nil
}
func (p *Plugin) DefaultModel(ctx context.Context) (agent.ModelRef, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return agent.ModelRef{}, err
	}
	if err = ctx.Err(); err != nil {
		return agent.ModelRef{}, err
	}
	if m := e.resolveModel(agent.ModelRef{}); m != nil {
		return modelRef(m), nil
	}
	return agent.ModelRef{}, nil
}
func (p *Plugin) SetModel(ctx context.Context, id agent.SessionID, ref agent.ModelRef) error {
	e, c, release, err := p.acquireConversation(id)
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
	// Model and thinking-level notifications can open dialogs that read metadata.
	// Keep mutations serialized without holding the lock those callbacks need.
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
	e.emit(agent.Event{Kind: agent.ModelChanged, Session: id, Model: c.rec.Session.Model})
	return err
}
func (p *Plugin) Rewind(ctx context.Context, id agent.SessionID, msg agent.MessageID) (agent.SessionID, error) {
	e, c, release, err := p.acquireConversation(id)
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
	// Open a separate owner so the original session and its resources remain intact.
	copyRec := c.rec
	copyRec.Attachments = maps.Clone(c.rec.Attachments)
	copyRec.Baselines = maps.Clone(c.rec.Baselines)
	copyRec.Path = c.session.Path()
	fork := &conversation{rec: copyRec, allowed: map[string]bool{}}
	fork.lifetime.RLock()
	defer fork.lifetime.RUnlock()
	if err = e.openLocked(fork); err != nil {
		return "", err
	}
	if err = fork.session.ForkToNewSession(string(msg)); err != nil {
		e.forget(fork)
		fork.closeResources()
		return "", err
	}
	fork.rec.Session.ID = agent.SessionID(fork.session.ID())
	fork.rec.Path = fork.session.Path()
	fork.rec.HasMessages = len(fork.session.UserMessagesForForking()) > 0
	fork.rec.Session.Updated = time.Now().UnixMilli()
	fork.translator.mu.Lock()
	fork.translator.session = fork.rec.Session.ID
	fork.translator.mu.Unlock()
	e.mu.Lock()
	e.sessions[fork.rec.Session.ID] = fork
	e.mu.Unlock()
	return fork.rec.Session.ID, e.saveLocked(fork)
}
