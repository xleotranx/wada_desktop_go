// Package runner schedules one active OpenCode connection without owning UI state.
package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"
)

type Attempt uint64

type Connection struct {
	Projects            bool
	ID, Name, Directory string
	Address             string
	Open                func(context.Context) (agent.Client, error)
	Pins                []string
}
type Origin struct {
	Session chat.SessionID
	Draft   uint64
	IsDraft bool
}
type Failure struct {
	Origin Origin
	Text   string
}

type Status struct {
	State   string
	Facts   agent.Facts
	Address string
	Err     error
}
type Page struct {
	Sessions     []agent.Session
	Cursor       string
	More, Pinned bool
	Err          error
}
type Catalog struct {
	DefaultErr error

	Models  []agent.Model
	Default agent.ModelRef
	Err     error

	Agents    []agent.Persona
	AgentsErr error
}
type Opened struct {
	Revision uint64

	Session  string
	Messages []agent.Message
}
type Created struct {
	Session agent.Session
	Draft   uint64
}

// Sink runs only through the injected dispatcher, after ownership validation.
type Sink interface {
	Status(Status)
	Sessions(Page)
	Catalog(Catalog)
	Projects([]string)
	Updates([]agent.Update)
	Opened(Opened)
	Created(Created)
	Answered(string)
	Deleted(string)
	Usage(agent.Message)
	Notice(string)
	Failed(Failure)
}

type Options struct {
	Start    func(context.Context, Connection) (agent.Client, error)
	After    func(time.Duration) <-chan time.Time
	Capacity int
}

type selected struct {
	connection Connection
	generation Generation
}
type queued struct {
	command    Command
	generation Generation
}

type Runner struct {
	ctx           context.Context
	cancel        context.CancelFunc
	post          func(func())
	sink          Sink
	options       Options
	jobs          chan queued
	stops         chan queued
	controls      chan selected
	done          chan struct{}
	mu            sync.Mutex
	cancelAttempt context.CancelFunc
	generation    atomic.Uint64
	attempt       atomic.Uint64
	refresh       atomic.Bool
	workers       sync.WaitGroup
}

func New(ctx context.Context, post func(func()), sink Sink, options Options) *Runner {
	ctx, cancel := context.WithCancel(ctx)
	if options.Start == nil {
		options.Start = func(ctx context.Context, connection Connection) (agent.Client, error) {
			if connection.Open == nil {
				return nil, errors.New("no backend configured")
			}
			return connection.Open(ctx)
		}
	}
	if options.After == nil {
		options.After = time.After
	}
	if options.Capacity <= 0 {
		options.Capacity = 16
	}
	r := &Runner{ctx: ctx, cancel: cancel, post: post, sink: sink, options: options, jobs: make(chan queued, options.Capacity), stops: make(chan queued, 1), controls: make(chan selected, 1), done: make(chan struct{})}
	go r.run()
	return r
}
func (r *Runner) Close() {
	r.cancel()
	r.mu.Lock()
	if r.cancelAttempt != nil {
		r.cancelAttempt()
	}
	r.mu.Unlock()
}
func (r *Runner) Done() <-chan struct{} { return r.done }

// Select invalidates publications immediately, even while the old client is
// starting or executing a request. Control has its own reserved channel.
func (r *Runner) Select(generation Generation, connection Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return
	}
	r.generation.Store(uint64(generation))
	if r.cancelAttempt != nil {
		r.cancelAttempt()
	}
	for {
		select {
		case <-r.jobs:
		default:
			goto drained
		}
	}
drained:
	r.refresh.Store(false)
	for {
		select {
		case <-r.stops:
		default:
			goto stopsDrained
		}
	}
stopsDrained:
	select {
	case <-r.controls:
	default:
	}
	connection.Pins = append([]string(nil), connection.Pins...)
	r.controls <- selected{connection, generation}
}

func (r *Runner) Submit(generation Generation, command Command) Admission {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return Closed
	}
	if uint64(generation) != r.generation.Load() || !valid(command) {
		return Invalid
	}
	q := queued{command, generation}
	if command.kind() == KindStop {
		select {
		case r.stops <- q:
			return Accepted
		default:
			return Busy
		}
	}
	if command.kind() == KindRefresh && !r.refresh.CompareAndSwap(false, true) {
		return Accepted
	}
	select {
	case r.jobs <- q:
		return Accepted
	default:
		if command.kind() == KindRefresh {
			r.refresh.Store(false)
		}
		return Busy
	}
}

func (r *Runner) publish(g Generation, attempt Attempt, fn func(Sink)) {
	r.post(func() {
		if r.ctx.Err() == nil && r.generation.Load() == uint64(g) && r.attempt.Load() == uint64(attempt) {
			fn(r.sink)
		}
	})
}
func (r *Runner) spawn(work func()) { r.workers.Add(1); go func() { defer r.workers.Done(); work() }() }

