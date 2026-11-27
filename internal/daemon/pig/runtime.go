// Package pig runs the embedded PiG coding-agent SDK headlessly inside wadad,
// decoupled from desktop UI frameworks and emitting protocol events.
package pig

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/protocol"
)

// ErrNotSent indicates validation refused Send before reaching the model.
var ErrNotSent = errors.New("the message was not sent")

// ErrNotConnected indicates the runtime has been closed.
var ErrNotConnected = errors.New("the agent runtime is closed")

// ProjectTool executes scoped turn operations when configured by a host/workflow.
type ProjectTool struct {
	Kind    string
	Execute func(ctx context.Context, dir, action string, input json.RawMessage) (string, error)
}

// Options configures a headless PiG runtime.
type Options struct {
	Root         string
	Instructions string
	ProjectTools map[string]ProjectTool
	ChiefTools   func(contextFn func() (sessionID, dir string)) []pa.AgentTool
}

// Runtime owns a headless PiG engine and its lifecycle.
type Runtime struct {
	origin string
	cancel context.CancelFunc
	mu     sync.Mutex
	engine *engine
}

// New initializes a headless PiG runtime rooted at opts.Root.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	r := &Runtime{
		origin: "pig " + absRoot,
		cancel: cancel,
	}
	spawn := func(fn func(context.Context)) bool {
		if rctx.Err() != nil {
			return false
		}
		go fn(rctx)
		return true
	}
	e, err := newEngine(rctx, absRoot, spawn)
	if err != nil {
		cancel()
		return nil, err
	}
	if opts.Instructions != "" {
		e.instructions = opts.Instructions
	} else {
		e.instructions = "You are Wada, a focused software engineering assistant and Chief Agent. Read the codebase before editing, make minimal verified changes, and use the selected workspace folder. For multi-part or parallel engineering tasks, you can also create isolated Git worktrees and spawn/supervise external CLI worker agents (such as Claude Code, Codex, or ACP harnesses) via your orchestration tools."
	}
	e.chiefTools = opts.ChiefTools
	if len(opts.ProjectTools) > 0 {
		tools := opts.ProjectTools
		e.projectTool = func(_ context.Context, kind string) (ProjectTool, error) {
			t, ok := tools[kind]
			if !ok || t.Execute == nil {
				return ProjectTool{}, errors.New("the project tools are unavailable")
			}
			return t, nil
		}
	}
	e.modelsChanged = func() {
		e.emit(protocol.Event{Kind: protocol.EventModelsChanged})
	}
	off := e.models.SetChangeListener(e.modelsChanged)
	e.offModels = off
	r.engine = e
	return r, nil
}

// Origin returns the backend origin identifier for this PiG storage root.
func (r *Runtime) Origin() string { return r.origin }

// ModelRuntime exposes PiG's ModelRuntime (e.g. for provider auth or tests).
func (r *Runtime) ModelRuntime() *coding.ModelRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.engine == nil {
		return nil
	}
	return r.engine.models
}

// Events returns the stream of normalized protocol events produced by this runtime.
func (r *Runtime) Events() <-chan protocol.Event {
	return r.engine.events
}

// Close cancels active turns, drains workers, and closes SDK resources.
func (r *Runtime) Close() error {
	r.cancel()
	r.mu.Lock()
	e := r.engine
	r.engine = nil
	r.mu.Unlock()
	if e != nil {
		e.close()
	}
	return nil
}

func (r *Runtime) acquire() (*engine, func(), error) {
	r.mu.Lock()
	e := r.engine
	r.mu.Unlock()
	if e == nil || e.ctx.Err() != nil {
		return nil, func() {}, ErrNotConnected
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return nil, func() {}, ErrNotConnected
	}
	e.active.Add(1)
	return e, func() { e.trimIdle(); e.active.Done() }, nil
}
