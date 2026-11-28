package schedules

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"
)

// fakeSchedules is an agent with a schedules store.
type fakeSchedules struct {
	*plugintest.Agent
	mu      sync.Mutex
	list    []agent.Schedule
	created []agent.ScheduleSpec
	ran     []string
	n       int
}

func (f *fakeSchedules) NewPlugin() kernel.Plugin { return &fakeSchedulesPlugin{f} }

type fakeSchedulesPlugin struct{ f *fakeSchedules }

func (p *fakeSchedulesPlugin) Manifest() kernel.Manifest {
	m := p.f.Manifest()
	m.Provides = append(m.Provides, agent.SchedulesKey)
	return m
}

func (p *fakeSchedulesPlugin) Setup(c *kernel.Context) error {
	if err := p.f.Setup(c); err != nil {
		return err
	}
	return kernel.Provide[agent.Schedules](c, agent.SchedulesKey, p.f)
}

func (f *fakeSchedules) Schedules(context.Context) ([]agent.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Schedule(nil), f.list...), nil
}

func (f *fakeSchedules) CreateSchedule(_ context.Context, spec agent.ScheduleSpec) (agent.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.created = append(f.created, spec)
	sc := agent.Schedule{ScheduleSpec: spec, ID: "sch" + string(rune('0'+f.n)), NextRun: time.Now().Add(time.Hour).UnixMilli()}
	if sc.Name == "" {
		sc.Name = "unnamed"
	}
	f.list = append(f.list, sc)
	return sc, nil
}

func (f *fakeSchedules) UpdateSchedule(_ context.Context, id string, spec agent.ScheduleSpec) (agent.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].ScheduleSpec = spec
			if spec.Paused {
				f.list[i].NextRun = 0
			}
			return f.list[i], nil
		}
	}
	return agent.Schedule{}, context.Canceled
}

func (f *fakeSchedules) DeleteSchedule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list = append(f.list[:i], f.list[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeSchedules) RunSchedule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, id)
	return nil
}

func waitCond(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func TestHeartbeatsAndSchedules(t *testing.T) {
	fa := &fakeSchedules{Agent: plugintest.NewAgent(), list: []agent.Schedule{
		{ID: "old", ScheduleSpec: agent.ScheduleSpec{Name: "Nightly triage", Prompt: "triage", Cron: "0 2 * * *"},
			LastStatus: agent.ScheduleFailed, LastError: "no model", LastRun: time.Now().UnixMilli()},
	}}
	fa.State = agent.Status{State: agent.Ready, Attempt: 1}
	chatFake := &plugintest.Chat{}
	host := &kittest.Host{Data: t.TempDir()}
	var p *Plugin
	h := kerneltest.New(t, host.New, fa.NewPlugin, chatFake.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	waitCond(t, h, "schedules load", func() bool { return len(p.list) == 1 })

	// A heartbeat for the open conversation.
	kernel.Emit(h, chat.Changed, chat.Conversation{Session: "chat1", Directory: "/repo"})
	h.Flush()
	tt := ui.NewTester(p.heartbeatView, 320, 300)
	tt.Frame()
	if !tt.HasText("Add heartbeat…") {
		t.Fatalf("heartbeat section = %q", tt.Texts())
	}
	p.openNew(true)
	p.form.when = "15m"
	p.submit()
	waitCond(t, h, "the heartbeat is added", func() bool { return !p.form.on && len(p.heartbeats()) == 1 })
	if got := fa.created[0]; got.Session != "chat1" || got.Every != "15m" || got.Directory != "" || got.Prompt == "" {
		t.Fatalf("created %+v", got)
	}
	tt.Frame()
	if !tt.HasText("Every 15m · next") {
		t.Fatalf("heartbeat section = %q", tt.Texts())
	}

	// A schedule that starts conversations in the folder.
	p.openNew(false)
	p.form.prompt, p.form.mode, p.form.profile = "summarize yesterday's commits", "full", "reviewer"
	p.submit()
	waitCond(t, h, "the schedule is added", func() bool { return len(p.list) == 3 })
	if got := fa.created[1]; got.Session != "" || got.Directory != "/repo" || got.Cron != "0 9 * * 1-5" || got.Mode != "full" || got.Profile != "reviewer" {
		t.Fatalf("created %+v", got)
	}

	pane := ui.NewTester(p.paneView, 700, 600)
	pane.Frame()
	for _, want := range []string{"Nightly triage", "Failed", "no model", "Cron 0 9 * * 1-5"} {
		found := false
		for _, txt := range pane.Texts() {
			if strings.Contains(txt, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("pane missing %q in %q", want, pane.Texts())
		}
	}

	// Pause, run and delete.
	beat := p.heartbeats()[0]
	p.setPaused(beat, true)
	waitCond(t, h, "the heartbeat pauses", func() bool { return p.heartbeats()[0].Paused })
	p.runNow(p.list[0])
	waitCond(t, h, "the run is asked for", func() bool { fa.mu.Lock(); defer fa.mu.Unlock(); return len(fa.ran) == 1 })
	p.remove(p.heartbeats()[0])
	waitCond(t, h, "the heartbeat goes", func() bool { return len(p.heartbeats()) == 0 })

	// An event from the backend replaces the copy.
	upd := p.list[0]
	upd.LastStatus = agent.ScheduleRan
	kernel.Emit(h, agent.ScheduleUpdated, upd)
	h.Flush()
	if p.list[0].LastStatus != agent.ScheduleRan {
		t.Fatalf("after the event: %+v", p.list[0])
	}
}
