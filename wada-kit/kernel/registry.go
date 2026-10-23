package kernel

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// declared holds the type each name was declared with, by kind and name.
var declared sync.Map

// declare panics when a name of this kind was already declared with another
// type: the kernel looks entries up by name, so two types under one name
// would fail a type assertion later, possibly in the host, which no plugin
// failure contains. Declaring the same name with the same type again is fine.
func declare[T any](kind, name string) {
	t := reflect.TypeFor[T]()
	if prev, loaded := declared.LoadOrStore(kind+" "+name, t); loaded && prev != t {
		panic(fmt.Sprintf("kernel: %s %q declared as %v and as %v", kind, name, prev, t))
	}
}

// ---- Services

// Key names a capability with exactly one provider.
type Key[T any] struct{ name string }

func NewKey[T any](name string) Key[T] {
	declare[T]("key", name)
	return Key[T]{name}
}
func (k Key[T]) Name() string { return k.name }

type service struct {
	value any
	by    *plugin
}

// Provide registers v as the provider of k. It is allowed only during Setup,
// for a key declared in the Manifest's Provides. The service is withdrawn when
// the plugin is disposed, after the plugins that require it.
func Provide[T any](c *Context, k Key[T], v T) error {
	p, kn := c.p, c.s.k
	switch {
	case !p.settingUp:
		return fmt.Errorf("kernel: %s provided outside Setup", k.name)
	case !declares(p.m.Provides, k.name):
		return fmt.Errorf("kernel: %s is not in the Manifest's Provides", k.name)
	case kn.services[k.name] != nil:
		return fmt.Errorf("kernel: %s is already provided by %q", k.name, kn.services[k.name].by.m.ID)
	}
	kn.services[k.name] = &service{value: v, by: p}
	c.s.onDispose(func() { delete(kn.services, k.name) })
	return nil
}

// Require returns a service declared in the Manifest's Requires. The result
// may be kept for the plugin's lifetime. An undeclared key is a programming
// error and panics.
func Require[T any](c *Context, k Key[T]) T {
	if !declares(c.p.m.Requires, k.name) {
		panic(fmt.Sprintf("kernel: plugin %q requires undeclared key %s", c.p.m.ID, k.name))
	}
	v, ok := Use(c, k)
	if !ok {
		panic("kernel: required key " + k.name + " has no provider")
	}
	return v
}

// Use returns the current provider of an optional capability. Ask again on
// each use rather than keeping the result.
func Use[T any](o Owner, k Key[T]) (T, bool) {
	s := o.owner().k.services[k.name]
	if s == nil {
		var zero T
		return zero, false
	}
	return s.value.(T), true
}

// ---- Events

// Topic names a broadcast notification.
type Topic[T any] struct{ name string }

func NewTopic[T any](name string) Topic[T] {
	declare[T]("topic", name)
	return Topic[T]{name}
}
func (t Topic[T]) Name() string { return t.name }

type topic struct{ listeners []*listener }

type listener struct {
	s    *scope
	call func(any)
	off  bool
}

// On subscribes fn until o is disposed or off is called.
func On[T any](o Owner, t Topic[T], fn func(T)) (off func()) {
	s := o.owner()
	if !s.alive {
		return func() {}
	}
	tp := s.k.topics[t.name]
	if tp == nil {
		tp = &topic{}
		s.k.topics[t.name] = tp
	}
	l := &listener{s: s, call: func(v any) { fn(v.(T)) }}
	tp.listeners = append(tp.listeners, l)
	off = func() {
		if l.off {
			return
		}
		l.off = true
		// Copy on write: an Emit in progress keeps iterating its own slice.
		tp.listeners = slices.DeleteFunc(slices.Clone(tp.listeners), func(x *listener) bool { return x == l })
		if len(tp.listeners) == 0 && s.k.topics[t.name] == tp {
			delete(s.k.topics, t.name)
		}
	}
	s.onDispose(off)
	return off
}

// Emit calls the listeners subscribed before it started, synchronously and in
// subscription order. A panicking listener fails its plugin; the rest still run.
func Emit[T any](o Owner, t Topic[T], v T) {
	k := o.owner().k
	tp := k.topics[t.name]
	if tp == nil {
		return
	}
	for _, l := range tp.listeners {
		if !l.off && l.s.alive {
			k.call(l.s.plugin, func() { l.call(v) })
		}
	}
}

// ---- Pipelines

// Pipeline names an ordered chain of transforms over a value.
type Pipeline[T any] struct{ name string }

func NewPipeline[T any](name string) Pipeline[T] {
	declare[T]("pipeline", name)
	return Pipeline[T]{name}
}
func (p Pipeline[T]) Name() string { return p.name }

type pipeline struct {
	stages []*stage
	seq    uint64
}

type stage struct {
	s     *scope
	order int
	seq   uint64
	fn    func(any) (any, error)
}

// Transform adds fn to the chain, ordered by order then registration, until o
// is disposed.
func Transform[T any](o Owner, p Pipeline[T], order int, fn func(T) (T, error)) {
	s := o.owner()
	if !s.alive {
		return
	}
	pl := s.k.pipes[p.name]
	if pl == nil {
		pl = &pipeline{}
		s.k.pipes[p.name] = pl
	}
	pl.seq++
	st := &stage{s: s, order: order, seq: pl.seq, fn: func(v any) (any, error) { return fn(v.(T)) }}
	stages := append(slices.Clone(pl.stages), st)
	slices.SortStableFunc(stages, func(a, b *stage) int {
		if a.order != b.order {
			return cmp.Compare(a.order, b.order)
		}
		return cmp.Compare(a.seq, b.seq)
	})
	pl.stages = stages
	s.onDispose(func() {
		pl.stages = slices.DeleteFunc(slices.Clone(pl.stages), func(x *stage) bool { return x == st })
		if len(pl.stages) == 0 && s.k.pipes[p.name] == pl {
			delete(s.k.pipes, p.name)
		}
	})
}

