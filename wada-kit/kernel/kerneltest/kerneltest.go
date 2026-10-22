// Package kerneltest runs a kernel in tests with a controllable UI thread and
// background queue.
package kerneltest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xleotranx/wada-kit/kernel"
)

type Config struct {
	Store   *Store // nil means a fresh one
	Lenient bool   // fail plugins on panic instead of re-panicking
}

// Harness is a started kernel. The test goroutine is its UI thread. Posted
// closures and background work queue until the test runs them.
type Harness struct {
	*kernel.Kernel
	Store *Store

	t      testing.TB
	mu     sync.Mutex
	posts  []func()
	work   []func()
	closed bool
}

// New starts a Strict kernel with the plugins installed.
func New(t testing.TB, plugins ...kernel.Plugin) *Harness {
	return Start(t, Config{}, plugins...)
}

// Start starts a kernel; t.Cleanup closes it and fails the test if workers leak.
func Start(t testing.TB, cfg Config, plugins ...kernel.Plugin) *Harness {
	t.Helper()
	h := &Harness{Store: cfg.Store, t: t}
	if h.Store == nil {
		h.Store = NewStore()
	}
	h.Kernel = kernel.New(kernel.Options{
		Post:       func(fn func()) { h.mu.Lock(); h.posts = append(h.posts, fn); h.mu.Unlock() },
		Background: func(fn func()) { h.mu.Lock(); h.work = append(h.work, fn); h.mu.Unlock() },
		Store:      h.Store,
		Strict:     !cfg.Lenient,
	})
	if err := h.Install(plugins...); err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

// Work reports how many background jobs are queued.
func (h *Harness) Work() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.work) }

// RunWork runs the queued background job at index i.
func (h *Harness) RunWork(i int) {
	h.mu.Lock()
	job := h.work[i]
	h.work = slices.Delete(h.work, i, i+1)
	h.mu.Unlock()
	job()
}

// RunPosts runs the closures posted so far, in order, on the test goroutine.
func (h *Harness) RunPosts() {
	h.mu.Lock()
	posts := h.posts
	h.posts = nil
	h.mu.Unlock()
	for _, fn := range posts {
		fn()
	}
}

// Flush runs queued work and posts until both queues are empty.
func (h *Harness) Flush() {
	for {
		h.mu.Lock()
		idle := len(h.work) == 0 && len(h.posts) == 0
		h.mu.Unlock()
		if idle {
			return
		}
		for h.Work() > 0 {
			h.RunWork(0)
		}
		h.RunPosts()
	}
}

// WaitPosts waits for a real goroutine (such as a Spawn worker) to post at
// least n closures, then runs them.
func (h *Harness) WaitPosts(n int) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		ready := len(h.posts) >= n
		h.mu.Unlock()
		if ready {
			h.RunPosts()
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %d posts", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Close closes the kernel, running queued work so Close can finish, then runs
// the remaining posts, which a closed kernel drops. Idempotent.
func (h *Harness) Close() {
	h.t.Helper()
	if h.closed {
		return
	}
	h.closed = true
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			h.mu.Lock()
			var job func()
			if len(h.work) > 0 {
				job, h.work = h.work[0], h.work[1:]
			}
			h.mu.Unlock()
			if job != nil {
				job()
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := h.Kernel.Close(ctx)
	close(stop)
	<-done
	if err != nil {
		h.t.Errorf("kernel close: %v", err)
	}
	h.RunPosts()
}

// Store is an in-memory kernel.Store.
type Store struct {
	mu     sync.Mutex
	values map[[2]string][]byte
	saves  int
	Fail   error // returned by Save when set
}

func NewStore() *Store { return &Store{values: map[[2]string][]byte{}} }

func (s *Store) Load(context.Context) ([]kernel.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []kernel.Value
	for _, k := range slices.SortedFunc(maps.Keys(s.values), func(a, b [2]string) int {
		if a[0] != b[0] {
			return strings.Compare(a[0], b[0])
		}
		return strings.Compare(a[1], b[1])
	}) {
		out = append(out, kernel.Value{Namespace: k[0], Key: k[1], JSON: slices.Clone(s.values[k])})
	}
	return out, nil
}

func (s *Store) Save(_ context.Context, changed []kernel.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	s.saves++
	for _, v := range changed {
		if v.JSON == nil {
			delete(s.values, [2]string{v.Namespace, v.Key})
		} else {
			s.values[[2]string{v.Namespace, v.Key}] = slices.Clone(v.JSON)
		}
	}
	return nil
}

// Raw returns a stored value, or nil.
func (s *Store) Raw(ns, key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[[2]string{ns, key}]
}

// Saves counts successful Save calls.
func (s *Store) Saves() int { s.mu.Lock(); defer s.mu.Unlock(); return s.saves }
