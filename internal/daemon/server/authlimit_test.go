package server

import (
	"fmt"
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

func TestAuthLimiterEvictsTheOldestPeerWhenFull(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newAuthLimiter()
	l.now = func() time.Time { return now }
	for i := range authMaxPeers {
		now = now.Add(time.Millisecond)
		l.fail(fmt.Sprint("peer", i))
	}
	for range authFreeFailures {
		now = now.Add(time.Millisecond)
		l.fail("newcomer")
	}
	if len(l.peers) != authMaxPeers {
		t.Fatalf("peers = %d, want %d", len(l.peers), authMaxPeers)
	}
	if _, ok := l.peers["peer0"]; ok {
		t.Fatal("the oldest peer was kept")
	}
	if w := l.retryAfter("newcomer"); w == 0 {
		t.Fatal("a new peer past the limit was not throttled")
	}
	if w := l.retryAfter("someone-else"); w != 0 {
		t.Fatalf("an unseen peer is throttled: %s", w)
	}
}
