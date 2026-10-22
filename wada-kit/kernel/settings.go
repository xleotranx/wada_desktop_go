package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Store persists settings as (namespace, key) -> JSON value.
type Store interface {
	Load(ctx context.Context) ([]Value, error)
	// Save writes changed values in one transaction. A nil JSON deletes the key.
	// The kernel calls it off the UI thread, one call at a time.
	Save(ctx context.Context, changed []Value) error
}

type Value struct {
	Namespace string // plugin ID, or "kernel" for enablement
	Key       string
	JSON      json.RawMessage
}

type valueKey struct{ ns, key string }

type pendingSave struct {
	batch []Value
	done  chan error
}

// Settings stores JSON values namespaced to a plugin. Values survive disable,
// re-enable and restart.
type Settings struct {
	k  *Kernel
	ns string
}

// Get decodes the stored value into into and reports whether one was decoded.
func (s *Settings) Get(key string, into any) bool { return s.k.get(s.ns, key, into) }

// Set stores v, encoded as JSON, and schedules a save.
func (s *Settings) Set(key string, v any) error { return s.k.set(s.ns, key, v) }

// Delete removes the stored value.
func (s *Settings) Delete(key string) {
	k := s.k
	if _, ok := k.values[s.ns][key]; !ok {
		return
	}
	delete(k.values[s.ns], key)
	vk := valueKey{s.ns, key}
	delete(k.dirty, vk)
	k.deleted[vk] = true
	k.save()
}

// Setting is a typed setting with a default.
type Setting[T any] struct {
	s   *Settings
	key string
	def T
}

func NewSetting[T any](c *Context, key string, def T) *Setting[T] {
	return &Setting[T]{s: c.Settings(), key: key, def: def}
}

// Get returns the stored value, or the default when none decodes.
func (s *Setting[T]) Get() T {
	var v T
	if s.s.Get(s.key, &v) {
		return v
	}
	return s.def
}

func (s *Setting[T]) Set(v T) error { return s.s.Set(s.key, v) }

func (k *Kernel) load() error {
	values, err := k.opts.Store.Load(k.root.ctx)
	if err != nil {
		return err
	}
	for _, v := range values {
		if k.values[v.Namespace] == nil {
			k.values[v.Namespace] = map[string][]byte{}
		}
		k.values[v.Namespace][v.Key] = slices.Clone(v.JSON)
	}
	return nil
}

func (k *Kernel) get(ns, key string, into any) bool {
	raw, ok := k.values[ns][key]
	return ok && json.Unmarshal(raw, into) == nil
}

func (k *Kernel) set(ns, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if old, ok := k.values[ns][key]; ok && bytes.Equal(old, raw) {
		return nil
	}
	if k.values[ns] == nil {
		k.values[ns] = map[string][]byte{}
	}
	k.values[ns][key] = raw
	vk := valueKey{ns, key}
	delete(k.deleted, vk)
	k.dirty[vk] = raw
	k.save()
	return nil
}

// save writes pending changes unless a save is in flight; changes made in the
// meantime are written when it finishes.
func (k *Kernel) save() {
	if k.saving != nil || k.closed || !k.started {
		return
	}
	batch := k.takeDirty()
	if len(batch) == 0 {
		return
	}
	pending := &pendingSave{batch: batch, done: make(chan error, 1)}
	k.saving = pending
	k.workers.Add(1)
	ctx := k.root.ctx
	k.opts.Background(func() {
		pending.done <- k.opts.Store.Save(context.WithoutCancel(ctx), batch)
		k.workers.Done()
		k.opts.Post(func() {
			if k.closed {
				return
			}
			err := <-pending.done
			k.saving = nil
			if err != nil {
				k.restoreDirty(batch)
				Emit(k, SaveFailed, err)
				return
			}
			k.save()
		})
	})
}

func (k *Kernel) takeDirty() []Value {
	var batch []Value
	for vk, raw := range k.dirty {
		batch = append(batch, Value{Namespace: vk.ns, Key: vk.key, JSON: raw})
	}
	for vk := range k.deleted {
		batch = append(batch, Value{Namespace: vk.ns, Key: vk.key})
	}
	clear(k.dirty)
	clear(k.deleted)
	slices.SortFunc(batch, func(a, b Value) int {
		if a.Namespace != b.Namespace {
			return strings.Compare(a.Namespace, b.Namespace)
		}
		return strings.Compare(a.Key, b.Key)
	})
	return batch
}

// restoreDirty re-queues a failed batch, keeping any newer change.
func (k *Kernel) restoreDirty(batch []Value) {
	for _, v := range batch {
		vk := valueKey{v.Namespace, v.Key}
		if _, newer := k.dirty[vk]; newer || k.deleted[vk] {
			continue
		}
		if v.JSON == nil {
			k.deleted[vk] = true
		} else {
			k.dirty[vk] = v.JSON
		}
	}
}
