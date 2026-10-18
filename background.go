package main

import "context"

// requestState owns one replaceable operation, including callbacks already
// posted when cancellation arrives.
type requestRevision uint64

type requestState struct {
	revision requestRevision
	cancel   context.CancelFunc
}

func (r *requestState) stop() {
	r.revision++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

func inBackground[T any](a *app, work func(context.Context) (T, error), done func(T, error)) {
	runRequest(a, new(requestState), work, done)
}

func runRequest[T any](a *app, request *requestState, work func(context.Context) (T, error), done func(T, error)) {
	route := a.connections.route
	runOwned(a, request, a.runtime.workCtx, work, func(value T, err error) {
		if a.connections.route == route {
			done(value, err)
		}
	})
}

// Registry operations may legitimately finish for an inactive server.
func runOwned[T any](a *app, request *requestState, parent context.Context, work func(context.Context) (T, error), done func(T, error)) {
	request.stop()
	revision := request.revision
	ctx, cancel := context.WithCancel(parent)
	request.cancel = cancel
	a.runtime.background(func() func() {
		value, err := work(ctx)
		return func() {
			defer cancel()
			if !a.runtime.closed && ctx.Err() == nil && request.revision == revision {
				done(value, err)
			}
		}
	})
}

func (a *app) closeWork() {
	a.runtime.closed = true
	a.runtime.stopWork()
	a.runtime.stopLifetime()
}
