package runner

import (
	"context"
	"time"
	"wada-desktop-go/internal/opencode"
)

// agentUpdates publishes ordered batches at most one pending UI callback
// at a time. The unbuffered pipe gives a busy UI backpressure instead of
// retaining an unbounded queue of event payloads.
func batchUpdates(ctx context.Context, source func(context.Context, func(opencode.Event)) error,
	post func(func()), apply func([]opencode.Update), notice func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan opencode.Update)
	done := make(chan error, 1)
	go func() {
		warned := false
		done <- source(ctx, func(wire opencode.Event) {
			ev, known, err := opencode.DecodeEvent(wire)
			if err != nil {
				if !warned {
					warned = true
					notice(err.Error())
				}
				return
			}
			if !known {
				return
			}
			select {
			case queue <- ev:
			case <-ctx.Done():
			}
		})
		close(queue)
	}()
	for {
		var first opencode.Update
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-queue:
			if !ok {
				return <-done
			}
			first = ev
		}
		batch := []opencode.Update{first}
		bytes := first.Bytes
		timer := time.NewTimer(time.Second / 60)
	collect:
		for len(batch) < 64 && bytes < 256<<10 {
			select {
			case ev, ok := <-queue:
				if !ok {
					break collect
				}
				batch = append(batch, ev)
				bytes += ev.Bytes
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		timer.Stop()
		applied := make(chan struct{})
		post(func() {
			if ctx.Err() == nil {
				apply(batch)
			}
			close(applied)
		})
		select {
		case <-applied:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
