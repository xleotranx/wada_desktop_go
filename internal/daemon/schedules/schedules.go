// Package schedules runs prompts on a schedule: heartbeats that nudge an
// existing conversation, and schedules that start a new one each time. They
// are kept in wadad.db and run by the daemon, in its local time.
//
// A run is never retried: a failed send's outcome is unknown, so it is
// recorded and the schedule waits for its next time. Runs missed while the
// daemon was down are not caught up.
package schedules

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

// Runner is what a run needs from the daemon.
type Runner interface {
	// Create starts a conversation; a request's Workspace is resolved by it.
	Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error)
	Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error)
	// Running reports that a conversation's turn is going.
	Running(session string) bool
}

// MinEvery is the shortest interval a schedule runs at.
const MinEvery = time.Minute

// ErrNotFound is a schedule that doesn't exist.
var ErrNotFound = errors.New("schedules: no such schedule")

// Service keeps and runs the daemon's schedules.
type Service struct {
	st   *store.Store
	run  Runner
	emit func(protocol.Event)
	now  func() time.Time

	mu      sync.Mutex
	list    map[string]*protocol.Schedule
	running map[string]bool // runs in flight, by schedule
	wake    chan struct{}
	wg      sync.WaitGroup
}

// Open loads the stored schedules. Each one's next run is counted from now.
func Open(ctx context.Context, st *store.Store, run Runner, emit func(protocol.Event)) (*Service, error) {
	return open(ctx, st, run, emit, time.Now)
}

func open(ctx context.Context, st *store.Store, run Runner, emit func(protocol.Event), now func() time.Time) (*Service, error) {
	stored, err := st.Schedules(ctx)
	if err != nil {
		return nil, err
	}
	s := &Service{st: st, run: run, emit: emit, now: now, list: map[string]*protocol.Schedule{},
		running: map[string]bool{}, wake: make(chan struct{}, 1)}
	for i := range stored {
		sc := stored[i]
		sc.NextRun = s.next(sc.ScheduleSpec, now())
		s.list[sc.ID] = &sc
	}
	return s, nil
}

// List is every schedule, by name.
func (s *Service) List() []protocol.Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.Schedule, 0, len(s.list))
	for _, sc := range s.list {
		out = append(out, *sc)
	}
	slices.SortFunc(out, func(a, b protocol.Schedule) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return out
}

// Validate checks a spec and returns it tidied.
func Validate(spec protocol.ScheduleSpec) (protocol.ScheduleSpec, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Cron = strings.TrimSpace(spec.Cron)
	spec.Every = strings.TrimSpace(spec.Every)
	if strings.TrimSpace(spec.Prompt) == "" {
		return spec, errors.New("a schedule needs a prompt")
	}
	switch {
	case spec.Cron != "" && spec.Every != "":
		return spec, errors.New("a schedule runs on cron or every, not both")
	case spec.Cron != "":
		if _, err := ParseCron(spec.Cron); err != nil {
			return spec, err
		}
	case spec.Every != "":
		d, err := time.ParseDuration(spec.Every)
		if err != nil {
			return spec, fmt.Errorf("every %q: %w", spec.Every, err)
		}
		if d < MinEvery {
			return spec, fmt.Errorf("every %s: at least %s", spec.Every, MinEvery)
		}
	default:
		return spec, errors.New("a schedule needs cron or every")
	}
	if spec.Session != "" && (spec.Directory != "" || spec.Workspace != "" || spec.Provider != "" || spec.Profile != "") {
		return spec, errors.New("a heartbeat goes to its conversation; it takes no folder, workspace, provider or profile")
	}
	switch spec.Mode {
	case "", "ask", "full":
	default:
		return spec, fmt.Errorf("mode %q: ask or full", spec.Mode)
	}
	if spec.Name == "" {
		spec.Name = firstLine(spec.Prompt, 48)
	}
	return spec, nil
}

// Create adds a schedule.
func (s *Service) Create(ctx context.Context, spec protocol.ScheduleSpec) (protocol.Schedule, error) {
	spec, err := Validate(spec)
	if err != nil {
		return protocol.Schedule{}, err
	}
	now := s.now()
	sc := protocol.Schedule{ScheduleSpec: spec, ID: "sch_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], Created: now.UnixMilli(),
		NextRun: s.next(spec, now)}
	if err := s.st.PutSchedule(ctx, sc); err != nil {
		return protocol.Schedule{}, err
	}
	s.mu.Lock()
	s.list[sc.ID] = &sc
	s.mu.Unlock()
	s.changed(sc)
	return sc, nil
}

// Update replaces a schedule's spec, keeping how its runs went.
func (s *Service) Update(ctx context.Context, id string, spec protocol.ScheduleSpec) (protocol.Schedule, error) {
	spec, err := Validate(spec)
	if err != nil {
		return protocol.Schedule{}, err
	}
	s.mu.Lock()
	cur, ok := s.list[id]
	if !ok {
		s.mu.Unlock()
		return protocol.Schedule{}, ErrNotFound
	}
	sc := *cur
	sc.ScheduleSpec = spec
	sc.NextRun = s.next(spec, s.now())
	s.mu.Unlock()
	if err := s.st.PutSchedule(ctx, sc); err != nil {
		return protocol.Schedule{}, err
	}
	s.mu.Lock()
	if _, ok := s.list[id]; ok {
		s.list[id] = &sc
	}
	s.mu.Unlock()
	s.changed(sc)
	return sc, nil
}

