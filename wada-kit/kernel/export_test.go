package kernel

// Leaks counts registrations still held by the kernel's tables.
func Leaks(k *Kernel) int {
	n := len(k.services) + len(k.active)
	for _, t := range k.topics {
		n += len(t.listeners)
	}
	for _, p := range k.pipes {
		n += len(p.stages)
	}
	for _, p := range k.points {
		n += len(p.entries)
	}
	return n
}

// Disposers counts the disposers a context's root scope still lists.
func Disposers(c *Context) int { return len(c.s.disposers) }
