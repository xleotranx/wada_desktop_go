package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A panic while handling pi's output ends the process and releases its
// callers before it reaches the worker that ran the reader, which fails the
// plugin.
func TestReaderPanicEndsTheProcess(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho '{\"type\":\"hello\"}'\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	recovered := make(chan any, 1)
	spawn := func(fn func(context.Context)) bool {
		go func() {
			defer func() { recovered <- recover() }()
			fn(context.Background())
		}()
		return true
	}
	p, err := start(context.Background(), exe, t.TempDir(), nil, spawn, func(*proc, string, []byte) { panic("bad record") })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-recovered:
		if v != "bad record" {
			t.Fatalf("the worker recovered %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reader did not end")
	}
	select {
	case <-p.done:
	default:
		t.Fatal("the process's callers were not released")
	}
	if err := p.call(context.Background(), "get_state", nil, nil); err == nil {
		t.Fatal("a call to the ended process succeeded")
	}
}

// A reader that cannot start, because the connection has ended, stops the
// process at once rather than leaving its callers waiting.
func TestStartOnAnEndedConnection(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, err := start(context.Background(), exe, t.TempDir(), nil, func(func(context.Context)) bool { return false },
		func(*proc, string, []byte) {})
	if err == nil {
		t.Fatal("start succeeded without its reader")
	}
	if time.Since(began) > 5*time.Second {
		t.Fatal("start waited for the process")
	}
}
