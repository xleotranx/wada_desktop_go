package kernel

import "context"

// Task is a replaceable background job. The zero value is ready to use. Its
// fields belong to the UI thread.
type Task struct {
	gen     uint64
	cancel  context.CancelFunc
	running bool
}

// Go cancels the task's previous run, runs work off the UI thread with a
// context derived from o's Lifetime, and calls apply on the UI thread only if
// o is still alive and this is still the task's latest run. A panic in work
// fails the owning plugin, even when a later run has replaced this one.
func Go[T any](o Owner, t *Task, work func(context.Context) (T, error), apply func(T, error)) {
	s := o.owner()
	if !s.alive {
		return
	}
	t.Cancel()
	gen := t.gen
	ctx, cancel := context.WithCancel(s.ctx)
	t.cancel, t.running = cancel, true
	k := s.k
	done := s.beginWork()
	k.opts.Background(func() {
		var v T
		var err error
		perr := recovered(func() { v, err = work(ctx) })
		done()
		k.opts.Post(func() {
			cancel()
			latest := t.gen == gen
			if latest {
				t.cancel, t.running = nil, false
			}
			switch {
			case !s.alive:
			case perr != nil:
				k.fail(s.plugin, perr)
			case latest:
				k.call(s.plugin, func() { apply(v, err) })
			}
		})
	})
}

// Cancel stops the current run; its result will not be applied.
func (t *Task) Cancel() {
	if t.cancel != nil {
		t.cancel()
	}
	t.gen++
	t.cancel, t.running = nil, false
}

// Running reports whether a run has started and not yet been applied or
// cancelled.
func (t *Task) Running() bool { return t.running }
