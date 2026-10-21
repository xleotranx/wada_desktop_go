package feature

import "context"

// Request cancels superseded work and rejects callbacks already posted for it.
type Request struct {
	revision uint64
	cancel   context.CancelFunc
}

func (r *Request) Stop() {
	r.revision++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}
func Run[T any](h Host, r *Request, work func(context.Context) (T, error), done func(T, error)) {
	snapshot := h.Context()
	r.Stop()
	revision := r.revision
	ctx, cancel := context.WithCancel(snapshot.Work)
	r.cancel = cancel
	stopLifetime := context.AfterFunc(h.Lifetime, cancel)
	h.Background(func() func() {
		if h.Lifetime.Err() != nil {
			cancel()
			stopLifetime()
			return func() {}
		}
		value, err := work(ctx)
		return func() {
			defer cancel()
			defer stopLifetime()
			current := h.Context()
			if h.Lifetime.Err() == nil && !current.Closed && ctx.Err() == nil && r.revision == revision && current.Revision == snapshot.Revision && current.Connection == snapshot.Connection && current.Attempt == snapshot.Attempt {
				done(value, err)
			}
		}
	})
}
