// Package schedules shows the backend's schedules: a conversation's
// heartbeat in the work panel, and every schedule in a Schedules tab, with
// one dialog to add or change either.
package schedules

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
)

// PaneKind is the Schedules tab's kind in tabs.Panes.
const PaneKind = "schedules.list"

// Plugin keeps the backend's schedules on the UI thread.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller
	host  shell.Host

	conv    chat.Conversation
	attempt uint64
	sourced bool // a provider of agent.SchedulesKey was there at the last read
	list    []agent.Schedule
	load    kernel.Task
	// One task per schedule acted on, so one action never cancels another's.
	acts map[string]*kernel.Task

	form form
}

// form is the add-or-change dialog.
type form struct {
	on, busy  bool
	id        string // the schedule changed; empty adds one
	heartbeat bool   // to session, else a new conversation each run
	session   agent.SessionID
	directory string
	name      string
	prompt    string
	cron      bool // Cron, else Every
	when      string
	mode      string
	profile   string
	paused    bool
	problem   string
	save      kernel.Task
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "schedules", Title: "Schedules",
		Description: "Heartbeats that nudge a conversation, and schedules that start one, run by the backend.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.acts = map[string]*kernel.Task{}
	p.conv = p.chat.Current()
	kernel.On(c, chat.Changed, func(conv chat.Conversation) { p.conv = conv })
	kernel.On(c, agent.ScheduleUpdated, p.updated)
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != p.attempt {
			p.attempt = st.Attempt
			p.reload()
		}
	})
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if _, ok := kernel.Use(c, agent.SchedulesKey); ok != p.sourced {
			p.reload()
		}
	})
	if st := p.agent.Status(); st.State == agent.Ready {
		p.attempt = st.Attempt
		p.reload()
	}

	kernel.Contribute(c, chat.Work, "schedules.heartbeat", 18, chat.WorkSection{Title: "Heartbeat",
		Shown: func() bool { return p.conv.Session != "" && p.sourced }, Render: p.heartbeatView})
	kernel.Contribute(c, tabs.Panes, PaneKind, 30, tabs.PaneKind{
		Title: func(string) string { return "Schedules" }, Icon: lucide.CalendarClock,
		Render: func(c *ui.Context, _ tabs.Tab) { p.paneView(c) }})
	kernel.Contribute(c, kernel.Commands, "schedules.show", 0, kernel.Command{
		Title: "Schedules", Menu: "View", Run: p.openTab,
		Enabled: func() bool {
			_, tabsOK := kernel.Use(c, tabs.ServiceKey)
			return tabsOK && p.sourced
		}})
	kernel.Contribute(c, kernel.Commands, "schedules.new", 0, kernel.Command{
		Title: "New Schedule…", Menu: "File", Run: func() { p.openNew(false) },
		Enabled: func() bool { return p.sourced && p.agent.Status().State == agent.Ready }})
	kernel.Contribute(c, kernel.Commands, "schedules.heartbeat", 0, kernel.Command{
		Title: "Add Heartbeat…", Menu: "File", Run: func() { p.openNew(true) },
		Enabled: func() bool { return p.sourced && p.conv.Session != "" && p.agent.Status().State == agent.Ready }})
	return kernel.Contribute(c, shell.Overlays, "schedules.dialog", 0, shell.View{Render: p.dialog})
}

func (p *Plugin) openTab() {
	if t, ok := kernel.Use(p.ctx, tabs.ServiceKey); ok {
		t.Open(PaneKind, "", t.Space())
	}
}

func (p *Plugin) reload() {
	svc, ok := kernel.Use(p.ctx, agent.SchedulesKey)
	p.sourced = ok
	if !ok {
		p.load.Cancel()
		p.list = nil
		return
	}
	kernel.Go(p.ctx, &p.load, svc.Schedules, func(list []agent.Schedule, err error) {
		if err == nil {
			p.list = list
			p.sort()
			p.ctx.Invalidate()
		}
	})
}

// updated takes in a schedule the backend added, changed, ran or removed.
func (p *Plugin) updated(sc agent.Schedule) {
	i := slices.IndexFunc(p.list, func(s agent.Schedule) bool { return s.ID == sc.ID })
	switch {
	case sc.Removed && i >= 0:
		p.list = slices.Delete(p.list, i, i+1)
	case sc.Removed:
		return
	case i >= 0:
		p.list[i] = sc
	default:
		p.list = append(p.list, sc)
		p.sort()
	}
	p.ctx.Invalidate()
}

