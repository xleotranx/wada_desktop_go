// Package opencode is the OpenCode backend plugin. It runs `opencode serve`
// itself or connects to a server the user names, keeps that connection up,
// and provides the agent contract over its HTTP API and event stream.
package opencode

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
)

// Connection is the plugin's saved setting. The zero value runs a managed
// local server.
type Connection struct {
	Remote   bool   `json:"remote,omitempty"`
	URL      string `json:"url,omitempty"`
	Password string `json:"password,omitempty"`
}

// Plugin is the OpenCode backend. The zero value is ready to install.
type Plugin struct {
	ctx    *kernel.Context
	conn   *kernel.Setting[Connection]
	run    *kernel.Scope // the current supervisor; replaced to reconnect
	status agent.Status  // UI thread
	form   form

	mu       sync.Mutex
	client   *client // the ready connection, or nil
	attempts atomic.Uint64

	// Test seams.
	exe   string
	dir   string
	after func(time.Duration) <-chan time.Time
}

var (
	_ agent.Service = (*Plugin)(nil)
	_ agent.Models  = (*Plugin)(nil)
	_ agent.Modes   = (*Plugin)(nil)
)

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID:          "opencode",
		Title:       "OpenCode",
		Description: "Chat with OpenCode, run by the app or on a server you choose.",
		Provides:    []kernel.Named{agent.ServiceKey, agent.ModelsKey, agent.ModesKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.conn = kernel.NewSetting(c, "connection", Connection{})
	if p.exe == "" {
		p.exe = "opencode"
	}
	if p.dir == "" {
		p.dir, _ = os.UserHomeDir()
	}
	if p.after == nil {
		p.after = time.After
	}
	p.status = agent.Status{State: agent.Stopped}
	c.OnDispose(p.dropClient)
	for _, err := range []error{
		kernel.Provide[agent.Service](c, agent.ServiceKey, p),
		kernel.Provide[agent.Models](c, agent.ModelsKey, p),
		kernel.Provide[agent.Modes](c, agent.ModesKey, p),
		kernel.Contribute(c, kernel.Commands, "opencode.reconnect", 0, kernel.Command{
			Title: "Reconnect to OpenCode", Menu: "Agent", Run: p.Reconnect}),
		p.contributeSection(c),
	} {
		if err != nil {
			return err
		}
	}
	p.Reconnect()
	return nil
}

// Configure saves a new connection and reconnects with it.
func (p *Plugin) Configure(conn Connection) error {
	if conn.Remote {
		if _, err := apiBase(conn.URL); err != nil {
			return err
		}
	}
	if err := p.conn.Set(conn); err != nil {
		return err
	}
	p.Reconnect()
	return nil
}

// Connection returns the saved connection.
func (p *Plugin) Connection() Connection { return p.conn.Get() }

// Reconnect drops the current connection and starts again at once. Results
// of the old supervisor are dropped with its scope.
func (p *Plugin) Reconnect() {
	if p.run != nil {
		p.run.Dispose()
	}
	p.dropClient()
	p.run = p.ctx.NewScope()
	run, conn := p.run, p.conn.Get()
	run.Spawn(func(ctx context.Context) { p.supervise(ctx, run, conn) })
}

// Status reports the connection. UI thread only.
func (p *Plugin) Status() agent.Status { return p.status }

func (p *Plugin) setStatus(st agent.Status) {
	p.status = st
	kernel.Emit(p.ctx, agent.StatusChanged, st)
}

// adopt makes c the ready client unless ctx, the supervisor that found it,
// has already been replaced.
func (p *Plugin) adopt(ctx context.Context, c *client) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	p.client = c
	return true
}

// dropClient forgets the ready client and stops it.
func (p *Plugin) dropClient() {
	p.mu.Lock()
	old := p.client
	p.client = nil
	p.mu.Unlock()
	if old != nil {
		old.stop()
	}
}

func (p *Plugin) current() (*client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return nil, agent.ErrNotConnected
	}
	return p.client, nil
}

var backoff = []time.Duration{time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}

// supervise keeps one connection up until ctx ends: connect, stream events,
// and retry with backoff after a failure or drop.
func (p *Plugin) supervise(ctx context.Context, run *kernel.Scope, conn Connection) {
	post := func(st agent.Status) { run.Post(func() { p.setStatus(st) }) }
	for failures := 0; ctx.Err() == nil; {
		if failures > 0 {
			select {
			case <-ctx.Done():
				return
			case <-p.after(backoff[min(failures, len(backoff))-1]):
			}
		}
		attempt := p.attempts.Add(1)
		post(agent.Status{State: agent.Connecting, Attempt: attempt, Endpoint: conn.URL})
		c, err := p.open(ctx, conn)
		if err != nil {
			if ctx.Err() == nil {
				post(agent.Status{State: agent.Down, Attempt: attempt, Endpoint: conn.URL, Err: err})
			}
			failures++
			continue
		}
		err = p.stream(ctx, run, c, func() {
			if p.adopt(ctx, c) {
				post(agent.Status{State: agent.Ready, Attempt: attempt, Endpoint: c.endpoint(), Version: c.version})
			}
		})
		p.mu.Lock()
		if p.client == c {
			p.client = nil
		}
		p.mu.Unlock()
		c.stop()
		if ctx.Err() != nil {
			return
		}
		post(agent.Status{State: agent.Down, Attempt: attempt, Endpoint: c.endpoint(), Err: err})
		failures = 1 // a connection that worked retries soon
	}
}

