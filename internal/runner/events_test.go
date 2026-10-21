package runner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"wada-desktop-go/internal/agent"
)

func TestBatchesPreserveOrder(t *testing.T) {
	for _, count := range []int{0, 80} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			posted, applied := 0, 0
			ended := errors.New("stream ended")
			err := batchUpdates(context.Background(), func(ctx context.Context, emit func(agent.Update), _ func(string)) error {
				for i := range count {
					emit(agent.Update{Kind: agent.TextDelta, SessionID: "session", Text: &agent.TextData{MessageID: "message", Delta: fmt.Sprint(i)}})
				}
				return ended
			}, func(fn func()) { posted++; fn() }, func(batch []agent.Update) {
				if len(batch) > 64 {
					t.Fatal("event batch exceeded its bound")
				}
				for _, event := range batch {
					if event.Text.Delta != fmt.Sprint(applied) {
						t.Fatalf("delta %q at position %d", event.Text.Delta, applied)
					}
					applied++
				}
			}, func(message string) { t.Fatalf("valid stream supplied diagnostic %q", message) })
			if err != ended || applied != count || (count == 0 && posted != 0) || (count > 0 && posted >= count) {
				t.Fatalf("err=%v applied=%d posted=%d", err, applied, posted)
			}
		})
	}
}

func TestBatchCancellationReleasesSourceWithOnePendingPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	posts := make(chan func(), 1)
	done := make(chan error, 1)
	sourceDone := make(chan struct{})
	applied := false
	go func() {
		done <- batchUpdates(ctx, func(ctx context.Context, emit func(agent.Update), _ func(string)) error {
			defer close(sourceDone)
			for range 10000 {
				emit(agent.Update{Kind: agent.TextDelta, SessionID: "session", Text: &agent.TextData{Delta: "x"}})
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return nil
		}, func(fn func()) { posts <- fn }, func([]agent.Update) { applied = true }, func(string) {})
	}()
	var pending func()
	select {
	case pending = <-posts:
	case <-time.After(3 * time.Second):
		t.Fatal("no pending publication")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked UI prevented cancellation")
	}
	select {
	case <-sourceDone:
	default:
		t.Fatal("batch returned before releasing its source")
	}
	pending()
	if applied || len(posts) != 0 {
		t.Fatal("closed stream applied events or accumulated callbacks")
	}
}
