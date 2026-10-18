package runner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"wada-desktop-go/internal/opencode"
)

func TestBatchesFilterUnknownEventsAndPreserveOrder(t *testing.T) {
	for _, count := range []int{0, 80} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			posted, applied := 0, 0
			ended := errors.New("stream ended")
			err := batchUpdates(context.Background(), func(ctx context.Context, emit func(opencode.Event)) error {
				for range 100 {
					emit(opencode.Event{Type: "session.tool.input.delta"})
				}
				for i := range count {
					emit(opencode.Event{Type: "session.text.delta", Data: []byte(fmt.Sprintf(`{"sessionID":"session","assistantMessageID":"message","ordinal":0,"delta":%q}`, fmt.Sprint(i)))})
				}
				return ended
			}, func(fn func()) { posted++; fn() }, func(batch []opencode.Update) {
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
		done <- batchUpdates(ctx, func(ctx context.Context, emit func(opencode.Event)) error {
			defer close(sourceDone)
			for range 10000 {
				emit(opencode.Event{Type: "session.text.delta", Data: []byte(`{"sessionID":"session","assistantMessageID":"message","ordinal":0,"delta":"x"}`)})
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return nil
		}, func(fn func()) { posts <- fn }, func([]opencode.Update) { applied = true }, func(string) {})
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

func TestMalformedStreamDiagnosticsAreBounded(t *testing.T) {
	notices := 0
	err := batchUpdates(context.Background(), func(_ context.Context, emit func(opencode.Event)) error {
		for range 1000 {
			emit(opencode.Event{Type: "session.text.delta", Data: []byte(`{malformed`)})
		}
		return nil
	}, func(fn func()) { fn() }, func([]opencode.Update) { t.Fatal("malformed data was published") }, func(string) { notices++ })
	if err != nil || notices != 1 {
		t.Fatalf("err=%v diagnostics=%d", err, notices)
	}
}