// Delete removes a schedule; a run in flight finishes.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	sc, ok := s.list[id]
	s.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if err := s.st.DeleteSchedule(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.list, id)
	s.mu.Unlock()
	gone := *sc
	gone.Removed = true
	s.changed(gone)
	return nil
}

// RunNow runs a schedule at once, paused or not, unless a run of it is in
// flight. Its next time stays as it was.
func (s *Service) RunNow(id string) error {
	s.mu.Lock()
	sc, ok := s.list[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if s.running[id] {
		s.mu.Unlock()
		return errors.New("schedules: it is running already")
	}
	s.running[id] = true
	spec := sc.ScheduleSpec
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runOnce(context.Background(), id, spec)
	}()
	return nil
}

// Run runs schedules as they come due until ctx ends, then waits for the
// runs in flight.
func (s *Service) Run(ctx context.Context) {
	defer s.wg.Wait()
	for {
		wait := s.due(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// due starts the runs that are due and returns how long until the next.
func (s *Service) due(ctx context.Context) time.Duration {
	now := s.now()
	wait := time.Minute
	s.mu.Lock()
	var start []protocol.Schedule
	for id, sc := range s.list {
		if sc.NextRun == 0 {
			continue
		}
		at := time.UnixMilli(sc.NextRun)
		if at.After(now) {
			wait = min(wait, at.Sub(now))
			continue
		}
		sc.NextRun = s.next(sc.ScheduleSpec, now)
		if s.running[id] {
			continue
		}
		s.running[id] = true
		start = append(start, *sc)
	}
	s.mu.Unlock()
	for _, sc := range start {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runOnce(ctx, sc.ID, sc.ScheduleSpec)
		}()
	}
	return max(wait, time.Second)
}

// runOnce sends the schedule's prompt and records how it went.
func (s *Service) runOnce(ctx context.Context, id string, spec protocol.ScheduleSpec) {
	defer func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()
	status, session, err := s.send(ctx, id, spec)
	s.mu.Lock()
	sc, ok := s.list[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	sc.LastRun = s.now().UnixMilli()
	sc.LastStatus = status
	sc.LastError = ""
	if err != nil {
		sc.LastError = err.Error()
	}
	if session != "" {
		sc.LastSession = session
	}
	cp := *sc
	s.mu.Unlock()
	_ = s.st.PutSchedule(context.WithoutCancel(ctx), cp)
	s.changed(cp)
}

func (s *Service) send(ctx context.Context, id string, spec protocol.ScheduleSpec) (protocol.ScheduleStatus, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	key := "sched_" + id + "_" + uuid.NewString()[:8]
	if spec.Session != "" {
		if s.run.Running(spec.Session) {
			return protocol.ScheduleSkipped, spec.Session, errors.New("its turn was still going")
		}
		_, err := s.run.Send(ctx, protocol.SendRequest{MessageID: key, Session: spec.Session, Text: spec.Prompt, Mode: spec.Mode})
		if err != nil {
			return protocol.ScheduleFailed, spec.Session, err
		}
		return protocol.ScheduleRan, spec.Session, nil
	}
	s.mu.Lock()
	last := ""
	if sc, ok := s.list[id]; ok {
		last = sc.LastSession
	}
	s.mu.Unlock()
	if last != "" && s.run.Running(last) {
		return protocol.ScheduleSkipped, "", errors.New("its last run was still going")
	}
	sess, err := s.run.Create(ctx, protocol.CreateSessionRequest{IdempotencyKey: key, Title: spec.Name + " · " + s.now().Format("Jan 2 15:04"),
		Directory: spec.Directory, Workspace: spec.Workspace, Provider: spec.Provider, Profile: spec.Profile, Mode: spec.Mode})
	if err != nil {
		return protocol.ScheduleFailed, "", err
	}
	if _, err := s.run.Send(ctx, protocol.SendRequest{MessageID: key, Session: sess.ID, Text: spec.Prompt, Mode: spec.Mode}); err != nil {
		return protocol.ScheduleFailed, sess.ID, err
	}
	return protocol.ScheduleRan, sess.ID, nil
}

// next is when spec runs after t, in Unix ms; 0 while paused.
func (s *Service) next(spec protocol.ScheduleSpec, t time.Time) int64 {
	if spec.Paused {
		return 0
	}
	if spec.Cron != "" {
		c, err := ParseCron(spec.Cron)
		if err != nil {
			return 0
		}
		if n := c.Next(t); !n.IsZero() {
			return n.UnixMilli()
		}
		return 0
	}
	d, err := time.ParseDuration(spec.Every)
	if err != nil || d < MinEvery {
		return 0
	}
	return t.Add(d).UnixMilli()
}

// changed tells clients and the run loop about a schedule.
func (s *Service) changed(sc protocol.Schedule) {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if s.emit != nil {
		s.emit(protocol.Event{Kind: protocol.EventScheduleUpdated, Schedule: &sc})
	}
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
