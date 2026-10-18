package main

import (
	"context"
	"time"
	"wada-desktop-go/internal/opencode"
)

func renderedEvent(kind string) bool {
	switch kind {
	case "session.text.started", "session.text.delta", "session.text.ended",
		"session.tool.input.started", "session.tool.called", "session.tool.success",
		"permission.asked", "session.execution.started", "session.execution.succeeded",
		"session.execution.failed", "session.execution.interrupted",
		"session.model.selected", "session.renamed":
		return true
	}
	return false
}

// agentUpdates publishes ordered batches at most one pending UI callback
// at a time. The unbuffered pipe gives a busy UI backpressure instead of
// retaining an unbounded queue of event payloads.
func agentUpdates(ctx context.Context, source func(context.Context, func(opencode.Event)) error,
	post func(func()), apply func(opencode.Event)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan opencode.Event)
	done := make(chan error, 1)
	go func() {
		done <- source(ctx, func(ev opencode.Event) {
			if !renderedEvent(ev.Type) {
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
		var first opencode.Event
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-queue:
			if !ok {
				return <-done
			}
			first = ev
		}
		batch := []opencode.Event{first}
		bytes := len(first.Data)
		timer := time.NewTimer(time.Second / 60)
	collect:
		for len(batch) < 64 && bytes < 256<<10 {
			select {
			case ev, ok := <-queue:
				if !ok {
					break collect
				}
				batch = append(batch, ev)
				bytes += len(ev.Data)
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
				for _, ev := range batch {
					apply(ev)
				}
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
