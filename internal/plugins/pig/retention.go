package pig

import (
	"slices"

	"wada-desktop-go/internal/contract/agent"
)

const idleSessionLimit = 8

// acquireConversation pins SDK resources through the service call, including
// synchronous extension callbacks that run without the metadata mutex.
func (p *Plugin) acquireConversation(id agent.SessionID) (*engine, *conversation, func(), error) {
	e, release, err := p.acquire()
	if err != nil {
		return nil, nil, release, err
	}
	c, err := e.get(id)
	if err != nil {
		return e, nil, release, err
	}
	c.lifetime.RLock()
	return e, c, func() { c.lifetime.RUnlock(); release() }, nil
}

// touchLocked is called while constructing or reading a pinned SDK owner.
// Only live owners enter this list; the metadata index may contain many more chats.
func (e *engine) touchLocked(c *conversation) {
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	e.live = slices.DeleteFunc(e.live, func(other *conversation) bool { return other == c })
	e.live = slices.Insert(e.live, 0, c)
}

func (e *engine) forget(c *conversation) {
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	e.live = slices.DeleteFunc(e.live, func(other *conversation) bool { return other == c })
}

// trimIdle keeps the eight most recently used idle owners. A reader pins an
// operation or a whole turn; TryLock lets unrelated chats finish independently.
func (e *engine) trimIdle() {
	e.liveMu.Lock()
	kept := e.live[:0]
	var evicted []*conversation
	idle := 0
	for _, c := range e.live {
		if !c.lifetime.TryLock() {
			kept = append(kept, c)
			continue
		}
		c.mu.Lock()
		protected := c.busy || c.pending > 0
		c.mu.Unlock()
		if !protected {
			idle++
		}
		if protected || idle <= idleSessionLimit {
			kept = append(kept, c)
			c.lifetime.Unlock()
		} else {
			evicted = append(evicted, c)
		}
	}
	clear(e.live[len(kept):])
	e.live = kept
	e.liveMu.Unlock()
	// SDK teardown can call extensions. Hold neither the metadata nor cache
	// mutex while it runs; the exclusive lifetime prevents reopening midway.
	for _, c := range evicted {
		c.closeResources()
		c.lifetime.Unlock()
	}
}

func (c *conversation) closeResources() {
	c.mu.Lock()
	s, off, rt, svcs := c.session, c.off, c.runtime, c.services
	c.session, c.off, c.runtime, c.services = nil, nil, nil, nil
	c.bound = false
	c.mu.Unlock()
	if off != nil {
		off()
	}
	if s != nil {
		_ = s.Close()
	}
	if rt != nil {
		_ = rt.Close()
	}
	if svcs != nil {
		svcs.Close()
	}
}
