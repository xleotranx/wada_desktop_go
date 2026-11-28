package client

import "sync"

const (
	feedOpen = iota
	feedFinishing
	feedStopped
)

// feed hands values to a consumer's channel in order without ever blocking
// the producer, the connection's read loop. Values wait in an unbounded queue
// up to limit (by weigh), past which the feed stops: a consumer that far
// behind is ended rather than allowed to stall every other stream and RPC.
type feed[T any] struct {
	out    chan T
	wake   chan struct{}
	stop   chan struct{}
	exited chan struct{}
	limit  int
	weigh  func(T) int

	mu    sync.Mutex
	items []T
	size  int
	state int
}

func newFeed[T any](limit int, weigh func(T) int) *feed[T] {
	f := &feed[T]{
		out:    make(chan T),
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		exited: make(chan struct{}),
		limit:  limit,
		weigh:  weigh,
	}
	go f.run()
	return f
}

// push queues v and reports whether the feed still accepts values. It stops
// the feed when v would take it past its limit.
func (f *feed[T]) push(v T) bool {
	f.mu.Lock()
	if f.state != feedOpen {
		f.mu.Unlock()
		return false
	}
	w := f.weigh(v)
	if f.size+w > f.limit {
		f.stopLocked()
		f.mu.Unlock()
		return false
	}
	f.items = append(f.items, v)
	f.size += w
	f.mu.Unlock()
	f.signal()
	return true
}

// finish closes the channel once the queued values are delivered.
func (f *feed[T]) finish() {
	f.mu.Lock()
	if f.state == feedOpen {
		f.state = feedFinishing
	}
	f.mu.Unlock()
	f.signal()
}

// halt drops the queued values and returns once the channel is closed.
func (f *feed[T]) halt() {
	f.mu.Lock()
	f.stopLocked()
	f.mu.Unlock()
	<-f.exited
}

func (f *feed[T]) stopLocked() {
	if f.state != feedStopped {
		f.state = feedStopped
		f.items = nil
		f.size = 0
		close(f.stop)
	}
}

func (f *feed[T]) signal() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *feed[T]) run() {
	defer close(f.exited)
	defer close(f.out)
	for {
		f.mu.Lock()
		if f.state == feedStopped {
			f.mu.Unlock()
			return
		}
		if len(f.items) == 0 {
			finishing := f.state == feedFinishing
			f.mu.Unlock()
			if finishing {
				return
			}
			select {
			case <-f.wake:
			case <-f.stop:
				return
			}
			continue
		}
		v := f.items[0]
		var zero T
		f.items[0] = zero
		f.items = f.items[1:]
		f.size -= f.weigh(v)
		f.mu.Unlock()
		select {
		case f.out <- v:
		case <-f.stop:
			return
		}
	}
}
