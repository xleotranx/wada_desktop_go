package pig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/contract/agent"
)

type record struct {
	Session      agent.Session
	Path         string
	HasMessages  bool
	ChangesError string
	Mode         string
	Parent       agent.SessionID
	Policy       *agent.ToolPolicy
	Attachments  map[string]attachment
	Baselines    map[string]fileSnapshot
}
type attachment struct {
	Internal bool
	Text     string
	Files    []agent.File
}
type conversation struct {
	turnMu     sync.Mutex
	turn       *turnState
	lifetime   sync.RWMutex // readers pin service calls and turns against idle eviction
	ops        sync.Mutex   // serializes service mutations without blocking event callbacks
	captureMu  sync.Mutex
	capture    *mutationCapture
	mu         sync.Mutex // lifecycle and metadata, never held while a turn runs
	rec        record
	session    *coding.Session
	services   *coding.AgentSessionServices
	runtime    *coding.Runtime
	cancel     context.CancelFunc
	busy       bool
	deleted    bool
	bound      bool // extension startup completed for this SDK session
	pending    int  // extension dialogs can outlive the service call that opened them
	off        func()
	allowed    map[string]bool
	translator translator
}

var storageLocks sync.Map // storage root -> *sync.Mutex, shared across activations

type engine struct {
	liveMu        sync.Mutex
	live          []*conversation // live SDK owners, most recently used first
	storageLock   *sync.Mutex
	active        sync.WaitGroup
	turns         sync.WaitGroup
	ctx           context.Context
	root          string
	spawn         func(func(context.Context)) bool
	models        *coding.ModelRuntime
	services      *coding.AgentSessionServices
	events        chan agent.Event
	modelsChanged func()
	mu            sync.Mutex // protects records and pending requests
	sessions      map[agent.SessionID]*conversation
	approvals     map[agent.ApprovalID]*approvalWait
	dialogs       map[agent.InteractionID]*interactionWait
	sequence      uint64
	mutation      sync.Mutex // makes baseline capture + execution attribution ordered across sessions
}

func newEngine(ctx context.Context, root string, spawn func(func(context.Context)) bool) (*engine, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	lock, _ := storageLocks.LoadOrStore(root, new(sync.Mutex))
	storageLock := lock.(*sync.Mutex)
	storageLock.Lock()
	succeeded := false
	defer func() {
		if !succeeded {
			storageLock.Unlock()
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(root, "metadata"), 0700); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: home, AgentDir: filepath.Join(root, "agent"), ModelRuntimeSignal: ctx})
	if err != nil {
		return nil, err
	}
	e := &engine{storageLock: storageLock, ctx: ctx, root: root, spawn: spawn, models: svcs.ModelRuntime(), services: svcs, events: make(chan agent.Event, 64), sessions: map[agent.SessionID]*conversation{}, approvals: map[agent.ApprovalID]*approvalWait{}, dialogs: map[agent.InteractionID]*interactionWait{}}
	entries, err := os.ReadDir(filepath.Join(root, "metadata"))
	if err != nil {
		svcs.Close()
		return nil, err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "metadata", entry.Name()))
		if err != nil {
			svcs.Close()
			return nil, err
		}
		var r record
		if err = json.Unmarshal(data, &r); err != nil {
			svcs.Close()
			return nil, fmt.Errorf("invalid PiG conversation metadata: %s", entry.Name())
		}
		if !validID(r.Session.ID) || entry.Name() != string(r.Session.ID)+".json" {
			svcs.Close()
			return nil, errors.New("invalid PiG session identity")
		}
		if !validMode(r.Mode) || (r.Parent != "" && !validID(r.Parent)) {
			svcs.Close()
			return nil, errors.New("invalid PiG session metadata")
		}
		if r.Path != "" {
			rel, pathErr := filepath.Rel(filepath.Join(root, "sessions"), r.Path)
			if pathErr != nil || !filepath.IsLocal(rel) {
				svcs.Close()
				return nil, errors.New("the transcript path leaves PiG storage")
			}
		}
		e.sessions[r.Session.ID] = &conversation{rec: r, allowed: map[string]bool{}}
	}
	e.spawn = func(work func(context.Context)) bool {
		e.mu.Lock()
		if e.ctx.Err() != nil {
			e.mu.Unlock()
			return false
		}
		e.turns.Add(1)
		e.mu.Unlock()
		if !spawn(func(ctx context.Context) { defer e.turns.Done(); work(ctx) }) {
			e.turns.Done()
			return false
		}
		return true
	}
	succeeded = true
	return e, nil
}
func validID(id agent.SessionID) bool {
	s := string(id)
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !filepath.IsAbs(s)
}
func (e *engine) emit(ev agent.Event) {
	if ev.Bytes == 0 {
		ev.Bytes = len(ev.Error) + len(ev.Title) + 128
		if ev.Text != nil {
			ev.Bytes += len(ev.Text.Delta) + len(ev.Text.Text)
		}
	}
	select {
	case e.events <- ev:
	case <-e.ctx.Done():
	}
}
func (e *engine) close() {
	defer e.storageLock.Unlock()
	e.active.Wait()
	e.turns.Wait()
	e.mu.Lock()
	all := make([]*conversation, 0, len(e.sessions))
	for _, c := range e.sessions {
		all = append(all, c)
	}
	e.mu.Unlock()
	for _, c := range all {
		c.closeResources()
	}
	e.live = nil
	e.services.Close()
}
func (e *engine) get(id agent.SessionID) (*conversation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.sessions[id]
	if c == nil {
		return nil, errors.New("the PiG conversation was not found")
	}
	return c, nil
}

