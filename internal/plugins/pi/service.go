package pi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/agent"
)

// ---- agent.Service

func (p *Plugin) Sessions(_ context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	c, err := p.current()
	if err != nil {
		return agent.SessionPage{}, err
	}
	c.mu.Lock()
	files, err := c.files.scan()
	c.mu.Unlock()
	if err != nil {
		return agent.SessionPage{}, err
	}
	return page(files, q), nil
}

type piState struct {
	SessionID   string   `json:"sessionId"`
	SessionFile string   `json:"sessionFile"`
	Model       *piModel `json:"model"`
	Thinking    string   `json:"thinkingLevel"`
}

// Create starts a process for a new conversation. Pi writes its session file
// with the first message.
func (p *Plugin) Create(ctx context.Context, req agent.CreateSession) (agent.Session, error) {
	c, err := p.current()
	if err != nil {
		return agent.Session{}, err
	}
	c.mu.Lock()
	l, err := c.spawnLocked("", req.Directory, nil)
	c.mu.Unlock()
	if err != nil {
		return agent.Session{}, err
	}
	var st piState
	if err := l.p.call(ctx, "get_state", nil, &st); err != nil || st.SessionID == "" {
		l.p.close()
		return agent.Session{}, errors.Join(errors.New("pi did not start a session"), err)
	}
	s := agent.Session{ID: agent.SessionID(st.SessionID), Directory: req.Directory}
	if s.Directory == "" {
		s.Directory = c.home
	}
	if st.Model != nil {
		s.Model = agent.ModelRef{Provider: st.Model.Provider, ID: st.Model.ID, Variant: st.Thinking}
	}
	l.t.setSession(s.ID)
	c.mu.Lock()
	c.sessions[s.ID] = l
	c.reapLocked(c.maxIdle)
	c.mu.Unlock()
	if !req.Model.IsZero() {
		if err := setModel(ctx, l.p, req.Model); err != nil {
			return s, err
		}
		s.Model = req.Model
	}
	return s, nil
}

func (p *Plugin) Messages(_ context.Context, id agent.SessionID) ([]agent.Message, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	f, err := c.findLocked(id)
	_, running := c.sessions[id]
	c.mu.Unlock()
	if err != nil {
		if running {
			return nil, nil // a new conversation Pi hasn't written yet
		}
		return nil, err
	}
	_, path, err := readSession(f.path)
	if err != nil {
		return nil, err
	}
	return messagesOf(path), nil
}

func (p *Plugin) Send(ctx context.Context, req agent.Send) (agent.SendDisposition, error) {
	l, err := p.process(req.Session)
	if err != nil {
		return agent.SendAccepted, err
	}
	cmd, err := prompt(req)
	if err != nil {
		return agent.SendAccepted, err
	}
	var receipt struct {
		Disposition string `json:"disposition"`
	}
	if err := l.p.call(ctx, "prompt", cmd, &receipt); err != nil {
		return agent.SendAccepted, err
	}
	switch receipt.Disposition {
	case "started":
		return agent.SendAccepted, nil
	case "queued":
		return agent.SendQueued, nil
	case "handled":
		return agent.SendHandled, nil
	default:
		return agent.SendAccepted, errors.New("pi prompt: unrecognized admission response; check the conversation before sending again")
	}
}

// fileTypes is what prompt can send: images, and text inlined.
type fileTypes struct{}

func (fileTypes) Accepts(mime string) bool {
	return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "text/")
}

func (fileTypes) Summary() string { return "images and text files" }

// prompt is the RPC prompt for a send. Pi takes images; text files join the
// message as fenced blocks; any other file is refused before sending.
func prompt(req agent.Send) (map[string]any, error) {
	text := req.Text
	var images []map[string]string
	for _, f := range req.Files {
		switch {
		case strings.HasPrefix(f.MIME, "image/"):
			images = append(images, map[string]string{"type": "image", "mimeType": f.MIME,
				"data": base64.StdEncoding.EncodeToString(f.Data)})
		case utf8.Valid(f.Data) && !bytes.ContainsRune(f.Data, 0):
			text += "\n\n(file: " + f.Name + ")\n```\n" + strings.TrimRight(string(f.Data), "\n") + "\n```"
		default:
			return nil, fmt.Errorf("%w: Pi takes images and text files, not %s", agent.ErrNotSent, f.Name)
		}
	}
	cmd := map[string]any{"message": text}
	if len(images) > 0 {
		cmd["images"] = images
	}
	return cmd, nil
}

