package server

import (
	"testing"
	"time"
)

func TestAuthLimiterBacksOffAfterRepeatedFailures(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newAuthLimiter()
	l.now = func() time.Time { return now }

	for range authFreeFailures - 1 {
		l.fail("p")
		if w := l.retryAfter("p"); w != 0 {
			t.Fatalf("throttled before %d failures: %s", authFreeFailures, w)
		}
	}
	l.fail("p")
	if w := l.retryAfter("p"); w != authBaseDelay {
		t.Fatalf("after %d failures wait = %s, want %s", authFreeFailures, w, authBaseDelay)
	}
	l.fail("p")
	if w := l.retryAfter("p"); w != 2*authBaseDelay {
		t.Fatalf("next wait = %s, want %s", w, 2*authBaseDelay)
	}
	if w := l.retryAfter("other"); w != 0 {
		t.Fatalf("another peer was throttled: %s", w)
	}
	for range 40 {
		l.fail("p")
	}
	if w := l.retryAfter("p"); w != authMaxDelay {
		t.Fatalf("capped wait = %s, want %s", w, authMaxDelay)
	}
	now = now.Add(authForget)
	if w := l.retryAfter("p"); w != 0 {
		t.Fatalf("failures not forgotten: %s", w)
	}
	l.fail("q")
	l.succeed("q")
	if _, ok := l.peers["q"]; ok {
		t.Fatal("success kept the failure count")
	}
}
