package jobs

import (
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/jobs"

	"github.com/egoist/mygo/ui"
)

// startEdit opens a job's plan for changing. The plan is required: a job
// without one has nothing to edit.
func (p *Plugin) startEdit(job jobs.Job) {
	if job.Plan == nil {
		return
	}
	p.editing = job.ID
	p.planSteps = slices.Clone(job.Plan.Steps)
	p.planDeliverable = job.Plan.Deliverable
	p.planCriteria = slices.Clone(job.Plan.Criteria)
}

// addStep appends an empty step to the plan being edited.
func (p *Plugin) addStep() {
	p.planSteps = append(p.planSteps, "")
	p.ctx.Invalidate()
}

// savePlan keeps the edited plan. A plan with no step is refused, so the
// worker always has something to follow.
func (p *Plugin) savePlan(id string) {
	job, ok := p.find(id)
	if !ok {
		p.editing = ""
		return
	}
	var steps []string
	for _, step := range p.planSteps {
		if step = strings.TrimSpace(step); step != "" {
			steps = append(steps, step)
		}
	}
	if len(steps) == 0 {
		p.host.Toast(ui.Toast{Title: "A plan needs at least one step"})
		return
	}
	var criteria []string
	for _, criterion := range p.planCriteria {
		if criterion = strings.TrimSpace(criterion); criterion != "" {
			criteria = append(criteria, criterion)
		}
	}
	plan := jobs.Plan{
		Steps:       steps,
		Deliverable: strings.TrimSpace(p.planDeliverable),
		Criteria:    criteria,
	}
	if plan.Deliverable == "" {
		plan.Deliverable = briefName
	}
	job.Plan = &plan
	p.upsert(job)
	p.emit(job)
	p.editing = ""
}