func (p *Plugin) Abort(ctx context.Context, id agent.SessionID) error {
	c, err := p.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	l := c.sessions[id]
	c.mu.Unlock()
	if l == nil {
		return nil // nothing runs without a process
	}
	return l.p.call(ctx, "abort", nil, nil)
}

func (p *Plugin) Reply(context.Context, agent.Reply) error {
	if _, err := p.current(); err != nil {
		return err
	}
	return errors.New("pi uses interactions, not permission approvals")
}

func (p *Plugin) Answer(_ context.Context, answer agent.InteractionAnswer) error {
	c, err := p.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	d, ok := c.dialogs[answer.ID]
	l := c.sessions[d.session]
	if !ok || l == nil || answer.Session != d.session {
		c.mu.Unlock()
		return errors.New("pi is no longer waiting for this answer")
	}
	if !answer.Cancelled && d.request.Kind == agent.Select && !slices.Contains(d.request.Options, answer.Value) {
		c.mu.Unlock()
		return errors.New("the answer is not one of the offered options")
	}
	delete(c.dialogs, answer.ID)
	c.mu.Unlock()
	return l.p.send(dialogReply(string(answer.ID), d.method, answer))
}

func (p *Plugin) Rename(ctx context.Context, id agent.SessionID, title string) error {
	l, err := p.process(id)
	if err != nil {
		return err
	}
	return l.p.call(ctx, "set_session_name", map[string]any{"name": title}, nil)
}

// Delete stops the conversation's process and removes its session file.
func (p *Plugin) Delete(_ context.Context, id agent.SessionID) error {
	c, err := p.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	l := c.sessions[id]
	delete(c.sessions, id)
	f, findErr := c.findLocked(id)
	c.mu.Unlock()
	if l != nil {
		l.p.close()
	}
	if findErr != nil {
		if l != nil {
			return nil // never written
		}
		return findErr
	}
	return os.Remove(f.path)
}

func (p *Plugin) process(id agent.SessionID) (*live, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	return c.ensure(id)
}

// ---- agent.Models

type piModel struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Provider  string             `json:"provider"`
	Reasoning bool               `json:"reasoning"`
	Levels    map[string]*string `json:"thinkingLevelMap"`
	Cost      struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
}

// thinkingLevels lists a model's levels: the common ones unless its map turns
// them off, and xhigh and max only when its map turns them on.
func (m piModel) thinkingLevels() []string {
	if !m.Reasoning {
		return nil
	}
	var out []string
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		v, listed := m.Levels[level]
		common := level != "xhigh" && level != "max"
		if (listed && v != nil) || (!listed && common) {
			out = append(out, level)
		}
	}
	return out
}

func (p *Plugin) Models(ctx context.Context) ([]agent.Model, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	var out struct {
		Models []piModel `json:"models"`
	}
	if err := c.control.call(ctx, "get_available_models", nil, &out); err != nil {
		return nil, err
	}
	models := make([]agent.Model, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, agent.Model{Ref: agent.ModelRef{Provider: m.Provider, ID: m.ID}, Name: m.Name,
			Variants: m.thinkingLevels(), Free: m.Cost.Input == 0 && m.Cost.Output == 0})
	}
	return models, nil
}

func (p *Plugin) DefaultModel(ctx context.Context) (agent.ModelRef, error) {
	c, err := p.current()
	if err != nil {
		return agent.ModelRef{}, err
	}
	var st piState
	if err := c.control.call(ctx, "get_state", nil, &st); err != nil {
		return agent.ModelRef{}, err
	}
	if st.Model == nil {
		return agent.ModelRef{}, errors.New("pi has no model selected")
	}
	return agent.ModelRef{Provider: st.Model.Provider, ID: st.Model.ID}, nil
}

// SetModel switches a conversation's model and thinking level. Pi records the
// change in the session; the confirmation is emitted as ModelChanged.
func (p *Plugin) SetModel(ctx context.Context, id agent.SessionID, m agent.ModelRef) error {
	l, err := p.process(id)
	if err != nil {
		return err
	}
	if err := setModel(ctx, l.p, m); err != nil {
		return err
	}
	c, err := p.current()
	if err == nil {
		c.emit(agent.Event{Kind: agent.ModelChanged, Session: id, Model: m})
	}
	return nil
}

func setModel(ctx context.Context, pr *proc, m agent.ModelRef) error {
	if err := pr.call(ctx, "set_model", map[string]any{"provider": m.Provider, "modelId": m.ID}, nil); err != nil {
		return err
	}
	if m.Variant != "" {
		return pr.call(ctx, "set_thinking_level", map[string]any{"level": m.Variant}, nil)
	}
	return nil
}
