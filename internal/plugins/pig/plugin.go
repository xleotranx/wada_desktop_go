// Package pig embeds the PiG coding-agent SDK behind Wada's agent contract.
package pig

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

const version = "0.5.3"

var attempts atomic.Uint64

// Plugin owns UI state; engine owns the synchronized SDK state used by workers.
type Plugin struct {
	ctx       *kernel.Context
	host      shell.Host
	run       *kernel.Scope
	status    agent.Status
	mu        sync.Mutex
	engine    *engine
	providers providerForm
}

func New() kernel.Plugin { return &Plugin{} }
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "pig", Title: "PiG", Description: "Embedded coding agent, with Wada-owned credentials and conversations.",
		Requires: []kernel.Named{shell.HostKey}, Provides: []kernel.Named{agent.ServiceKey, agent.ModelsKey, agent.ModesKey, agent.ChildrenKey, agent.SnapshotsKey, agent.FilesKey, agent.SkillsKey, agent.FileTypesKey, agent.ImagesKey, agent.RewindKey, agent.ChangesKey, agent.InteractionsKey}}
}
func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	kernel.Provide[agent.Service](c, agent.ServiceKey, p)
	kernel.Provide[agent.Models](c, agent.ModelsKey, p)
	kernel.Provide[agent.Modes](c, agent.ModesKey, p)
	kernel.Provide[agent.Snapshots](c, agent.SnapshotsKey, p)
	kernel.Provide[agent.Children](c, agent.ChildrenKey, p)
	kernel.Provide[agent.Files](c, agent.FilesKey, p)
	kernel.Provide[agent.Skills](c, agent.SkillsKey, p)
	kernel.Provide[agent.FileTypes](c, agent.FileTypesKey, fileTypes{})
	kernel.Provide[agent.Images](c, agent.ImagesKey, p)
	kernel.Provide[agent.Rewinder](c, agent.RewindKey, p)
	kernel.Provide[agent.Changes](c, agent.ChangesKey, p)
	kernel.Provide[agent.Interactions](c, agent.InteractionsKey, p)
	c.OnDispose(p.drop)
	kernel.Contribute(c, kernel.Commands, "pig.restart", 0, kernel.Command{Title: "Restart PiG", Menu: "Agent", Run: p.Restart})
	p.contributeSettings(c)
	p.Restart()
	return nil
}
func (p *Plugin) Status() agent.Status { return p.status }
func (p *Plugin) setStatus(st agent.Status) {
	p.status = st
	kernel.Emit(p.ctx, agent.StatusChanged, st)
}
func (p *Plugin) drop() { p.mu.Lock(); p.engine = nil; p.mu.Unlock() }
func (p *Plugin) current() (*engine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine == nil || p.engine.ctx.Err() != nil {
		return nil, agent.ErrNotConnected
	}
	return p.engine, nil
}
func (p *Plugin) Restart() {
	p.cancelLogin()
	p.providers.load.Cancel()
	p.providers.list = nil
	if p.run != nil {
		p.run.Dispose()
	}
	p.drop()
	p.run = p.ctx.NewScope()
	run := p.run
	root := filepath.Join(p.host.DataDir(), "pig")
	st := agent.Status{State: agent.Connecting, Attempt: attempts.Add(1), Endpoint: "Embedded PiG", Version: version, Origin: "pig " + root}
	p.setStatus(st)
	run.Spawn(func(ctx context.Context) {
		e, err := newEngine(ctx, root, run.Spawn)
		if err != nil {
			run.Post(func() {
				st.State = agent.Down
				st.Err = errors.New("could not open PiG storage: " + err.Error())
				p.setStatus(st)
			})
			return
		}
		defer e.close()
		e.modelsChanged = func() { run.Post(func() { kernel.Emit(p.ctx, agent.ModelsChanged, struct{}{}); p.loadProviders() }) }
		off := e.models.SetChangeListener(e.modelsChanged)
		defer off()
		p.mu.Lock()
		if ctx.Err() == nil {
			p.engine = e
		}
		p.mu.Unlock()
		run.Post(func() { st.State = agent.Ready; p.setStatus(st); p.loadProviders() })
		p.batch(ctx, run, e.events)
	})
}
func (p *Plugin) batch(ctx context.Context, run *kernel.Scope, queue <-chan agent.Event) {
	for {
		var batch []agent.Event
		select {
		case <-ctx.Done():
			return
		case ev := <-queue:
			batch = append(batch, ev)
		}
		size := batch[0].Bytes
		timer := time.NewTimer(time.Second / 60)
	collect:
		for len(batch) < 64 && size < 256<<10 {
			select {
			case ev := <-queue:
				batch = append(batch, ev)
				size += ev.Bytes
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		applied := make(chan struct{})
		// Most batches change what is not on show, as a conversation in
		// the background; their listeners ask for a frame when they do.
		run.PostQuiet(func() { defer close(applied); kernel.Emit(p.ctx, agent.Events, batch) })
		select {
		case <-applied:
		case <-ctx.Done():
			return
		}
	}
}

// acquire prevents shutdown from closing SDK collaborators during a service call.
func (p *Plugin) acquire() (*engine, func(), error) {
	e, err := p.current()
	if err != nil {
		return nil, func() {}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return nil, func() {}, agent.ErrNotConnected
	}
	e.active.Add(1)
	return e, func() { e.trimIdle(); e.active.Done() }, nil
}