func (r *Runner) run() {
	defer func() { r.workers.Wait(); close(r.done) }()
	var current selected
	select {
	case <-r.ctx.Done():
		return
	case current = <-r.controls:
	}
	retries := 0

	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		if retries > 0 {
			delay := []time.Duration{time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}[min(retries-1, 3)]
			select {
			case <-r.ctx.Done():
				return
			case next := <-r.controls:
				current = next
				retries = 0

				continue
			case <-r.options.After(delay):
			}
		}
		r.mu.Lock()
		if r.generation.Load() != uint64(current.generation) {
			r.mu.Unlock()
			select {
			case current = <-r.controls:
				retries = 0

				continue
			case <-r.ctx.Done():
				return
			}
		}
		attemptCtx, cancel := context.WithCancel(r.ctx)
		r.cancelAttempt = cancel
		attempt := Attempt(r.attempt.Add(1))
		r.mu.Unlock()
		owner := current
		post := func(fn func(Sink)) {
			r.publish(owner.generation, attempt, func(s Sink) {
				if attemptCtx.Err() == nil {
					fn(s)
				}
			})
		}
		post(func(s Sink) { s.Status(Status{State: "connecting", Address: owner.connection.Address}) })
		client, err := r.options.Start(attemptCtx, owner.connection)
		if err != nil {
			cancel()
			r.publish(owner.generation, attempt, func(s Sink) {
				s.Status(Status{State: "down", Err: err, Address: owner.connection.Address})
				s.Notice("The agent would not come up: " + err.Error())
			})
		} else {
			post(func(s Sink) { s.Status(Status{State: "ready", Address: client.Endpoint(), Facts: client.Info()}) })
			r.bootstrap(attemptCtx, client, owner.connection, post)

			uncertain := r.serve(attemptCtx, client, owner, attempt, post)
			cancel()
			client.Stop()
			// The attempt's own end publishes through the runner's gates
			// only: the attempt context died a line above, and a guard
			// on it would press the drop's status out of the pipe with
			// the dead work.
			r.publish(owner.generation, attempt, func(s Sink) {
				s.Status(Status{State: "down", Address: client.Endpoint()})
				if uncertain {
					s.Notice("The connection ended while an action was running. Inspect the server state before retrying.")
				} else {
					s.Notice("The connection dropped. Trying again…")
				}
			})
		}
		if r.ctx.Err() != nil {
			return
		}
		select {
		case current = <-r.controls:
			retries = 0

		default:
			retries++
		}
	}
}

func (r *Runner) bootstrap(ctx context.Context, client agent.Client, connection Connection, post func(func(Sink))) {
	r.spawn(func() {
		rows, cursor, err := client.ListSessions(ctx, "", "", "")
		page := Page{Sessions: rows, Cursor: cursor, Err: err}
		post(func(s Sink) { s.Sessions(page) })
		if err != nil {
			return
		}
		extra, err := LoadPinnedSessions(ctx, connection.Pins, rows, cursor, func(ctx context.Context, cursor, search string) ([]agent.Session, string, error) {
			return client.ListSessions(ctx, cursor, search, "")
		})
		if err != nil {
			post(func(s Sink) { s.Notice("Pinned chats could not all load: " + err.Error()) })
			return
		}
		if len(extra) > 0 {
			post(func(s Sink) { s.Sessions(Page{Sessions: extra, Pinned: true}) })
		}
	})
	r.spawn(func() {
		models, err := client.ListModels(ctx)
		var agents []agent.Persona
		var agentsErr error
		if catalog, ok := client.(agent.Personas); ok {
			agents, agentsErr = catalog.ListAgents(ctx)
		}
		def, defaultErr := client.DefaultModel(ctx)
		post(func(s Sink) {
			s.Catalog(Catalog{Models: models, Agents: agents, Err: err, AgentsErr: agentsErr, Default: def, DefaultErr: defaultErr})
		})
	})
	if connection.Projects {
		r.spawn(func() {
			workspace, ok := client.(agent.Workspaces)
			if !ok {
				return
			}
			projects, err := workspace.ListProjects(ctx)
			if err == nil {
				post(func(s Sink) { s.Projects(projects) })
				return
			}
			// The failure keeps the folders already known, and names why.
			post(func(s Sink) { s.Notice("The workspace list did not load: " + err.Error()) })
		})
	}
}

