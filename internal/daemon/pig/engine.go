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
	_ "github.com/MichaelKinsy/PiG/ai/all"
	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/protocol"
)

type record struct {
	Session      protocol.Session
	Path         string
	HasMessages  bool
	ChangesError string
	Mode         string
	Parent       string
	Policy       *protocol.ToolPolicy
	Attachments  map[string]attachment
	Baselines    map[string]fileSnapshot `json:"Baselines,omitempty"` // legacy inline migration only
}

type attachment struct {
	Internal bool
	Text     string
	Files    []protocol.File
}

type conversation struct {
	turnMu     sync.Mutex
	turn       *turnState
	lifetime   sync.RWMutex
	ops        sync.Mutex
	captureMu  sync.Mutex
	capture    *mutationCapture
	mu         sync.Mutex
	rec        record
	session    *coding.Session
	services   *coding.AgentSessionServices
	runtime    *coding.Runtime
	loader     *coding.DefaultResourceLoader
	cancel     context.CancelFunc
	busy       bool
	deleted    bool
	bound      bool
	pending    int
	off        func()
	allowed    map[string]bool
	translator translator
}

var storageLocks sync.Map // storage root -> *sync.Mutex

type engine struct {
	liveMu        sync.Mutex
	live          []*conversation
	storageLock   *sync.Mutex
	active        sync.WaitGroup
	turns         sync.WaitGroup
	ctx           context.Context
	root          string
	spawn         func(func(context.Context)) bool
	models        *coding.ModelRuntime
	offModels     func()
	services      *coding.AgentSessionServices
	events        chan protocol.Event
	instructions  string
	modelsChanged func()
	projectTool   func(context.Context, string) (ProjectTool, error)
	chiefTools    func(contextFn func() (sessionID, dir string)) []pa.AgentTool
	mu            sync.Mutex
	sessions      map[string]*conversation
	approvals     map[string]*approvalWait
	dialogs       map[string]*interactionWait
	sequence      uint64
	loginsMu      sync.Mutex
	logins        map[string]*providerLogin
	dirLocksMu    sync.Mutex
	dirLocks      map[string]*sync.Mutex
}

