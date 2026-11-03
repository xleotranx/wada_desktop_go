package jobs

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/jobs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/native"
)

// maxShown is how many jobs the work panel and the new chat list.
const maxShown = 3

// maxFindings is how many of a job's findings are listed.
const maxFindings = 3

// shown reports that there are jobs to list beside a conversation.
func (p *Plugin) shown() bool { return len(p.jobs) > 0 }

// homeShown reports that the new chat has something to say about jobs: what
// is running, or how to start one now that a folder is open.
func (p *Plugin) homeShown() bool {
	return len(p.jobs) > 0 || p.chat.Current().Directory != ""
}

// view lists the newest jobs beside the conversation: what was asked, how far
// it got and what came out.
func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		for _, job := range p.recent() {
			ui.Column(c.Key(job.ID)).FillWidth().Gap(1).Padding(6, 4, 2, 4).Children(func() {
				p.jobBlock(c, pal, job)
			})
		}
	})
}

// home lists the same jobs under the starting points of a new chat, so a job
// is visible before any conversation exists. With a folder open and no job
// yet, it says how to start one.
func (p *Plugin) home(c *ui.Context) {
	pal := native.PaletteFor(c)
	if len(p.jobs) == 0 {
		ui.Text(c, "Type the goal above, then run Agent ▸ Start Job from Draft. The job reads the folder and works in the background.").
			FontSize(11).TextColor(pal.Faint)
		return
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		for _, job := range p.recent() {
			ui.Column(c.Key("home:"+job.ID)).FillWidth().Gap(1).Padding(4, 0, 2, 0).Children(func() {
				p.jobBlock(c, pal, job)
			})
		}
		ui.Row(c).Gap(6).Padding(2, 0, 0, 0).Children(func() { p.allJobs(c) })
	})
}

// recent is the newest jobs, at most maxShown.
func (p *Plugin) recent() []jobs.Job {
	list := p.Jobs()
	if len(list) > maxShown {
		list = list[:maxShown]
	}
	return list
}

// jobBlock is one job: what was asked, how far it got, its plan while it
// waits, and what came out.
func (p *Plugin) jobBlock(c *ui.Context, pal native.Palette, job jobs.Job) {
	ui.Text(c, job.Goal).FontSize(12).TextColor(pal.Text)
	ui.Text(c, status(job)).FontSize(11).TextColor(pal.Faint)
	if job.State == jobs.PlanReview {
		p.planCard(c, pal, job, false)
		return
	}
	if job.Result != "" {
		ui.Text(c, oneLine(job.Result)).FontSize(11).TextColor(pal.Muted)
	}
	for i, finding := range job.Findings {
		if i >= maxFindings {
			break
		}
		ui.Text(c, "• "+finding.Where+": "+finding.What).FontSize(11).TextColor(pal.Warning)
	}
}

// planCard shows what the job means to do, before it does it. Editable adds
// the way to change it, on the page where there is room.
func (p *Plugin) planCard(c *ui.Context, pal native.Palette, job jobs.Job, editable bool) {
	if p.editing == job.ID {
		p.planEditor(c, pal, job)
		return
	}
	ui.Column(c.Key("plan:"+job.ID)).FillWidth().Gap(2).Padding(4, 0, 0, 0).Children(func() {
		if job.Plan == nil {
			ui.Text(c, "Planning…").FontSize(11).TextColor(pal.Faint)
			return
		}
		for i, step := range job.Plan.Steps {
			ui.Text(c, fmt.Sprintf("%d. %s", i+1, step)).FontSize(12).TextColor(pal.Text)
		}
		if job.Plan.Deliverable != "" {
			ui.Text(c, "Deliverable · "+filepath.Base(job.Plan.Deliverable)).
				FontSize(11).TextColor(pal.Muted)
		}
		if len(job.Plan.Criteria) > 0 {
			ui.Text(c, "Done when · "+strings.Join(job.Plan.Criteria, "; ")).
				FontSize(11).TextColor(pal.Faint)
		}
		ui.Row(c.Key("actions:"+job.ID)).Gap(6).Padding(6, 0, 0, 0).Children(func() {
			components.Button(c, "Run plan", components.ButtonPrimary).OnClick(func() {
				if err := p.Run(job.ID); err != nil {
					p.host.Toast(ui.Toast{Title: err.Error()})
				}
			})
			if editable {
				components.Button(c, "Edit plan", components.ButtonSecondary).OnClick(func() { p.startEdit(job) })
			}
			components.Button(c, "Discard", components.ButtonGhost).OnClick(func() {
				if err := p.Cancel(job.ID); err != nil {
					p.host.Toast(ui.Toast{Title: err.Error()})
				}
			})
		})
	})
}

