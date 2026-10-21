// Package pi is the Pi backend plugin. Pi's RPC mode drives one session per
// process, and Pi keeps sessions as JSONL files, so this plugin:
//
//   - keeps a control process (`pi --mode rpc --no-session`) for the model
//     catalog and as the connection's health;
//   - starts a process per conversation when it is first used, and stops idle
//     ones beyond a small limit;
//   - lists and reads conversations from the session files directly.
package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
)

// Settings is the plugin's saved configuration. Empty values use Pi's defaults.
type Settings struct {
	Executable string `json:"executable,omitempty"`
	SessionDir string `json:"sessionDir,omitempty"`
}

// Plugin is the Pi backend. The zero value is ready to install.
type Plugin struct {
	ctx       *kernel.Context
	cfg       *kernel.Setting[Settings]
	run       *kernel.Scope
	status    agent.Status // UI thread
	form      Settings     // the settings section's unsaved edit
	formDirty bool

	attempts atomic.Uint64
	mu       sync.Mutex
	conn     *connection // the ready connection, or nil

	// Test seams.
	after   func(time.Duration) <-chan time.Time
	maxIdle int
}

// connection is everything one supervisor run owns.
type connection struct {
	ctx      context.Context
	exe, dir string // executable and session directory
	home     string
	control  *proc
	version  string
	maxIdle  int
	queue    chan agent.Event

	mu       sync.Mutex
	sessions map[agent.SessionID]*live
	dialogs  map[agent.InteractionID]dialog
	files    catalog
}

// live is one conversation's process.
type live struct {
	p    *proc
	t    *translator // its session is set once, before the first prompt
	used time.Time
	busy atomic.Bool
}

type dialog struct {
	session agent.SessionID
	method  string
	request agent.Interaction
}

var (
	_ agent.Service = (*Plugin)(nil)
	_ agent.Models  = (*Plugin)(nil)
)

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "pi", Title: "Pi", DefaultOff: true,
		Description: "Chat with the Pi coding agent, run by the app.",
		Provides:    []kernel.Named{agent.ServiceKey, agent.ModelsKey, agent.FileTypesKey, agent.InteractionsKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.cfg = kernel.NewSetting(c, "settings", Settings{})
	if p.after == nil {
		p.after = time.After
	}
	if p.maxIdle == 0 {
		p.maxIdle = 4
	}
	p.status = agent.Status{State: agent.Stopped}
	c.OnDispose(p.drop)
	for _, err := range []error{
		kernel.Provide[agent.Service](c, agent.ServiceKey, p),
		kernel.Provide[agent.Models](c, agent.ModelsKey, p),
		kernel.Provide[agent.Interactions](c, agent.InteractionsKey, p),
		kernel.Provide[agent.FileTypes](c, agent.FileTypesKey, fileTypes{}),
		kernel.Contribute(c, kernel.Commands, "pi.reconnect", 0, kernel.Command{
			Title: "Restart Pi", Menu: "Agent", Run: p.Reconnect}),
		p.contributeSection(c),
	} {
		if err != nil {
			return err
		}
	}
	p.Reconnect()
	return nil
}

// Reconnect stops every Pi process and starts again.
func (p *Plugin) Reconnect() {
	if p.run != nil {
		p.run.Dispose()
	}
	p.drop()
	p.run = p.ctx.NewScope()
	run, cfg := p.run, p.cfg.Get()
	home, _ := os.UserHomeDir()
	c := &connection{ctx: run.Lifetime(), exe: cmpOr(cfg.Executable, "pi"), home: home, maxIdle: p.maxIdle,
		queue: make(chan agent.Event, 256), sessions: map[agent.SessionID]*live{}, dialogs: map[agent.InteractionID]dialog{}}
	c.dir = cmpOr(cfg.SessionDir, defaultSessionDir(home))
	c.files.dir = c.dir
	run.Spawn(func(ctx context.Context) { p.batch(ctx, run, c.queue) })
	run.Spawn(func(ctx context.Context) { p.supervise(ctx, run, c) })
}

// Configure saves new settings and restarts Pi with them.
func (p *Plugin) Configure(s Settings) error {
	if err := p.cfg.Set(s); err != nil {
		return err
	}
	p.Reconnect()
	return nil
}

func (p *Plugin) Status() agent.Status { return p.status }

func (p *Plugin) setStatus(st agent.Status) {
	p.status = st
	kernel.Emit(p.ctx, agent.StatusChanged, st)
}

// drop forgets the connection at once, so calls fail with ErrNotConnected
// while its supervisor winds down.
func (p *Plugin) drop() {
	p.mu.Lock()
	p.conn = nil
	p.mu.Unlock()
}

func (p *Plugin) current() (*connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil, agent.ErrNotConnected
	}
	return p.conn, nil
}

var backoff = []time.Duration{time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}