func (e *engine) mutationLock(dir string) *sync.Mutex {
	key := filepath.Clean(dir)
	e.dirLocksMu.Lock()
	defer e.dirLocksMu.Unlock()
	if e.dirLocks == nil {
		e.dirLocks = map[string]*sync.Mutex{}
	}
	l := e.dirLocks[key]
	if l == nil {
		l = &sync.Mutex{}
		e.dirLocks[key] = l
	}
	return l
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
	if err = os.MkdirAll(filepath.Join(root, "metadata"), 0o700); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(root, "baselines"), 0o700); err != nil {
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
	e := &engine{
		storageLock: storageLock,
		ctx:         ctx,
		root:        root,
		spawn:       spawn,
		models:      svcs.ModelRuntime(),
		services:    svcs,
		events:      make(chan protocol.Event, 256),
		sessions:    map[string]*conversation{},
		approvals:   map[string]*approvalWait{},
		dialogs:     map[string]*interactionWait{},
		logins:      map[string]*providerLogin{},
		dirLocks:    map[string]*sync.Mutex{},
	}
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
		if !validID(r.Session.ID) || entry.Name() != r.Session.ID+".json" {
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
		conv := &conversation{rec: r, allowed: map[string]bool{}}
		if len(conv.rec.Baselines) > 0 {
			existing, bErr := e.loadBaselines(conv.rec.Session.ID)
			if bErr != nil {
				svcs.Close()
				return nil, bErr
			}
			for k, v := range conv.rec.Baselines {
				if _, ok := existing[k]; !ok {
					existing[k] = v
				}
			}
			if bErr = e.saveBaselines(conv.rec.Session.ID, existing); bErr != nil {
				svcs.Close()
				return nil, bErr
			}
			conv.rec.Baselines = nil
			if bErr = e.saveLocked(conv); bErr != nil {
				svcs.Close()
				return nil, bErr
			}
		}
		e.sessions[r.Session.ID] = conv
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

func validID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !filepath.IsAbs(id)
}

func (e *engine) emit(ev protocol.Event) {
	select {
	case e.events <- ev:
	case <-e.ctx.Done():
	}
}

func (e *engine) close() {
	defer e.storageLock.Unlock()
	if e.offModels != nil {
		e.offModels()
	}
	e.loginsMu.Lock()
	for id, login := range e.logins {
		login.cancel()
		delete(e.logins, id)
	}
	e.loginsMu.Unlock()
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
	close(e.events)
}

func (e *engine) get(id string) (*conversation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.sessions[id]
	if c == nil {
		return nil, errors.New("the PiG conversation was not found")
	}
	return c, nil
}

func (e *engine) saveLocked(c *conversation) error {
	data, err := json.Marshal(c.rec)
	if err != nil {
		return err
	}
	name := filepath.Join(e.root, "metadata", c.rec.Session.ID+".json")
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

func (e *engine) openLocked(c *conversation) error {
	if c.deleted {
		return errors.New("the conversation was deleted")
	}
	if c.session != nil {
		e.touchLocked(c)
		return nil
	}
	svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: c.rec.Session.Directory, AgentDir: filepath.Join(e.root, "agent"), ModelRuntime: e.models, ProjectTrusted: new(false)})
	if err != nil {
		return err
	}
	_ = svcs.SettingsManager().SetRetryEnabled(false)
	rt, err := coding.NewRuntime(coding.RuntimeOptions{Services: svcs, AbortContext: e.ctx})
	if err != nil {
		svcs.Close()
		return err
	}
	model := e.models.GetModel(c.rec.Session.Model.Provider, c.rec.Session.Model.ID)
	prompt := e.instructions
	if prompt == "" {
		prompt = "You are Wada, a focused software engineering assistant. Read the codebase before editing and verify your changes."
	}
	loader := coding.NewDefaultResourceLoader(coding.DefaultResourceLoaderOptions{
		CWD: c.rec.Session.Directory, AgentDir: filepath.Join(e.root, "agent"), SettingsManager: svcs.SettingsManager(),
		SystemPrompt: &prompt, AppendSystemPrompt: []string{}, NoContextFiles: true, NoExtensions: true,
		NoPromptTemplates: true, NoThemes: true, NoSkills: true,
	})
	if err := loader.ReloadWith(e.ctx); err != nil {
		svcs.Close()
		_ = rt.Close()
		return err
	}
	tools := []pa.AgentTool{&resultTool{c: c}, &projectTool{c: c, e: e}}
	if c.rec.Parent == "" && e.chiefTools != nil {
		tools = append(tools, e.chiefTools(func() (string, string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.rec.Session.ID, c.rec.Session.Directory
		})...)
	}
	opts := coding.SessionStartOptions{
		Model: model, ThinkingLevel: ai.ModelThinkingLevel(c.rec.Session.Model.Variant),
		SessionID: c.rec.Session.ID, SessionDir: filepath.Join(e.root, "sessions"),
		SystemPrompt: prompt, ResourceLoader: loader, SkipBuiltinTools: false, SkipExtensionTools: true,
		BeforeToolCall: []pa.BeforeToolCallHook{e.toolHook(c)}, ExtraTools: tools,
	}

	retainedLoader := false
	defer func() {
		if !retainedLoader {
			loader.Close()
		}
	}()
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
	if c.rec.Session.ID != "" && c.rec.Session.ID != s.ID() {
		_ = s.Close()
		_ = rt.Close()
		svcs.Close()
		return errors.New("the transcript belongs to another conversation")
	}
	s.SetActiveToolsByName(withoutTurnTools(s.ActiveToolNames()))
	s.Agent().SetToolExecution(pa.ToolModeSequential)
	c.rec.Path = s.Path()
	c.session, c.services, c.runtime, c.loader = s, svcs, rt, loader
	retainedLoader = true
	c.translator.session = s.ID()
	s.DiscardEvents()
	c.off = s.Subscribe(func(ev pa.AgentEvent) { e.onEvent(c, ev) })
	e.touchLocked(c)
	return nil
}

func (e *engine) resolveModel(ref protocol.ModelRef) *ai.Model {
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