func (p *Plugin) sort() {
	slices.SortFunc(p.list, func(a, b agent.Schedule) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
}

// heartbeats are the open conversation's.
func (p *Plugin) heartbeats() []agent.Schedule {
	var out []agent.Schedule
	for _, sc := range p.list {
		if sc.Session != "" && sc.Session == p.conv.Session {
			out = append(out, sc)
		}
	}
	return out
}

// openNew opens the dialog to add a heartbeat to the open conversation, or a
// schedule that starts one in its folder.
func (p *Plugin) openNew(heartbeat bool) {
	p.form = form{on: true, heartbeat: heartbeat && p.conv.Session != "", session: p.conv.Session,
		directory: p.conv.Directory, mode: "ask", when: "30m"}
	if p.form.heartbeat {
		p.form.prompt = "Check how this is going and continue where it stopped."
	} else {
		p.form.cron, p.form.when = true, "0 9 * * 1-5"
	}
}

// openEdit opens the dialog on a schedule.
func (p *Plugin) openEdit(sc agent.Schedule) {
	p.form = form{on: true, id: sc.ID, heartbeat: sc.Heartbeat(), session: sc.Session, directory: sc.Directory,
		name: sc.Name, prompt: sc.Prompt, cron: sc.Cron != "", when: cmp.Or(sc.Cron, sc.Every),
		mode: cmp.Or(sc.Mode, "ask"), profile: sc.Profile, paused: sc.Paused}
}

// spec is the form as a schedule spec. A changed schedule keeps the parts
// the form doesn't show.
func (f *form) spec(base agent.ScheduleSpec) agent.ScheduleSpec {
	s := base
	s.Name, s.Prompt, s.Paused = strings.TrimSpace(f.name), f.prompt, f.paused
	s.Cron, s.Every = "", ""
	if f.cron {
		s.Cron = strings.TrimSpace(f.when)
	} else {
		s.Every = strings.TrimSpace(f.when)
	}
	if f.heartbeat {
		s.Session = f.session
		return s
	}
	s.Directory, s.Mode, s.Profile = f.directory, f.mode, strings.TrimSpace(f.profile)
	return s
}

// submit adds or changes the schedule. A failure is shown and not retried:
// the change may have been made.
func (p *Plugin) submit() {
	svc, ok := kernel.Use(p.ctx, agent.SchedulesKey)
	if !ok || p.form.busy {
		return
	}
	f := &p.form
	var base agent.ScheduleSpec
	if i := slices.IndexFunc(p.list, func(s agent.Schedule) bool { return s.ID == f.id }); i >= 0 {
		base = p.list[i].ScheduleSpec
	}
	spec, id := f.spec(base), f.id
	f.busy, f.problem = true, ""
	kernel.Go(p.ctx, &f.save, func(ctx context.Context) (agent.Schedule, error) {
		if id == "" {
			return svc.CreateSchedule(ctx, spec)
		}
		return svc.UpdateSchedule(ctx, id, spec)
	}, func(sc agent.Schedule, err error) {
		f.busy = false
		if err != nil {
			f.problem = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.updated(sc)
		f.on = false
	})
}

// act runs one action on a schedule and reports a failure.
func (p *Plugin) act(id, what string, fn func(context.Context, agent.Schedules) (agent.Schedule, error)) {
	svc, ok := kernel.Use(p.ctx, agent.SchedulesKey)
	if !ok {
		return
	}
	task := p.acts[id]
	if task == nil {
		task = new(kernel.Task)
		p.acts[id] = task
	}
	kernel.Go(p.ctx, task, func(ctx context.Context) (agent.Schedule, error) { return fn(ctx, svc) },
		func(sc agent.Schedule, err error) {
			delete(p.acts, id)
			if err != nil {
				p.host.Toast(ui.Toast{Title: "Could not " + what, Description: err.Error(), Type: components.ToastError})
				return
			}
			if sc.ID != "" {
				p.updated(sc)
			}
		})
}

func (p *Plugin) setPaused(sc agent.Schedule, paused bool) {
	spec := sc.ScheduleSpec
	spec.Paused = paused
	what := "resume the schedule"
	if paused {
		what = "pause the schedule"
	}
	p.act(sc.ID, what, func(ctx context.Context, svc agent.Schedules) (agent.Schedule, error) {
		return svc.UpdateSchedule(ctx, sc.ID, spec)
	})
}

func (p *Plugin) runNow(sc agent.Schedule) {
	p.act(sc.ID, "run the schedule", func(ctx context.Context, svc agent.Schedules) (agent.Schedule, error) {
		return agent.Schedule{}, svc.RunSchedule(ctx, sc.ID)
	})
}

func (p *Plugin) remove(sc agent.Schedule) {
	p.act(sc.ID, "delete the schedule", func(ctx context.Context, svc agent.Schedules) (agent.Schedule, error) {
		gone := sc
		gone.Removed = true
		return gone, svc.DeleteSchedule(ctx, sc.ID)
	})
}