func defaultSessionDir(home string) string {
	if dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// supervise keeps the control process up until ctx ends, retrying with backoff.
func (p *Plugin) supervise(ctx context.Context, run *kernel.Scope, c *connection) {
	defer c.closeAll()
	post := func(st agent.Status) { run.Post(func() { p.setStatus(st) }) }
	endpoint := "Pi on this Mac"
	for failures := 0; ctx.Err() == nil; {
		if failures > 0 {
			select {
			case <-ctx.Done():
				return
			case <-p.after(backoff[min(failures, len(backoff))-1]):
			}
		}
		attempt := p.attempts.Add(1)
		post(agent.Status{State: agent.Connecting, Attempt: attempt, Endpoint: endpoint})
		if c.version == "" {
			if out, err := exec.CommandContext(ctx, c.exe, "--version").Output(); err == nil {
				c.version = strings.TrimSpace(string(out))
			}
		}
		control, err := start(ctx, c.exe, c.home, []string{"--no-session", "--session-dir", c.dir}, c.controlEvents)
		if err == nil {
			err = control.call(ctx, "get_state", nil, nil)
			if err != nil {
				control.close()
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				post(agent.Status{State: agent.Down, Attempt: attempt, Endpoint: endpoint, Err: err})
			}
			failures++
			continue
		}
		c.control = control
		p.mu.Lock()
		if ctx.Err() == nil {
			p.conn = c
		}
		p.mu.Unlock()
		post(agent.Status{State: agent.Ready, Attempt: attempt, Endpoint: endpoint, Version: c.version})
		select {
		case <-ctx.Done():
		case <-control.done:
		}
		p.mu.Lock()
		if p.conn == c {
			p.conn = nil
		}
		p.mu.Unlock()
		control.close()
		if ctx.Err() != nil {
			return
		}
		post(agent.Status{State: agent.Down, Attempt: attempt, Endpoint: endpoint, Err: control.exitErr()})
		failures = 1
	}
}

// controlEvents declines dialogs the control process raises; it has no
// conversation to show them in.
func (c *connection) controlEvents(pr *proc, kind string, raw []byte) {
	var w dialogRequest
	if kind != "extension_ui_request" || json.Unmarshal(raw, &w) != nil {
		return
	}
	switch w.Method {
	case "confirm", "select", "input", "editor":
		pr.send(dialogReply(w.ID, w.Method, agent.InteractionAnswer{Cancelled: true}))
	}
}

// ensure returns the process of a conversation, starting it if needed.
func (c *connection) ensure(id agent.SessionID) (*live, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l := c.sessions[id]; l != nil {
		select {
		case <-l.p.done:
			delete(c.sessions, id)
		default:
			l.used = time.Now()
			return l, nil
		}
	}
	f, err := c.findLocked(id)
	if err != nil {
		return nil, err
	}
	return c.spawnLocked(id, f.session.Directory, []string{"--session", f.path})
}

// spawnLocked starts a process for a conversation (id may be empty for a new
// one; register sets it). Callers hold c.mu.
func (c *connection) spawnLocked(id agent.SessionID, dir string, args []string) (*live, error) {
	if info, err := os.Stat(dir); dir == "" || err != nil || !info.IsDir() {
		dir = c.home // the folder is gone; Pi still opens the session file
	}
	l := &live{used: time.Now()}
	t := &translator{session: id}
	pr, err := start(c.ctx, c.exe, dir, append(args, "--session-dir", c.dir), func(pr *proc, kind string, raw []byte) {
		c.sessionEvent(pr, l, t, kind, raw)
	})
	if err != nil {
		return nil, err
	}
	l.p, l.t = pr, t
	if id != "" {
		c.sessions[id] = l
		c.reapLocked(c.maxIdle)
	}
	return l, nil
}

func (c *connection) sessionEvent(pr *proc, l *live, t *translator, kind string, raw []byte) {
	if kind == "agent_settled" {
		id := t.sessionID()
		c.mu.Lock()
		for key, pending := range c.dialogs {
			if pending.session == id {
				delete(c.dialogs, key)
			}
		}
		c.mu.Unlock()
	}
	if kind == "extension_ui_request" {
		var w dialogRequest
		if json.Unmarshal(raw, &w) == nil {
			if request, ok := w.interaction(); ok {
				c.mu.Lock()
				c.dialogs[request.ID] = dialog{session: t.sessionID(), method: w.Method, request: request}
				c.mu.Unlock()
			}
		}
	}
	events := t.translate(kind, raw)
	l.busy.Store(t.isRunning())
	for _, ev := range events {
		select {
		case c.queue <- ev:
		case <-c.ctx.Done():
			return
		}
	}
}

// reapLocked stops the least recently used idle processes beyond max.
func (c *connection) reapLocked(max int) {
	var idle []agent.SessionID
	for id, l := range c.sessions {
		if !l.busy.Load() {
			idle = append(idle, id)
		}
	}
	if len(idle) <= max {
		return
	}
	slices.SortFunc(idle, func(a, b agent.SessionID) int { return c.sessions[a].used.Compare(c.sessions[b].used) })
	for _, id := range idle[:len(idle)-max] {
		l := c.sessions[id]
		delete(c.sessions, id)
		go l.p.close()
	}
}

func (c *connection) findLocked(id agent.SessionID) (sessionFile, error) {
	for _, f := range c.files.cache {
		if f.session.ID == id {
			return f, nil
		}
	}
	files, _ := c.files.scan()
	for _, f := range files {
		if f.session.ID == id {
			return f, nil
		}
	}
	return sessionFile{}, errors.New("the Pi session file was not found")
}

func (c *connection) closeAll() {
	c.mu.Lock()
	clear(c.dialogs)
	procs := make([]*proc, 0, len(c.sessions))
	for id, l := range c.sessions {
		procs = append(procs, l.p)
		delete(c.sessions, id)
	}
	c.mu.Unlock()
	for _, pr := range procs {
		pr.close()
	}
}

// emit queues a synthetic event, such as the confirmation of a model switch.
func (c *connection) emit(ev agent.Event) {
	select {
	case c.queue <- ev:
	case <-c.ctx.Done():
	}
}

const (
	batchEvents = 64
	batchBytes  = 256 << 10
	batchWait   = time.Second / 60
)

// batch emits queued events in ordered batches, one batch in flight at a time.
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
		timer := time.NewTimer(batchWait)
	collect:
		for len(batch) < batchEvents && size < batchBytes {
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
		run.Post(func() {
			defer close(applied)
			kernel.Emit(p.ctx, agent.Events, batch)
		})
		select {
		case <-applied:
		case <-ctx.Done():
			return
		}
	}
}
