package schedules

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

func TestCronNext(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ expr, from, want string }{
		{"*/15 * * * *", "2026-11-28 10:07", "2026-11-28 10:15"},
		{"0 9 * * 1-5", "2026-11-28 10:00", "2026-11-30 09:00"}, // Saturday → Monday
		{"30 2 1 * *", "2026-11-28 10:00", "2026-12-01 02:30"},
		{"0 0 * * 7", "2026-11-28 10:00", "2026-11-29 00:00"}, // 7 is Sunday
		{"@daily", "2026-12-31 23:59", "2027-01-01 00:00"},
		{"0 12 13 * 5", "2026-11-28 10:00", "2026-12-04 12:00"}, // the 13th or a Friday
		{"5,10 8-9 * * *", "2026-11-28 08:10", "2026-11-28 09:05"},
	} {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := cr.Next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s after %s = %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04 Mon"), c.want)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "*/0 * * * *", "5-1 * * * *", "x * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		spec protocol.ScheduleSpec
		ok   bool
	}{
		{protocol.ScheduleSpec{Prompt: "check CI", Every: "30m"}, true},
		{protocol.ScheduleSpec{Prompt: "check CI", Cron: "@hourly", Session: "s1"}, true},
		{protocol.ScheduleSpec{Prompt: "", Every: "30m"}, false},
		{protocol.ScheduleSpec{Prompt: "x", Every: "10s"}, false},
		{protocol.ScheduleSpec{Prompt: "x", Every: "1h", Cron: "@daily"}, false},
		{protocol.ScheduleSpec{Prompt: "x"}, false},
		{protocol.ScheduleSpec{Prompt: "x", Every: "1h", Session: "s", Directory: "/d"}, false},
		{protocol.ScheduleSpec{Prompt: "x", Every: "1h", Mode: "yolo"}, false},
	} {
		got, err := Validate(c.spec)
		if (err == nil) != c.ok {
			t.Errorf("%+v: err %v", c.spec, err)
		}
		if err == nil && got.Name == "" {
			t.Errorf("%+v: no name", c.spec)
		}
	}
}

type fakeRunner struct {
	mu      sync.Mutex
	created []protocol.CreateSessionRequest
	sent    []protocol.SendRequest
	running map[string]bool
	sendErr error
	n       int
}

func (f *fakeRunner) Create(_ context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, req)
	f.n++
	return protocol.Session{ID: "new" + string(rune('0'+f.n))}, nil
}

func (f *fakeRunner) Send(_ context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	return protocol.SendAccepted, f.sendErr
}

func (f *fakeRunner) Running(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id]
}

func TestServiceRunsAndRecords(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := time.Date(2026, 11, 28, 10, 0, 0, 0, time.Local)
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	run := &fakeRunner{running: map[string]bool{}}
	var events []protocol.Schedule
	var evMu sync.Mutex
	s, err := open(t.Context(), st, run, func(ev protocol.Event) {
		evMu.Lock()
		events = append(events, *ev.Schedule)
		evMu.Unlock()
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	beat, err := s.Create(t.Context(), protocol.ScheduleSpec{Prompt: "keep going", Every: "10m", Session: "chat1"})
	if err != nil {
		t.Fatal(err)
	}
	daily, err := s.Create(t.Context(), protocol.ScheduleSpec{Name: "Triage", Prompt: "triage issues", Cron: "0 9 * * *", Workspace: "wks1", Profile: "triager"})
	if err != nil {
		t.Fatal(err)
	}
	if beat.NextRun != clock.Add(10*time.Minute).UnixMilli() || beat.Name != "keep going" {
		t.Fatalf("heartbeat = %+v", beat)
	}

	// Due: the heartbeat runs; the daily one waits for 9:00 tomorrow.
	advance(10 * time.Minute)
	s.due(t.Context())
	s.wg.Wait()
	if len(run.sent) != 1 || run.sent[0].Session != "chat1" || run.sent[0].Text != "keep going" || len(run.created) != 0 {
		t.Fatalf("sent %+v created %+v", run.sent, run.created)
	}
	// Busy: skipped, and not queued.
	run.running["chat1"] = true
	advance(10 * time.Minute)
	s.due(t.Context())
	s.wg.Wait()
	if got := s.get(beat.ID); got.LastStatus != protocol.ScheduleSkipped || len(run.sent) != 1 {
		t.Fatalf("busy heartbeat = %+v, sent %d", got, len(run.sent))
	}

	// Run now starts a new conversation as the profile, in the workspace.
	if err := s.RunNow(daily.ID); err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	if len(run.created) != 1 || run.created[0].Workspace != "wks1" || run.created[0].Profile != "triager" {
		t.Fatalf("created %+v", run.created)
	}
	got := s.get(daily.ID)
	if got.LastStatus != protocol.ScheduleRan || got.LastSession != "new1" || got.NextRun != daily.NextRun {
		t.Fatalf("daily after run now = %+v", got)
	}
	// A failed send is recorded, not retried.
	run.sendErr = errors.New("boom")
	run.running["chat1"] = false
	advance(10 * time.Minute)
	s.due(t.Context())
	s.wg.Wait()
	if got := s.get(beat.ID); got.LastStatus != protocol.ScheduleFailed || got.LastError != "boom" || len(run.sent) != 3 {
		t.Fatalf("failed heartbeat = %+v, sent %d", got, len(run.sent))
	}

	// Paused: no next run. Reopened: still there, next counted from now.
	spec := beat.ScheduleSpec
	spec.Paused = true
	if p, err := s.Update(t.Context(), beat.ID, spec); err != nil || p.NextRun != 0 {
		t.Fatalf("paused = %+v, %v", p, err)
	}
	again, err := open(t.Context(), st, run, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if list := again.List(); len(list) != 2 || list[0].Name != "Triage" || list[1].LastStatus != protocol.ScheduleFailed {
		t.Fatalf("reopened = %+v", list)
	}
	if err := s.Delete(t.Context(), daily.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), daily.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	evMu.Lock()
	last := events[len(events)-1]
	evMu.Unlock()
	if !last.Removed || last.ID != daily.ID {
		t.Fatalf("last event = %+v", last)
	}
}

func (s *Service) get(id string) protocol.Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.list[id]
}