func (r *Runner) serve(ctx context.Context, client agent.Client, owner selected, attempt Attempt, post func(func(Sink))) bool {
	ended := make(chan error, 1)
	r.spawn(func() {
		ended <- batchUpdates(ctx, client.Updates, r.post, func(batch []agent.Update) {
			if r.ctx.Err() == nil && r.generation.Load() == uint64(owner.generation) && r.attempt.Load() == uint64(attempt) {
				r.sink.Updates(batch)
			}
		}, func(message string) { post(func(s Sink) { s.Notice(message) }) })
	})
	var active <-chan struct{}
	activeMutation := false
	var session string
	var draft uint64
	for {
		select {
		case <-ctx.Done():
			return active != nil && activeMutation
		case <-ended:
			return active != nil && activeMutation
		case stop := <-r.stops:
			if stop.generation != owner.generation {
				continue
			}
			command := stop.command.(Stop)
			r.spawn(func() {
				if err := client.Interrupt(ctx, string(command.Session)); err != nil {
					post(func(s Sink) {
						s.Failed(Failure{Origin: Origin{Session: command.Session}, Text: "The turn refused to stop: " + err.Error()})
					})
				}
			})
		case <-active:
			active = nil
		case job := <-availableJobs(r.jobs, active):
			if job.command.kind() == KindRefresh {
				r.refresh.Store(false)
			}
			if job.generation != owner.generation {
				continue
			}
			finished := make(chan struct{})
			active = finished
			activeMutation = mutation(job.command)
			r.spawn(func() { defer close(finished); r.execute(ctx, client, job.command, &session, &draft, post) })
		}
	}
}
func availableJobs(jobs <-chan queued, active <-chan struct{}) <-chan queued {
	if active != nil {
		return nil
	}
	return jobs
}
func (r *Runner) execute(ctx context.Context, client agent.Client, command Command, session *string, draft *uint64, post func(func(Sink))) {
	origin := commandOrigin(command)
	report := func(action string, err error) {
		if err == nil {
			return
		}
		message := action + ": " + err.Error()
		if mutation(command) {
			message += ". The server outcome may be uncertain; inspect before retrying."
		}
		failure := Failure{Origin: origin, Text: message}
		post(func(s Sink) { s.Failed(failure) })
	}
	switch command.kind() {
	case KindPrompt:
		c := command.(Prompt)
		target := string(c.Session)
		if target == "" {
			if *session == "" || *draft != c.Draft {
				id, ref, err := client.CreateSession(ctx, c.Directory, c.Agent)
				if err != nil {
					report("The chat did not open", err)
					return
				}
				want := c.Model
				if want.ID != "" {
					if err := client.SetModel(ctx, id, want.ProviderID, want.ID, want.Variant); err != nil {
						report("The model was not set", err)
					} else {
						ref = want
					}
				}
				*session, *draft = id, c.Draft
				post(func(s Sink) {
					s.Created(Created{Session: agent.Session{ID: id, Directory: c.Directory, Model: ref}, Draft: c.Draft})
				})
			}
			target = *session
		}
		origin = Origin{Session: chat.SessionID(target)}
		report("The prompt did not complete", client.Prompt(ctx, target, c.Text))
	case KindOpen:
		c := command.(Open)
		messages, err := client.SessionMessages(ctx, string(c.Session))
		if err != nil {
			report("The conversation did not open", err)
		} else {
			post(func(s Sink) { s.Opened(Opened{Session: string(c.Session), Messages: messages, Revision: c.Revision}) })
		}
	case KindStop: // Stops use the independent control channel.
	case KindAnswer:
		c := command.(Answer)
		err := client.ReplyPermission(ctx, string(c.Session), string(c.Request), string(c.Decision))
		if err != nil {
			report("The approval did not go through", err)
		} else {
			post(func(s Sink) { s.Answered(string(c.Request)) })
		}
	case KindRename:
		c := command.(Rename)
		report("The rename did not complete", client.RenameSession(ctx, string(c.Session), c.Title))
	case KindDelete:
		c := command.(Delete)
		err := client.DeleteSession(ctx, string(c.Session))
		if err != nil {
			report("The chat did not delete", err)
		} else {
			post(func(s Sink) { s.Deleted(string(c.Session)) })
		}
	case KindSelectModel:
		c := command.(SelectModel)
		report("The model did not switch", client.SetModel(ctx, string(c.Session), c.Model.ProviderID, c.Model.ID, c.Model.Variant))
	case KindMore:
		c := command.(More)
		rows, cursor, err := client.ListSessions(ctx, c.Cursor, "", "")
		post(func(s Sink) { s.Sessions(Page{Sessions: rows, Cursor: cursor, More: true, Err: err}) })
	case KindRefresh:
		rows, cursor, err := client.ListSessions(ctx, "", "", "")
		post(func(s Sink) { s.Sessions(Page{Sessions: rows, Cursor: cursor, Err: err}) })
	case KindUsage:
		c := command.(Usage)
		message, err := client.SessionMessage(ctx, string(c.Session), string(c.Message))
		if err == nil {
			post(func(s Sink) { s.Usage(message) })
			return
		}
		// The line under the answer stays without its costing, and the
		// note says why.
		report("What this turn spent did not load", err)
	}
}
