// Package kernel is the app's plugin core: plugin lifecycle, scoped and
// reversible registration, services, events, pipelines, extension points,
// background tasks and settings. It has no UI and knows no feature; see
// docs/kernel-api.md.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Plugin is one installable unit of the app.
type Plugin interface {
	Manifest() Manifest
	// Setup runs on the UI thread and registers everything the plugin needs
	// through c. If it returns an error or panics, or a registration through
	// c failed meanwhile, everything registered so far is undone and the
	// plugin is marked Failed.
	Setup(c *Context) error
}

// Factory makes a plugin. The kernel calls it once at Install, for the
// manifest and the first activation, and again for every later activation,
// so each Setup starts on a new value with nothing left of an earlier one.
type Factory func() Plugin

type Manifest struct {
	ID          string // [a-z][a-z0-9-]*; prefixes every entry ID the plugin registers
	Title       string
	Description string
	Provides    []Named // service keys Setup provides
	Requires    []Named // service keys that must be provided before Setup runs
	Locked      bool    // the user cannot disable it
	DefaultOff  bool    // disabled until the user enables it
}

// Named is implemented by Key, Topic, Pipeline and Point.
type Named interface{ Name() string }

type State uint8

const (
	Active   State = iota
	Disabled       // by the user, or DefaultOff
	Blocked        // a required key has no provider
	Failed         // Setup or a callback errored or panicked
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Disabled:
		return "disabled"
	case Blocked:
		return "blocked"
	case Failed:
		return "failed"
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

type Status struct {
	Manifest Manifest
	State    State
	Err      error
	Missing  []string // unprovided required keys
	// Restarting is set on a Failed plugin the kernel sets up again shortly;
	// Restarts counts its restarts within the policy's window.
	Restarting bool
	Restarts   int
}

// PluginError attributes an error to the plugin that caused it.
type PluginError struct {
	Plugin string
	Err    error
}

func (e *PluginError) Error() string { return "plugin " + e.Plugin + ": " + e.Err.Error() }
func (e *PluginError) Unwrap() error { return e.Err }

// PanicError is a recovered panic.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

var (
	Started        = NewTopic[struct{}]("kernel.started")
	PluginsChanged = NewTopic[[]Status]("kernel.plugins")
	SaveFailed     = NewTopic[error]("kernel.save-failed")
	// PointChanged carries the name of a point whose entries changed. It is
	// posted after the change, once for the changes to a point that land
	// before it runs.
	PointChanged = NewTopic[string]("kernel.point-changed")
)

type Options struct {
	Post       func(func()) // required: schedule on the UI thread
	Background func(func()) // optional: run off the UI thread; default is a goroutine
	Store      Store        // required
	Strict     bool         // re-panic instead of failing the plugin
	// Restart brings back a plugin that failed after its Setup; the zero
	// value never does.
	Restart Restart
	// Now is the clock for Restart's window; nil means time.Now.
	Now func() time.Time
}

// Restart is how often the kernel sets a plugin up again after it failed at
// run time, as when a view or listener panicked: up to Max times within
// Within, and then it stays Failed until the user enables it. A Setup that
// fails is not restarted, since it would fail the same way.
type Restart struct {
	Max    int
	Within time.Duration
}

// HostPrefix starts the IDs of entries the host contributes through the Kernel.
const HostPrefix = "app."

const kernelNamespace = "kernel"

var pluginID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type plugin struct {
	make  Factory
	fresh Plugin // made and not yet set up; nil once used
	m     Manifest
	index int
	// setupErr is the first registration that failed during Setup.
	setupErr error

	state     State
	err       error
	enabled   bool
	ctx       *Context // non-nil while Active
	settingUp bool
	disposing bool
	workers   atomic.Int64

	failures   []time.Time // run-time failures within the restart window
	restarting bool        // a restart is posted
	meter      Meter       // UI-thread time in its callbacks
}

func (p *plugin) id() string {
	if p == nil {
		return ""
	}
	return p.m.ID
}

// rank orders the host (nil) before plugins, then plugins by install order.
func (p *plugin) rank() int {
	if p == nil {
		return -1
	}
	return p.index
}

// Kernel hosts the plugins. It is not safe for concurrent use: call it on the
// UI thread only.
type Kernel struct {
	opts    Options
	root    *scope
	plugins []*plugin
	byID    map[string]*plugin
	active  []*plugin // in setup order

	services map[string]*service
	topics   map[string]*topic
	pipes    map[string]*pipeline
	points   map[string]*point
	// moved holds the points changed since PointChanged was last posted.
	moved map[string]bool

	values  map[string]map[string][]byte
	rev     uint64 // counts changes to values, for Setting's cache
	dirty   map[valueKey][]byte
	deleted map[valueKey]bool
	saving  *pendingSave

	workers sync.WaitGroup
	started bool
	closed  bool
	// nested is the time spent in plugin callbacks inside the callback that
	// runs now, so that one is charged only for its own.
	nested time.Duration
}

func New(opts Options) *Kernel {
	if opts.Post == nil || opts.Store == nil {
		panic("kernel: Options.Post and Options.Store are required")
	}
	if opts.Background == nil {
		opts.Background = func(fn func()) { go fn() }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	k := &Kernel{opts: opts, byID: map[string]*plugin{},
		services: map[string]*service{}, topics: map[string]*topic{},
		pipes: map[string]*pipeline{}, points: map[string]*point{}, moved: map[string]bool{},
		values: map[string]map[string][]byte{}, dirty: map[valueKey][]byte{}, deleted: map[valueKey]bool{}}
	k.root = newScope(k, nil, nil)
	return k
}

func (k *Kernel) owner() *scope { return k.root }

// Install registers plugins by manifest. It runs no plugin code beyond the
// factories and Manifest and must precede Start.
func (k *Kernel) Install(factories ...Factory) error {
	if k.started {
		return errors.New("kernel: Install after Start")
	}
	for _, f := range factories {
		var p Plugin
		if f != nil {
			p = f()
		}
		if p == nil {
			return errors.New("kernel: a factory made no plugin")
		}
		m := p.Manifest()
		if !pluginID.MatchString(m.ID) || m.ID == kernelNamespace || m.ID+"." == HostPrefix {
			return fmt.Errorf("kernel: invalid plugin ID %q", m.ID)
		}
		if k.byID[m.ID] != nil {
			return fmt.Errorf("kernel: duplicate plugin ID %q", m.ID)
		}
		for _, n := range append(slices.Clone(m.Provides), m.Requires...) {
			if n == nil || n.Name() == "" {
				return fmt.Errorf("kernel: plugin %q declares an unnamed key", m.ID)
			}
		}
		for _, r := range m.Requires {
			if declares(m.Provides, r.Name()) {
				return fmt.Errorf("kernel: plugin %q requires a key it provides: %s", m.ID, r.Name())
			}
		}
		rec := &plugin{make: f, fresh: p, m: m, index: len(k.plugins), state: Disabled}
		k.plugins = append(k.plugins, rec)
		k.byID[m.ID] = rec
	}
	return k.checkCycles()
}

// checkCycles rejects plugins that could only start after themselves.
func (k *Kernel) checkCycles() error {
	const (
		unseen = iota
		visiting
		done
	)
	mark := map[*plugin]int{}
	var visit func(*plugin) error
	visit = func(p *plugin) error {
		switch mark[p] {
		case visiting:
			return fmt.Errorf("kernel: requirement cycle through plugin %q", p.m.ID)
		case done:
			return nil
		}
		mark[p] = visiting
		for _, r := range p.m.Requires {
			for _, q := range k.plugins {
				if declares(q.m.Provides, r.Name()) {
					if err := visit(q); err != nil {
						return err
					}
				}
			}
		}
		mark[p] = done
		return nil
	}
	for _, p := range k.plugins {
		if err := visit(p); err != nil {
			return err
		}
	}
	return nil
}

// Start loads settings and sets up every enabled plugin in dependency order.
func (k *Kernel) Start() error {
	if k.started {
		return errors.New("kernel: Start called twice")
	}
	if err := k.load(); err != nil {
		return err
	}
	k.started = true
	for _, p := range k.plugins {
		p.enabled = p.m.Locked || !p.m.DefaultOff
		var on bool
		if !p.m.Locked && k.get(kernelNamespace, p.m.ID, &on) {
			p.enabled = on
		}
	}
	k.settle()
	Emit(k, Started, struct{}{})
	k.changed()
	return nil
}

// Enable turns a plugin on and sets it up when its requirements are met.
// A plugin left Blocked is not an error; its Status says what is missing.
func (k *Kernel) Enable(id string) error {
	p, err := k.lookup(id)
	if err != nil || p.state == Active {
		return err
	}
	if other := k.conflict(p); other != nil {
		return fmt.Errorf("kernel: %s is already provided by %q", other.key, other.by)
	}
	p.enabled, p.state, p.err, p.restarting, p.failures = true, Disabled, nil, false, nil
	if !p.m.Locked {
		k.set(kernelNamespace, p.m.ID, true)
	}
	k.settle()
	k.changed()
	if p.state == Failed {
		return p.err
	}
	return nil
}

// Disable disposes a plugin and every active plugin that requires its keys.
func (k *Kernel) Disable(id string) error {
	p, err := k.lookup(id)
	if err != nil {
		return err
	}
	if p.m.Locked {
		return fmt.Errorf("kernel: plugin %q is locked", id)
	}
	p.enabled, p.restarting = false, false
	k.set(kernelNamespace, p.m.ID, false)
	if p.ctx != nil {
		k.dispose(p, Disabled, nil)
	} else {
		p.state, p.err = Disabled, nil
	}
	k.changed()
	return nil
}

func (k *Kernel) lookup(id string) (*plugin, error) {
	if !k.started || k.closed {
		return nil, errors.New("kernel: not running")
	}
	p := k.byID[id]
	if p == nil {
		return nil, fmt.Errorf("kernel: unknown plugin %q", id)
	}
	return p, nil
}

// Plugins reports every installed plugin in install order.
func (k *Kernel) Plugins() []Status {
	out := make([]Status, 0, len(k.plugins))
	for _, p := range k.plugins {
		st := Status{Manifest: p.m, State: p.state, Err: p.err, Restarting: p.restarting, Restarts: k.recentRestarts(p)}
		if p.state == Blocked {
			st.Missing = k.missing(p)
		}
		out = append(out, st)
	}
	return out
}

func (k *Kernel) changed() { Emit(k, PluginsChanged, k.Plugins()) }

// Close disposes every plugin, waits for workers until ctx ends, then writes
// settings that are still unsaved.
func (k *Kernel) Close(ctx context.Context) error {
	if k.closed {
		return nil
	}
	for len(k.active) > 0 {
		k.dispose(k.active[len(k.active)-1], Disabled, nil)
	}
	k.root.dispose()
	k.closed = true
	wait := make(chan struct{})
	go func() { k.workers.Wait(); close(wait) }()
	var errs []error
	select {
	case <-wait:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("kernel: workers still running: %w", ctx.Err()))
	}
	// UI callbacks cannot run while Close waits. Collect the in-flight save
	// here so a failed batch is retried together with any newer changes.
	if pending := k.saving; pending != nil {
		select {
		case err := <-pending.done:
			k.saving = nil
			if err != nil {
				k.restoreDirty(pending.batch)
			}
		case <-ctx.Done():
			// Never start a second Save while the first is still running.
			return errors.Join(append(errs, fmt.Errorf("kernel: settings save still running: %w", ctx.Err()))...)
		}
	}
	if batch := k.takeDirty(); len(batch) > 0 {
		if err := k.opts.Store.Save(ctx, batch); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type conflict struct{ key, by string }

// conflict names a key p provides that another active plugin already provides.
func (k *Kernel) conflict(p *plugin) *conflict {
	for _, n := range p.m.Provides {
		if s := k.services[n.Name()]; s != nil && s.by != p {
			return &conflict{n.Name(), s.by.m.ID}
		}
	}
	return nil
}

func (k *Kernel) missing(p *plugin) []string {
	var out []string
	for _, r := range p.m.Requires {
		if k.services[r.Name()] == nil {
			out = append(out, r.Name())
		}
	}
	return out
}

// settle sets up enabled, inactive plugins whose requirements are met, in
// install order, repeating while any setup provides something new.
func (k *Kernel) settle() {
	for progress := true; progress; {
		progress = false
		for _, p := range k.plugins {
			if !p.enabled || p.ctx != nil || p.state == Failed {
				continue
			}
			if len(k.missing(p)) > 0 {
				p.state = Blocked
				continue
			}
			if c := k.conflict(p); c != nil {
				p.state, p.err = Failed, fmt.Errorf("%s is already provided by %q", c.key, c.by)
				continue
			}
			if k.setup(p) {
				progress = true
			}
		}
	}
}

func (k *Kernel) setup(p *plugin) bool {
	inst := p.fresh
	p.fresh = nil
	if inst == nil {
		inst = p.make()
	}
	if inst == nil || inst.Manifest().ID != p.m.ID {
		p.state, p.err = Failed, errors.New("its factory made another plugin")
		return false
	}
	c := &Context{Scope: Scope{newScope(k, p, nil)}, p: p}
	p.ctx, p.settingUp, p.err, p.setupErr = c, true, nil, nil
	var err error
	k.timed(p, func() {
		if k.opts.Strict {
			err = inst.Setup(c)
		} else if perr := recovered(func() { err = inst.Setup(c) }); perr != nil {
			err = perr
		}
	})
	p.settingUp = false
	if err == nil {
		err = p.setupErr
	}
	p.setupErr = nil
	if err == nil {
		for _, n := range p.m.Provides {
			if s := k.services[n.Name()]; s == nil || s.by != p {
				err = fmt.Errorf("declared %s but did not provide it", n.Name())
				break
			}
		}
	}
	state := Failed
	if err == nil && len(k.missing(p)) > 0 {
		// A provider went down while Setup ran, as when a listener of its
		// panicked: p is not yet active, so it was not taken down with it.
		state = Blocked
	}
	if err != nil || state == Blocked {
		p.disposing = true
		c.s.dispose()
		p.ctx, p.disposing = nil, false
		p.state, p.err = state, err
		return false
	}
	p.state = Active
	k.active = append(k.active, p)
	return true
}

// dispose takes an active plugin down, dependents first. A dependent becomes
// Blocked; p ends in state, or Failed if one of its disposers panicked.
func (k *Kernel) dispose(p *plugin, state State, err error) {
	if p.ctx == nil || p.disposing {
		return
	}
	p.disposing = true
	for d := k.dependent(p); d != nil; d = k.dependent(p) {
		k.dispose(d, Blocked, nil)
	}
	p.err = err
	p.ctx.s.dispose()
	p.ctx, p.disposing = nil, false
	k.active = slices.DeleteFunc(k.active, func(q *plugin) bool { return q == p })
	if p.err != nil && state != Failed {
		state = Failed // a disposer failed
	}
	p.state = state
}

// dependent is the most recently set up active plugin requiring a key p provides.
func (k *Kernel) dependent(p *plugin) *plugin {
	for i := len(k.active) - 1; i >= 0; i-- {
		d := k.active[i]
		if d == p || d.disposing {
			continue
		}
		for _, r := range d.m.Requires {
			if declares(p.m.Provides, r.Name()) {
				return d
			}
		}
	}
	return nil
}

// call runs fn on behalf of p, failing p if fn panics, and times it. Host
// callbacks (p nil), Strict kernels and a plugin's own Setup let the panic
// through; setup then fails the plugin.
func (k *Kernel) call(p *plugin, fn func()) {
	if p == nil {
		fn()
		return
	}
	k.timed(p, func() {
		if k.opts.Strict || p.settingUp {
			fn()
		} else if err := recovered(fn); err != nil {
			k.fail(p, err)
		}
	})
}

// timed runs fn and charges p for the time it took, less what callbacks of
// plugins it ran themselves took.
func (k *Kernel) timed(p *plugin, fn func()) {
	start, outer := time.Now(), k.nested
	k.nested = 0
	defer func() {
		took := time.Since(start)
		p.meter.add(took - k.nested)
		k.nested = outer + took
	}()
	fn()
}

// fail disposes p as Failed with err, or records err while p is already
// being disposed. A plugin within its restart budget is set up again once
// the callback that failed has unwound.
func (k *Kernel) fail(p *plugin, err error) {
	if p == nil || k.opts.Strict {
		panic(err)
	}
	if p.ctx == nil || p.disposing {
		if p.err == nil {
			p.err = err
		}
		return
	}
	k.dispose(p, Failed, err)
	if p.state == Failed && k.mayRestart(p) {
		p.restarting = true
		k.opts.Post(func() { k.restart(p) })
	}
	k.changed()
}

// mayRestart counts a run-time failure of p and reports whether the policy
// allows another restart.
func (k *Kernel) mayRestart(p *plugin) bool {
	r := k.opts.Restart
	if r.Max <= 0 {
		return false
	}
	now := k.opts.Now()
	p.failures = slices.DeleteFunc(p.failures, func(t time.Time) bool { return now.Sub(t) >= r.Within })
	if len(p.failures) >= r.Max {
		return false
	}
	p.failures = append(p.failures, now)
	return true
}

// recentRestarts counts p's restarts within the policy's window.
func (k *Kernel) recentRestarts(p *plugin) int {
	if len(p.failures) == 0 {
		return 0
	}
	now, n := k.opts.Now(), 0
	for _, t := range p.failures {
		if now.Sub(t) < k.opts.Restart.Within {
			n++
		}
	}
	return n
}

// restart sets p up again unless something else decided its state since it
// failed: the user disabled or enabled it, or the kernel closed.
func (k *Kernel) restart(p *plugin) {
	if !p.restarting {
		return
	}
	p.restarting = false
	if k.closed || !p.enabled || p.state != Failed {
		return
	}
	p.state = Disabled // so settle sets it up; its error stays until then
	k.settle()
	k.changed()
}

func recovered(fn func()) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	fn()
	return nil
}

func declares(keys []Named, name string) bool {
	return slices.ContainsFunc(keys, func(n Named) bool { return n.Name() == name })
}