func (p *Plugin) open(ctx context.Context, conn Connection) (*client, error) {
	if conn.Remote {
		return dial(ctx, conn)
	}
	return spawn(ctx, p.exe, p.dir)
}

const (
	batchEvents = 64
	batchBytes  = 256 << 10
	batchWait   = time.Second / 60
)

// stream follows c's events until it ends. Events are decoded and emitted in
// ordered batches on the UI thread. Only one batch is in flight at a time, so a
// busy UI slows the stream instead of queueing without bound. The first
// malformed event of a stream is reported in Status.Err.
func (p *Plugin) stream(ctx context.Context, run *kernel.Scope, c *client, opened func()) error {
	ctx, cancel := context.WithCancel(ctx)
	queue := make(chan agent.Event)
	result := make(chan error, 1)
	go func() {
		warned := false
		result <- c.events(ctx, opened, func(wire wireEvent) {
			ev, known, err := decodeEvent(wire)
			if err != nil && !warned {
				warned = true
				run.Post(func() {
					st := p.status
					st.Err = err
					p.setStatus(st)
				})
			}
			if err != nil || !known {
				return
			}
			select {
			case queue <- ev:
			case <-ctx.Done():
			}
		})
		close(queue)
	}()
	defer func() {
		cancel()
		for range queue { // let the reader exit
		}
	}()
	for {
		var batch []agent.Event
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-queue:
			if !ok {
				return <-result
			}
			batch = append(batch, ev)
		}
		size := batch[0].Bytes
		timer := time.NewTimer(batchWait)
	collect:
		for len(batch) < batchEvents && size < batchBytes {
			select {
			case ev, ok := <-queue:
				if !ok {
					break collect
				}
				batch = append(batch, ev)
				size += ev.Bytes
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		timer.Stop()
		applied := make(chan struct{})
		run.Post(func() {
			defer close(applied)
			kernel.Emit(p.ctx, agent.Events, batch)
		})
		select {
		case <-applied:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ---- agent.Service, agent.Models, agent.Modes

func (p *Plugin) Sessions(ctx context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	c, err := p.current()
	if err != nil {
		return agent.SessionPage{}, err
	}
	return c.sessions(ctx, q)
}

func (p *Plugin) Create(ctx context.Context, req agent.CreateSession) (agent.Session, error) {
	c, err := p.current()
	if err != nil {
		return agent.Session{}, err
	}
	return c.create(ctx, req)
}

func (p *Plugin) Messages(ctx context.Context, id agent.SessionID) ([]agent.Message, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	return c.messages(ctx, id)
}

func (p *Plugin) Send(ctx context.Context, req agent.Send) error {
	return p.with(func(c *client) error { return c.send(ctx, req) })
}

func (p *Plugin) Abort(ctx context.Context, id agent.SessionID) error {
	return p.with(func(c *client) error { return c.abort(ctx, id) })
}

func (p *Plugin) Reply(ctx context.Context, req agent.Reply) error {
	return p.with(func(c *client) error { return c.reply(ctx, req) })
}

func (p *Plugin) Rename(ctx context.Context, id agent.SessionID, title string) error {
	return p.with(func(c *client) error { return c.rename(ctx, id, title) })
}

func (p *Plugin) Delete(ctx context.Context, id agent.SessionID) error {
	return p.with(func(c *client) error { return c.delete(ctx, id) })
}

func (p *Plugin) Models(ctx context.Context) ([]agent.Model, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	return c.models(ctx)
}

func (p *Plugin) DefaultModel(ctx context.Context) (agent.ModelRef, error) {
	c, err := p.current()
	if err != nil {
		return agent.ModelRef{}, err
	}
	return c.defaultModel(ctx)
}

func (p *Plugin) SetModel(ctx context.Context, id agent.SessionID, m agent.ModelRef) error {
	if m.IsZero() {
		return errors.New("no model given")
	}
	return p.with(func(c *client) error { return c.setModel(ctx, id, m) })
}

func (p *Plugin) Modes(ctx context.Context) ([]agent.Mode, error) {
	c, err := p.current()
	if err != nil {
		return nil, err
	}
	return c.modes(ctx)
}

func (p *Plugin) with(fn func(*client) error) error {
	c, err := p.current()
	if err != nil {
		return err
	}
	return fn(c)
}
