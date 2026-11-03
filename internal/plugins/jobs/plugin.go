// Package jobs runs the app's jobs: it reads a folder's documents, then has
// a child agent work on the goal from the composer and keeps what came out.
// The job list lives in jobs.db, so it survives a restart.
package jobs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/documents"
	"wada-desktop-go/internal/contract/jobs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin owns the job list, the engine that runs one job at a time, and the
// Jobs section of the work panel.
type Plugin struct {
	ctx       *kernel.Context
	host      shell.Host
	chat      chat.Controller
	agent     agent.Service
	children  agent.Children
	docs      documents.Reader
	workspace workspace.Service
	store     *store
	work      kernel.Task
	send      kernel.Task
	review    kernel.Task
	settle    kernel.Task
	jobs      []jobs.Job
	selected  string // the job the Jobs page shows
	draft     string // the goal being typed on the Jobs page
	folder    string // the folder the next job from the page works in
	next      atomic.Uint64

	// editing is the job whose plan the user changes, and the fields hold
	// the working copy while it is.
	editing         string
	planSteps       []string
	planDeliverable string
	planCriteria    []string
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "jobs", Title: "Jobs",
		Description: "Reads a folder, works on a goal in the background and keeps what came out.",
		Provides:    []kernel.Named{jobs.ServiceKey},
		Requires:    []kernel.Named{shell.HostKey, chat.ControllerKey, agent.ServiceKey, agent.ChildrenKey, documents.ReaderKey, workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.children = kernel.Require(c, agent.ChildrenKey)
	p.docs = kernel.Require(c, documents.ReaderKey)
	p.workspace = kernel.Require(c, workspace.ServiceKey)
	store := newStore(filepath.Join(p.host.DataDir(), "jobs.db"))
	p.store = store
	c.Spawn(func(ctx context.Context) {
		store.run(ctx, func(loaded []jobs.Job, err error) {
			c.Post(func() { p.restore(loaded, err) })
		}, func(err error) { log.Printf("jobs: %v", err) })
	})
	if err := kernel.Provide[jobs.Service](c, jobs.ServiceKey, p); err != nil {
		return err
	}
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, shell.PageChanged, func(id string) {
		if id == jobs.PageID {
			p.seedFolder()
		}
	})
	kernel.On(c, shell.NoticeClicked, p.noticeClicked)
	if err := kernel.Contribute(c, kernel.Commands, "jobs.start", 55, kernel.Command{
		Title: "Start Job from Draft", Menu: "Jobs", Run: p.startFromDraft,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, kernel.Commands, "jobs.cancel", 56, kernel.Command{
		Title: "Stop Job", Menu: "Jobs", Enabled: func() bool { return p.active() != nil }, Run: p.cancel,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, chat.Work, "jobs.list", 20, chat.WorkSection{
		Title: "Jobs", Shown: p.shown, Render: p.view,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, chat.Home, "jobs.home", 10, chat.HomeSection{
		Shown: p.homeShown, Render: p.home,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, shell.Pages, jobs.PageID, 50, shell.Page{
		Title: func() string { return "Jobs" }, Render: p.page,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, shell.TitleBar, "jobs.title", 1, shell.View{Render: p.titleButton}); err != nil {
		return err
	}
	return kernel.Contribute(c, kernel.Commands, "jobs.show", 54, kernel.Command{
		Title: "Show Jobs", Menu: "Jobs", Keys: []string{"cmd+j"},
		Run: func() { p.host.ShowPage(jobs.PageID) },
	})
}

// Jobs lists jobs newest first.
func (p *Plugin) Jobs() []jobs.Job {
	out := slices.Clone(p.jobs)
	slices.Reverse(out)
	return out
}

// Start begins a job in the open conversation's folder.
func (p *Plugin) Start(goal, folder string) (jobs.Job, error) {
	return p.begin(goal, folder, p.chat.Current().Session)
}

// startFromDraft begins a job whose goal is what the user typed.
func (p *Plugin) startFromDraft() {
	goal, folder := strings.TrimSpace(p.chat.DraftText()), p.chat.Current().Directory
	if _, err := p.Start(goal, folder); err != nil {
		p.host.Toast(ui.Toast{Title: err.Error()})
		return
	}
	p.folder = folder
	p.chat.SetDraft("")
	p.host.Toast(ui.Toast{Title: "Reading the folder…"})
}

// Run approves the waiting plan and starts the work.
func (p *Plugin) Run(id string) error {
	job, ok := p.find(id)
	if !ok || job.State != jobs.PlanReview {
		return errors.New("no plan is waiting")
	}
	p.execute(job)
	return nil
}

// Remove forgets a settled job and deletes its source pack. What the job wrote
// in the folder stays: only derived data is removed.
func (p *Plugin) Remove(id string) error {
	job, ok := p.find(id)
	if !ok {
		return errors.New("no such job")
	}
	if job.State.Active() {
		return errors.New("stop the job before removing it")
	}
	p.jobs = slices.DeleteFunc(p.jobs, func(j jobs.Job) bool { return j.ID == id })
	if p.selected == id {
		p.selected = ""
	}
	if p.editing == id {
		p.editing = ""
	}
	p.store.remove(id)
	p.ctx.Invalidate()
	dir := filepath.Join(job.Folder, ".wada", "jobs", job.ID)
	kernel.Go(p.ctx, &p.settle, func(context.Context) (struct{}, error) {
		return struct{}{}, os.RemoveAll(dir)
	}, func(_ struct{}, err error) {
		if err != nil {
			log.Printf("jobs: removing %s: %v", dir, err)
		}
	})
	return nil
}

// Cancel stops a job: it is marked first, then the participant is aborted on
// a worker, because Abort blocks. A job waiting for its plan has no turn to
// abort.
func (p *Plugin) Cancel(id string) error {
	job, ok := p.find(id)
	if !ok || !job.State.Active() {
		return errors.New("no active job")
	}
	working := job.State.Working()
	session := job.Session
	if job.State == jobs.Reviewing && job.Reviewer != "" {
		session = job.Reviewer
	}
	p.setState(job, jobs.Cancelled, "")
	if !working || session == "" {
		return nil
	}
	kernel.Go(p.ctx, &p.settle, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Abort(ctx, session)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.setState(job, jobs.Failed, err.Error())
		}
	})
	return nil
}

// cancel stops whatever is running.
func (p *Plugin) cancel() {
	if job := p.active(); job != nil {
		if err := p.Cancel(job.ID); err != nil {
			p.host.Toast(ui.Toast{Title: err.Error()})
		}
	}
}

// begin validates and queues a job. It runs on the UI thread.
func (p *Plugin) begin(goal, folder string, parent agent.SessionID) (jobs.Job, error) {
	goal = strings.TrimSpace(goal)
	switch {
	case goal == "":
		return jobs.Job{}, errors.New("type the job's goal in the composer first")
	case folder == "":
		return jobs.Job{}, errors.New("open a folder first")
	case p.active() != nil:
		return jobs.Job{}, errors.New("a job is already active")
	}
	now := time.Now().UnixMilli()
	job := jobs.Job{ID: p.newID(), Goal: goal, Folder: folder, Parent: parent, State: jobs.Reading, Created: now, Updated: now}
	p.upsert(job)
	p.emit(job)
	p.launch(job, parent)
	return job, nil
}

// restore loads what jobs.db held, after whatever was started while it did.
// A job that was working when the app quit cannot continue, so it is reported
// as stopped rather than left working.
func (p *Plugin) restore(loaded []jobs.Job, err error) {
	if err != nil {
		log.Printf("jobs: %v", err)
		return
	}
	known := make(map[string]bool, len(loaded))
	merged := make([]jobs.Job, 0, len(loaded)+len(p.jobs))
	for _, job := range loaded {
		known[job.ID] = true
		if job.State.Working() {
			job.State = jobs.Failed
			job.Problem = "The app quit while this was running"
			p.store.put(job)
		}
		merged = append(merged, job)
	}
	for _, job := range p.jobs {
		if !known[job.ID] {
			merged = append(merged, job)
		}
	}
	p.jobs = merged
	slices.SortFunc(p.jobs, func(a, b jobs.Job) int { return cmp.Compare(a.Created, b.Created) })
}

// events follows the participant's and the reviewer's turns, on the UI
// thread. Which turn ended decides what happens: a plan, a review, the
// deliverable, or a failure.
func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		if !terminal(ev.Kind) {
			continue
		}
		job, ok := p.bySession(ev.Session)
		if !ok {
			continue
		}
		if ev.Kind != agent.TurnSucceeded {
			p.finish(job, ev)
			continue
		}
		switch {
		case ev.Session == job.Reviewer && job.State == jobs.Reviewing:
			p.reviewed(job, ev)
		case ev.Session == job.Session && job.State == jobs.Planning:
			p.planned(job, ev)
		case ev.Session == job.Session && job.State == jobs.Running:
			p.workDone(job)
		default:
			p.finish(job, ev)
		}
	}
}

// terminal reports that kind ends a turn.
func terminal(kind agent.EventKind) bool {
	return kind == agent.TurnSucceeded || kind == agent.TurnFailed || kind == agent.TurnInterrupted
}

// setState records a job's new state.
func (p *Plugin) setState(job jobs.Job, state jobs.State, problem string) {
	job.State = state
	job.Problem = problem
	p.upsert(job)
	p.emit(job)
	p.notify(job)
}

// notify says on the desktop that a job needs the user or settled, unless
// the user is looking at it already.
func (p *Plugin) notify(job jobs.Job) {
	notice, ok := noticeFor(job)
	if !ok {
		return
	}
	if p.host.Foreground() && p.host.Page() == jobs.PageID {
		return
	}
	p.host.Notify(notice)
}

// noticeClicked opens the job a notice was about.
func (p *Plugin) noticeClicked(id string) {
	jobID, ok := strings.CutPrefix(id, noticePrefix)
	if !ok {
		return
	}
	if _, found := p.find(jobID); !found {
		return
	}
	p.selected = jobID
	p.host.ShowPage(jobs.PageID)
}

// upsert saves a job and keeps the list in creation order.
func (p *Plugin) upsert(job jobs.Job) {
	job.Updated = time.Now().UnixMilli()
	for i := range p.jobs {
		if p.jobs[i].ID == job.ID {
			p.jobs[i] = job
			p.store.put(job)
			return
		}
	}
	p.jobs = append(p.jobs, job)
	p.store.put(job)
}

// emit tells the window a job changed and asks for the frame that shows it.
func (p *Plugin) emit(job jobs.Job) {
	kernel.Emit(p.ctx, jobs.Changed, job)
	p.ctx.Invalidate()
}

// find returns the job with id.
func (p *Plugin) find(id string) (jobs.Job, bool) {
	for _, job := range p.jobs {
		if job.ID == id {
			return job, true
		}
	}
	return jobs.Job{}, false
}

// bySession returns the job whose participant is session.
func (p *Plugin) bySession(session agent.SessionID) (jobs.Job, bool) {
	if session == "" {
		return jobs.Job{}, false
	}
	for _, job := range p.jobs {
		if !job.State.Active() {
			continue
		}
		if job.Session == session || (job.Reviewer != "" && job.Reviewer == session) {
			return job, true
		}
	}
	return jobs.Job{}, false
}

// active returns the job that is working or waiting for its plan, if any.
func (p *Plugin) active() *jobs.Job {
	for i := range p.jobs {
		if p.jobs[i].State.Active() {
			return &p.jobs[i]
		}
	}
	return nil
}

// noticePrefix marks a notice ID as naming a job.
const noticePrefix = "job:"

// noticeFor describes what happened to a job, for the desktop.
func noticeFor(job jobs.Job) (shell.Notice, bool) {
	notice := shell.Notice{ID: noticePrefix + job.ID, Subtitle: job.Goal}
	if job.State == jobs.PlanReview {
		notice.Title = "Plan ready to run"
		if job.Plan != nil {
			notice.Body = fmt.Sprintf("%d steps · %s", len(job.Plan.Steps), deliverableName(job))
		}
		return notice, true
	}
	if job.State == jobs.Done {
		notice.Title = "Job finished"
		if job.Result != "" {
			notice.Body = oneLine(job.Result)
		} else if len(job.Files) > 0 {
			notice.Body = "Wrote " + filepath.Base(job.Files[0].Path)
		}
		return notice, true
	}
	if job.State == jobs.Failed {
		notice.Title = "Job failed"
		notice.Body = oneLine(job.Problem)
		return notice, true
	}
	return shell.Notice{}, false
}

// newID makes a job identity that sorts by creation.
func (p *Plugin) newID() string {
	return fmt.Sprintf("job-%d-%d", time.Now().UnixMilli(), p.next.Add(1))
}
