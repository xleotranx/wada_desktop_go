package kernel

import "context"

// Owner is anything registrations can belong to: *Context, *Scope and *Kernel.
// Only this package implements it.
type Owner interface{ owner() *scope }

// scope is one lifetime. Everything registered on it is undone by dispose.
// All fields belong to the UI thread except ctx, which is immutable.
type scope struct {
	k         *Kernel
	plugin    *plugin // nil for the kernel's own root scope
	parent    *scope
	ctx       context.Context
	cancel    context.CancelFunc
	alive     bool
	children  []*scope
	disposers []func()
}

func newScope(k *Kernel, p *plugin, parent *scope) *scope {
	base := context.Background()
	if parent != nil {
		base = parent.ctx
	}
	ctx, cancel := context.WithCancel(base)
	s := &scope{k: k, plugin: p, parent: parent, ctx: ctx, cancel: cancel, alive: true}
	if parent != nil {
		parent.children = append(parent.children, s)
	}
	return s
}

// onDispose registers fn on a live scope, or runs nothing on a dead one.
func (s *scope) onDispose(fn func()) {
	if s.alive {
		s.disposers = append(s.disposers, fn)
	}
}

// dispose marks the scope dead, cancels its context, disposes children newest
// first, then runs its own disposers newest first. It is idempotent.
func (s *scope) dispose() {
	if !s.alive {
		return
	}
	s.alive = false
	s.cancel()
	for i := len(s.children) - 1; i >= 0; i-- {
		s.children[i].dispose()
	}
	s.children = nil
	for i := len(s.disposers) - 1; i >= 0; i-- {
		s.k.call(s.plugin, s.disposers[i])
	}
	s.disposers = nil
	if p := s.parent; p != nil && p.alive {
		for i, c := range p.children {
			if c == s {
				p.children = append(p.children[:i], p.children[i+1:]...)
				break
			}
		}
	}
}

// Scope is a lifetime whose registrations unwind together. Use NewScope for
// work tied to something shorter than the plugin, such as one open conversation.
type Scope struct{ s *scope }

func (s *Scope) owner() *scope { return s.s }

// Lifetime is cancelled when the scope is disposed. Safe on any goroutine.
func (s *Scope) Lifetime() context.Context { return s.s.ctx }

func (s *Scope) Alive() bool { return s.s.alive }

// OnDispose runs fn during dispose, in reverse registration order.
func (s *Scope) OnDispose(fn func()) { s.s.onDispose(fn) }

// NewScope returns a child scope, disposed with this one at the latest.
func (s *Scope) NewScope() *Scope {
	if !s.s.alive {
		dead := newScope(s.s.k, s.s.plugin, nil)
		dead.dispose()
		return &Scope{dead}
	}
	return &Scope{newScope(s.s.k, s.s.plugin, s.s)}
}

// Dispose cancels Lifetime, then undoes every registration. Idempotent.
func (s *Scope) Dispose() { s.s.dispose() }

// Post runs fn on the UI thread if the scope is still alive then. Safe on any
// goroutine.
func (s *Scope) Post(fn func()) {
	sc := s.s
	sc.k.opts.Post(func() {
		if sc.alive {
			sc.k.call(sc.plugin, fn)
		}
	})
}

// Spawn runs a long-lived worker bound to Lifetime and reports whether it
// started: it does not once the scope is disposed. Close waits for it to
// return. A panic in fn fails the owning plugin. Call it on the UI thread or
// from a worker the kernel runs (a Spawn body or the work of Go, of any
// plugin), which is how a worker starts a goroutine of its own; a plugin
// never uses a bare go statement, whose panic would end the app.
func (s *Scope) Spawn(fn func(ctx context.Context)) bool {
	sc := s.s
	if sc.ctx.Err() != nil { // cancelled first by dispose; safe off the UI thread
		return false
	}
	done := sc.beginWork()
	go func() {
		defer done()
		if err := recovered(func() { fn(sc.ctx) }); err != nil {
			s.Post(func() { sc.k.fail(sc.plugin, err) })
		}
	}()
	return true
}

// beginWork is called on the UI thread, or by a worker the kernel already
// counts, so Close cannot be waiting on a zero count meanwhile; its
// completion is safe on a worker.
func (s *scope) beginWork() func() {
	s.k.workers.Add(1)
	if s.plugin != nil {
		s.plugin.workers.Add(1)
	}
	return func() {
		if s.plugin != nil {
			s.plugin.workers.Add(-1)
		}
		s.k.workers.Done()
	}
}

// Context is one plugin's handle: its root scope plus plugin-level facilities.
type Context struct {
	Scope
	p *plugin
}

func (c *Context) ID() string { return c.p.m.ID }

// Settings returns the plugin's namespaced settings.
func (c *Context) Settings() *Settings { return &Settings{k: c.s.k, ns: c.p.m.ID} }
