package kernel

import (
	"cmp"
	"errors"
	"slices"
)

// Inspector is read-only and UI-thread-only. The host exposes the kernel through
// InspectorKey so a diagnostics plugin needs no private access to the shell.
type Inspector interface{ Inspect() []Diagnostic }

var InspectorKey = NewKey[Inspector]("kernel.inspector")

type Dependency struct{ Key, Provider string }
type Registration struct{ Kind, Name, ID string }
type Diagnostic struct {
	Status        Status
	Requires      []Dependency
	Scopes        int   // active scopes, including the plugin root
	Workers       int64 // work still running, including cancelled work from older scopes
	Registrations []Registration
	Stack         string // recovered panic stack, when present
}

// Inspect returns a snapshot in install order. It performs no I/O or mutation.
func (k *Kernel) Inspect() []Diagnostic {
	statuses := k.Plugins()
	out := make([]Diagnostic, len(k.plugins))
	for i, p := range k.plugins {
		d := Diagnostic{Status: statuses[i], Workers: p.workers.Load()}
		var panicErr *PanicError
		if errors.As(p.err, &panicErr) {
			d.Stack = string(panicErr.Stack)
		}
		for _, key := range p.m.Requires {
			dependency := Dependency{Key: key.Name()}
			if service := k.services[key.Name()]; service != nil {
				dependency.Provider = service.by.id()
			}
			d.Requires = append(d.Requires, dependency)
		}
		if p.ctx != nil {
			d.Scopes = countScopes(p.ctx.s)
		}
		for name, topic := range k.topics {
			for _, listener := range topic.listeners {
				if listener.s.plugin == p {
					d.Registrations = append(d.Registrations, Registration{Kind: "listener", Name: name})
				}
			}
		}
		for name, pipe := range k.pipes {
			for _, stage := range pipe.stages {
				if stage.s.plugin == p {
					d.Registrations = append(d.Registrations, Registration{Kind: "transform", Name: name})
				}
			}
		}
		for name, point := range k.points {
			for _, entry := range point.entries {
				if entry.s.plugin == p {
					d.Registrations = append(d.Registrations, Registration{Kind: "contribution", Name: name, ID: entry.id})
				}
			}
		}
		for name, service := range k.services {
			if service.by == p {
				d.Registrations = append(d.Registrations, Registration{Kind: "service", Name: name})
			}
		}
		slices.SortFunc(d.Registrations, func(a, b Registration) int {
			if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Name, b.Name); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
		out[i] = d
	}
	return out
}

func countScopes(s *scope) int {
	n := 1
	for _, child := range s.children {
		n += countScopes(child)
	}
	return n
}
