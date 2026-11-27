package pig

import "slices"

const idleSessionLimit = 8

func (r *Runtime) acquireConversation(id string) (*engine, *conversation, func(), error) {
	e, release, err := r.acquire()
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
	for _, c := range evicted {
		c.closeResources()
		c.lifetime.Unlock()
	}
}

func (c *conversation) closeResources() {
	c.mu.Lock()
	s, off, rt, svcs, loader := c.session, c.off, c.runtime, c.services, c.loader
	c.session, c.off, c.runtime, c.services, c.loader = nil, nil, nil, nil, nil
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
	if loader != nil {
		loader.Close()
	}
	if svcs != nil {
		svcs.Close()
	}
}