// saveLocked commits only Wada metadata. PiG owns and writes its transcript.
func (e *engine) saveLocked(c *conversation) error {
	data, err := json.Marshal(c.rec)
	if err != nil {
		return err
	}
	name := filepath.Join(e.root, "metadata", string(c.rec.Session.ID)+".json")
	f, err := os.CreateTemp(filepath.Dir(name), ".conversation-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, name)
}

// openLocked constructs directory-specific collaborators. Call with c.mu held.
func (e *engine) openLocked(c *conversation) error {
	if c.deleted {
		return errors.New("the conversation was deleted")
	}
	if c.session != nil {
		e.touchLocked(c)
		return nil
	}
	svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: c.rec.Session.Directory, AgentDir: filepath.Join(e.root, "agent"), ModelRuntime: e.models})
	if err != nil {
		return err
	}
	// Wada owns user-authorized retries; disable the SDK's automatic turn retries.
	_ = svcs.SettingsManager().SetRetryEnabled(false)
	rt, err := coding.NewRuntime(coding.RuntimeOptions{Services: svcs, AbortContext: e.ctx})
	if err != nil {
		svcs.Close()
		return err
	}
	model := e.models.GetModel(c.rec.Session.Model.Provider, c.rec.Session.Model.ID)
	opts := coding.SessionStartOptions{Model: model, ThinkingLevel: ai.ModelThinkingLevel(c.rec.Session.Model.Variant), SessionID: string(c.rec.Session.ID), SessionDir: filepath.Join(e.root, "sessions"), BeforeToolCall: []pa.BeforeToolCallHook{e.toolHook(c)}, ExtraTools: []pa.AgentTool{&resultTool{c: c}}}
	var s *coding.Session
	if c.rec.Path != "" {
		if _, err = os.Stat(c.rec.Path); err == nil {
			s, err = rt.Open(c.rec.Path, opts)
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = rt.Close()
			svcs.Close()
			return err
		} else {
			if c.rec.HasMessages {
				_ = rt.Close()
				svcs.Close()
				return errors.New("the PiG transcript is missing")
			}
			err = nil
		}
	}
	if s == nil && err == nil {
		s, err = rt.New(opts)
	}
	if s == nil && errors.Is(err, os.ErrNotExist) {
		err = errors.New("the PiG transcript is missing")
	}
	if err != nil {
		_ = rt.Close()
		svcs.Close()
		return err
	}
	if c.rec.Session.ID != "" && string(c.rec.Session.ID) != s.ID() {
		_ = s.Close()
		_ = rt.Close()
		svcs.Close()
		return errors.New("the transcript belongs to another conversation")
	}
	s.SetActiveToolsByName(withoutResult(s.ActiveToolNames()))
	s.Agent().SetToolExecution(pa.ToolModeSequential)
	c.rec.Path = s.Path()
	c.session, c.services, c.runtime = s, svcs, rt
	c.translator.session = agent.SessionID(s.ID())
	s.DiscardEvents()
	c.off = s.Subscribe(func(ev pa.AgentEvent) { e.onEvent(c, ev) })
	e.touchLocked(c)
	return nil
}

// resolveModel also permits an unconfigured catalog: users can create chats before login.
func (e *engine) resolveModel(ref agent.ModelRef) *ai.Model {
	if !ref.IsZero() {
		return e.models.GetModel(ref.Provider, ref.ID)
	}
	settings := e.services.Settings()
	if m := e.models.GetModel(settings.DefaultProvider, settings.DefaultModel); m != nil {
		return m
	}
	models, err := e.models.GetAllAvailable(e.ctx)
	if err == nil {
		for _, m := range models {
			if chat, ok := m.(*ai.Model); ok {
				return chat
			}
		}
	}
	return nil
}