// planEditor is the plan while the user changes it: one field per step, the
// deliverable and its criteria.
func (p *Plugin) planEditor(c *ui.Context, pal native.Palette, job jobs.Job) {
	ui.Column(c.Key("plan.edit:"+job.ID)).FillWidth().Gap(4).Padding(4, 0, 0, 0).Children(func() {
		for i := range p.planSteps {
			ui.Row(c.Key("plan.step:" + strconv.Itoa(i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d.", i+1)).FontSize(12).TextColor(pal.Faint)
				ui.TextInput(c.Key("plan.field:"+job.ID+":"+strconv.Itoa(i)), &p.planSteps[i]).MinHeight(28).Grow(1)
			})
		}
		ui.Row(c).Gap(6).Children(func() {
			components.Button(c, "Add step", components.ButtonGhost).OnClick(p.addStep)
		})
		ui.Text(c, "Deliverable").FontSize(11).TextColor(pal.Faint)
		ui.TextInput(c.Key("plan.deliverable:"+job.ID), &p.planDeliverable).MinHeight(28).Placeholder(briefName)
		if len(p.planCriteria) > 0 {
			ui.Text(c, "Done when").FontSize(11).TextColor(pal.Faint)
			for i := range p.planCriteria {
				ui.TextInput(c.Key("plan.criterion:"+job.ID+":"+strconv.Itoa(i)), &p.planCriteria[i]).MinHeight(28)
			}
		}
		ui.Row(c.Key("plan.save:"+job.ID)).Gap(6).Padding(4, 0, 0, 0).Children(func() {
			components.Button(c, "Save plan", components.ButtonPrimary).OnClick(func() { p.savePlan(job.ID) })
			components.Button(c, "Cancel", components.ButtonGhost).OnClick(func() {
				p.editing = ""
				p.ctx.Invalidate()
			})
		})
	})
}

// status says how far a job got and what it has to show.
func status(job jobs.Job) string {
	if job.State == jobs.Failed {
		if job.Problem != "" {
			return "Failed · " + oneLine(job.Problem)
		}
		return "Failed"
	}
	if job.State == jobs.Cancelled {
		return "Stopped"
	}
	if job.State == jobs.PlanReview && job.Plan != nil {
		return fmt.Sprintf("Plan ready · %d steps", len(job.Plan.Steps))
	}
	if job.State == jobs.Reviewing {
		return fmt.Sprintf("Reviewing · round %d", job.Reviews+1)
	}
	if job.State == jobs.Done {
		if len(job.Findings) > 0 {
			return fmt.Sprintf("Done · %d issues", len(job.Findings))
		}
		if len(job.Files) > 0 {
			return fmt.Sprintf("Done · %s", filepath.Base(job.Files[0].Path))
		}
		return "Done"
	}
	line := job.State.String()
	if job.Sources > 0 {
		line += fmt.Sprintf(" · %d documents", job.Sources)
	}
	return line
}

// oneLine cuts text to the words before its first line break, for the
// section's narrow rows.
func oneLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if runes := []rune(text); len(runes) > 120 {
		text = string(runes[:119]) + "…"
	}
	return text
}