// Run passes v through every transform. The first error stops the chain and
// is returned as a *PluginError. A panicking transform fails its plugin and is
// skipped.
func Run[T any](o Owner, p Pipeline[T], v T) (T, error) {
	k := o.owner().k
	pl := k.pipes[p.name]
	if pl == nil {
		return v, nil
	}
	cur := any(v)
	for _, st := range pl.stages {
		if !st.s.alive {
			continue
		}
		var next any
		var err error
		done := false
		k.call(st.s.plugin, func() { next, err = st.fn(cur); done = true })
		if err != nil {
			var zero T
			return zero, &PluginError{Plugin: st.s.plugin.id(), Err: err}
		}
		if done {
			cur = next
		}
	}
	out, _ := cur.(T)
	return out, nil
}

// ---- Extension points

// Point names a collection of contributions.
type Point[T any] struct{ name string }

func NewPoint[T any](name string) Point[T] {
	declare[T]("point", name)
	return Point[T]{name}
}
func (p Point[T]) Name() string { return p.name }

type Entry[T any] struct {
	ID     string // "<plugin-id>.<name>", unique within the point
	Plugin string // empty for the host
	Order  int
	Value  T
	s      *scope // exact registration lifetime, including the plugin activation
}

type point struct {
	entries []*entry
	ids     map[string]bool
	seq     uint64
	cache   any // []Entry[T], nil when stale
}

type entry struct {
	s     *scope
	id    string
	order int
	seq   uint64
	value any
}

// Contribute adds an entry until o is disposed. A plugin's IDs start with its
// ID and a dot; the host's start with HostPrefix.
func Contribute[T any](o Owner, p Point[T], id string, order int, v T) error {
	s := o.owner()
	if !s.alive {
		return errors.New("kernel: contribute on a disposed scope")
	}
	prefix := HostPrefix
	if s.plugin != nil {
		prefix = s.plugin.m.ID + "."
	}
	if !strings.HasPrefix(id, prefix) || len(id) == len(prefix) {
		return fmt.Errorf("kernel: entry ID %q must start with %q", id, prefix)
	}
	pt := s.k.points[p.name]
	if pt == nil {
		pt = &point{ids: map[string]bool{}}
		s.k.points[p.name] = pt
	}
	if pt.ids[id] {
		return fmt.Errorf("kernel: duplicate entry %q in %s", id, p.name)
	}
	pt.seq++
	e := &entry{s: s, id: id, order: order, seq: pt.seq, value: v}
	pt.entries = append(pt.entries, e)
	pt.ids[id] = true
	pt.cache = nil
	s.k.pointMoved(p.name)
	s.onDispose(func() {
		pt.entries = slices.DeleteFunc(pt.entries, func(x *entry) bool { return x == e })
		delete(pt.ids, id)
		pt.cache = nil
		if len(pt.entries) == 0 && s.k.points[p.name] == pt {
			delete(s.k.points, p.name)
		}
		s.k.pointMoved(p.name)
	})
	return nil
}

// pointMoved posts PointChanged for the point, unless it is already due.
func (k *Kernel) pointMoved(name string) {
	if k.closed || k.moved[name] {
		return
	}
	if len(k.moved) == 0 {
		k.opts.Post(k.announcePoints)
	}
	k.moved[name] = true
}

func (k *Kernel) announcePoints() {
	names := slices.Sorted(maps.Keys(k.moved))
	clear(k.moved)
	if k.closed {
		return
	}
	for _, name := range names {
		Emit(k, PointChanged, name)
	}
}

// Items returns the entries ordered by Order, then host before plugins in
// install order, then registration. The slice is cached until the point
// changes; callers must not modify it.
func Items[T any](o Owner, p Point[T]) []Entry[T] {
	pt := o.owner().k.points[p.name]
	if pt == nil {
		return nil
	}
	if pt.cache == nil {
		sorted := slices.Clone(pt.entries)
		slices.SortStableFunc(sorted, func(a, b *entry) int {
			switch {
			case a.order != b.order:
				return cmp.Compare(a.order, b.order)
			case a.s.plugin.rank() != b.s.plugin.rank():
				return cmp.Compare(a.s.plugin.rank(), b.s.plugin.rank())
			}
			return cmp.Compare(a.seq, b.seq)
		})
		items := make([]Entry[T], len(sorted))
		for i, e := range sorted {
			items[i] = Entry[T]{ID: e.id, Plugin: e.s.plugin.id(), Order: e.order, Value: e.value.(T), s: e.s}
		}
		pt.cache = items
	}
	return pt.cache.([]Entry[T])
}

// Invoke runs fn with an entry's value on behalf of the entry's plugin, so a
// panic fails that plugin instead of the host. The host calls command Run and
// slot Render callbacks through it. Only entries returned by Items on this
// kernel whose original registration scope is still alive can be invoked.
func Invoke[T any](o Owner, e Entry[T], fn func(T)) {
	s := o.owner()
	if !s.alive || e.s == nil || e.s.k != s.k || !e.s.alive {
		return
	}
	s.k.call(e.s.plugin, func() { fn(e.Value) })
}

// ---- Commands

type Command struct {
	Title   string
	Menu    string      // menu-bar menu, e.g. "View"; empty for a shortcut-only command
	Keys    []string    // fixed accelerators, e.g. "cmd+shift+k"; parsed by the shell
	Enabled func() bool // nil means always enabled
	Run     func()
}

var Commands = NewPoint[Command]("kernel.commands")
