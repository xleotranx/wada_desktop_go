package server

import (
	"sync"
	"time"
)

// Failed remote sign-ins are throttled per peer: after authFreeFailures in a
// row, each further attempt must wait a doubling delay, up to authMaxDelay.
// A peer's count is forgotten authForget after its last failure. Relay peers
// share one key, since the relay hides their addresses.
const (
	authFreeFailures = 5
	authBaseDelay    = time.Second
	authMaxDelay     = 5 * time.Minute
	authForget       = 15 * time.Minute
	authMaxPeers     = 4096
)

type authFailure struct {
	count int
	last  time.Time
}

type authLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	peers map[string]authFailure
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{now: time.Now, peers: map[string]authFailure{}}
}

func (l *authLimiter) delay(count int) time.Duration {
	if count < authFreeFailures {
		return 0
	}
	d := authBaseDelay
	for i := authFreeFailures; i < count && d < authMaxDelay; i++ {
		d *= 2
	}
	return min(d, authMaxDelay)
}

// retryAfter reports how long peer must still wait before its next attempt.
func (l *authLimiter) retryAfter(peer string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.peers[peer]
	if !ok {
		return 0
	}
	now := l.now()
	if now.Sub(f.last) >= authForget {
		delete(l.peers, peer)
		return 0
	}
	return max(0, f.last.Add(l.delay(f.count)).Sub(now))
}

func (l *authLimiter) fail(peer string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if _, ok := l.peers[peer]; !ok && len(l.peers) >= authMaxPeers {
		for k, f := range l.peers {
			if now.Sub(f.last) >= authForget {
				delete(l.peers, k)
			}
		}
		if len(l.peers) >= authMaxPeers {
			// Too many distinct failing peers: count them all as one.
			peer = "overflow"
		}
	}
	f := l.peers[peer]
	if now.Sub(f.last) >= authForget {
		f.count = 0
	}
	f.count++
	f.last = now
	l.peers[peer] = f
}

func (l *authLimiter) succeed(peer string) {
	l.mu.Lock()
	delete(l.peers, peer)
	l.mu.Unlock()
}
