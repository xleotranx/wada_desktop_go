// Package workflow runs session-owned conversational tasks. All mutable task
// state belongs to the UI thread; workers receive immutable requests and values.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
)

type Plugin struct {
	ctx       *kernel.Context
	agent     agent.Service
	children  agent.Children
	snapshots agent.Snapshots
	host      shell.Host
	lanes     map[agent.SessionID]*lane
	store     *store
	loaded    bool
	loadError string
	sequence  uint64
	expanded  map[string]bool
	restored  []savedLane
	watching  map[agent.SessionID]*lane
}

func New() kernel.Plugin { return &Plugin{} }
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "workflow", Title: "Workflow engine", Description: "Conversational tasks with independent review.", Requires: []kernel.Named{agent.ServiceKey, agent.ChildrenKey, agent.SnapshotsKey, shell.HostKey}, Provides: []kernel.Named{chat.DeliveryKey}}
}
func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.children = kernel.Require(c, agent.ChildrenKey)
	p.snapshots = kernel.Require(c, agent.SnapshotsKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.lanes = map[agent.SessionID]*lane{}
	p.watching = map[agent.SessionID]*lane{}
	p.expanded = map[string]bool{}
	p.loaded = p.host.DataDir() == ""
	if !p.loaded {
		p.store = newStore(filepath.Join(p.host.DataDir(), "workflow.db"))
		store := p.store
		c.Spawn(func(ctx context.Context) {
			store.run(ctx, func(records []savedLane, err error) {
				c.Post(func() {
					p.loaded = true
					if err != nil {
						p.loadError = err.Error()
						return
					}
					p.restored = records
					p.restore()
					c.Invalidate()
				})
			}, func(err error) {
				c.Post(func() {
					p.loadError = err.Error()
					p.host.Toast(ui.Toast{Title: "Workflow history could not be saved", Description: err.Error()})
				})
			})
		})
	}
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, chat.RequestAnswered, func(answer chat.Answered) {
		if l := p.watching[answer.Session]; l != nil {
			l.requests = slices.DeleteFunc(l.requests, func(ev agent.Event) bool {
				return (ev.Approval != nil && ev.Approval.ID == answer.Approval) || (ev.Interaction != nil && ev.Interaction.ID == answer.Interaction)
			})
		}
	})
	kernel.On(c, agent.Deleted, func(id agent.SessionID) {
		if l := p.lanes[id]; l != nil {
			if l.Pending != nil {
				delete(p.watching, l.Pending.Request.Session)
			}
			l.scope.Dispose()
			delete(p.lanes, id)
			if p.store != nil {
				p.store.remove(l.Origin, id)
			}
		}
	})
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready {
			p.restore()
		}
		if st.State != agent.Ready {
			for _, l := range p.lanes {
				if l.Working {
					p.Stop(l.Session)
					l.Error = "The backend disconnected. Resume explicitly."
				}
			}
		}
	})
	kernel.On(c, chat.Changed, func(conv chat.Conversation) {
		if l := p.lanes[conv.Session]; l != nil {
			p.requests(l)
		}
	})
	kernel.Contribute(c, chat.Activities, "workflow.tasks", 0, chat.Activity{Items: p.activities})
	return kernel.Provide[chat.Delivery](c, chat.DeliveryKey, p)
}
func (p *Plugin) flow(id string) (wf.Flow, bool) {
	for _, e := range kernel.Items(p.ctx, wf.Definitions) {
		if e.ID == id {
			return e.Value, true
		}
	}
	return wf.Flow{}, false
}
func (p *Plugin) Validate(d chat.Draft) error {
	if !p.loaded {
		return errors.New("workflow history is still loading")
	}
	if p.loadError != "" {
		return fmt.Errorf("workflow storage: %s", p.loadError)
	}
	if p.agent.Status().State != agent.Ready {
		return agent.ErrNotConnected
	}
	if d.Workflow != "" {
		if _, ok := p.flow(d.Workflow); !ok {
			return errors.New("the selected workflow is unavailable")
		}
	}
	return nil
}
func (p *Plugin) Admit(d chat.Draft) error {
	if err := p.Validate(d); err != nil {
		return err
	}
	if d.Session == "" {
		return errors.New("delivery needs a created conversation")
	}
	// Freeze composer-owned arrays and file data before accepting another message.
	d.Files = slices.Clone(d.Files)
	for i := range d.Files {
		d.Files[i].Data = slices.Clone(d.Files[i].Data)
	}
	d.Skills = slices.Clone(d.Skills)
	l := p.lanes[d.Session]
	if l == nil {
		l = p.newLane(savedLane{Session: d.Session, Origin: p.agent.Status().Origin, Directory: d.Directory})
		p.lanes[d.Session] = l
	}
	l.Queue = append(slices.Clone(l.Queue), d)
	l.Halted = false
	p.save(l)
	p.advance(l)
	return nil
}
func (p *Plugin) Busy(id agent.SessionID) bool {
	l := p.lanes[id]
	return l != nil && (l.Working || (!l.Halted && (len(l.Queue) > 0 || l.runningTask())))
}
func (p *Plugin) changed(l *lane) {
	kernel.Emit(p.ctx, chat.DeliveryChanged, l.Session)
	p.ctx.Invalidate()
}
func (p *Plugin) save(l *lane) {
	if p.store != nil {
		p.store.put(l.saved())
	}
	p.changed(l)
}
func (p *Plugin) fail(l *lane, err error) {
	l.Working = false
	l.Halted = true
	if l.Pending != nil && p.watching[l.Pending.Request.Session] == l {
		delete(p.watching, l.Pending.Request.Session)
	}
	l.Error = err.Error()
	if t := l.task(); t != nil && l.Pending != nil && l.Pending.TaskID == t.ID {
		t.State = "failed"
		if l.Pending.Phase == "review" {
			t.Outcome = "Review incomplete"
		}
	}
	p.save(l)
	kernel.Emit(p.ctx, chat.DeliveryError, chat.DeliveryFailure{Session: l.Session, Text: err.Error()})
}
func (p *Plugin) Stop(id agent.SessionID) {
	l := p.lanes[id]
	if l == nil {
		return
	}
	l.Halted = true
	if l.Pending != nil && p.watching[l.Pending.Request.Session] == l {
		delete(p.watching, l.Pending.Request.Session)
	}
	kernel.Emit(p.ctx, chat.Incoming, []agent.Event{{Session: id, Kind: agent.TurnInterrupted}})
	l.requests = nil
	if t := l.task(); t != nil && t.State == "running" {
		t.State = "stopped"
	}
	l.job.Cancel()
	if l.Pending == nil || l.Pending.Request.Session == "" {
		l.Working = false
		p.save(l)
		return
	}
	target := l.Pending.Request.Session
	l.Working = false
	l.stopping = true
	backend := p.agent
	var stop kernel.Task
	abort := l.abort
	kernel.Go(p.ctx, &stop, func(context.Context) (struct{}, error) {
		if abort.Swap(nil) == nil {
			return struct{}{}, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return struct{}{}, backend.Abort(ctx, target)
	}, func(_ struct{}, err error) {
		l.stopping = false
		if err != nil {
			l.Error = "The agent may still be running: " + err.Error()
		}
		p.save(l)
	})
	p.save(l)
}
func (p *Plugin) resume(l *lane, t *task) {
	if l.Working || l.stopping {
		return
	}
	l.Halted = false
	l.Error = ""
	if t != nil {
		l.Current = t.ID
		l.Associated = t.ID
		t.State = "running"
	}
	if l.Pending != nil && (t == nil || l.Pending.TaskID == t.ID) {
		// Retain partial child IDs and the exact prepared request. Retry is explicit.
		p.execute(l, l.Pending)
	} else {
		l.Pending = nil
		p.advance(l)
	}
	p.save(l)
}
func (p *Plugin) requests(l *lane) {
	for _, ev := range l.requests {
		copy := ev
		copy.Session = l.Session
		kernel.Emit(p.ctx, chat.Incoming, []agent.Event{copy})
	}
}
func (p *Plugin) newID() string {
	p.sequence++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), p.sequence)
}

// A lifetime worker performs best-effort cancellation even after UI registrations
// have been disposed. It reads only an atomic immutable session ID.
func (p *Plugin) newLane(record savedLane) *lane {
	scope := p.ctx.NewScope()
	abort := new(atomic.Pointer[agent.SessionID])
	backend := p.agent
	scope.Spawn(func(ctx context.Context) {
		<-ctx.Done()
		if id := abort.Swap(nil); id != nil {
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = backend.Abort(stop, *id)
		}
	})
	return &lane{savedLane: record, scope: scope, abort: abort}
}

func (p *Plugin) restore() {
	if p.agent.Status().State != agent.Ready {
		return
	}
	for _, record := range p.restored {
		if record.Origin != p.agent.Status().Origin {
			continue
		}
		if p.lanes[record.Session] != nil {
			continue
		}
		l := p.newLane(record)
		if l.Pending != nil || l.runningTask() {
			l.Halted = true
			l.Error = "Execution was interrupted. Resume explicitly before continuing."
			if t := l.task(); t != nil {
				t.State = "interrupted"
			}
		}
		p.lanes[l.Session] = l
	}
}
