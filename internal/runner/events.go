package runner

import (
	"context"
	"time"

	"wada-desktop-go/internal/agent"
)

// agentUpdates publishes ordered batches at most one pending UI callback
// at a time. The unbuffered pipe gives a busy UI backpressure instead of
// retaining an unbounded queue of event payloads.
func batchUpdates(ctx context.Context, source func(context.Context, func(agent.Update), func(string)) error,
	post func(func()), apply func([]agent.Update), notice func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	queue := make(chan agent.Update)
	done := make(chan error, 1)
	sourceExited := make(chan struct{})
	defer func() { cancel(); <-sourceExited }()
	go func() {
		defer close(sourceExited)
		done <- source(ctx, func(ev agent.Update) {
			select {
			case queue <- ev:
			case <-ctx.Done():
			}
		}, notice)
		close(queue)
	}()
	for {
		var first agent.Update
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-queue:
			if !ok {
				return <-done
			}
			first = ev
		}
		batch := []agent.Update{first}
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
