package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/documents"
	"wada-desktop-go/internal/contract/jobs"

	"github.com/xleotranx/wada-kit/kernel"
)

// briefName is the file a job asks for when the plan does not name one.
const briefName = "brief.md"

// turnWait is how long one job may take, from reading the folder to the
// participant's last answer.
const turnWait = 15 * time.Minute

// planSchema is what a planning turn must return: the backend validates it
// through submit_result before the turn can end.
var planSchema = json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"string"},"minItems":1},"deliverable":{"type":"string"},"criteria":{"type":"array","items":{"type":"string"}}},"required":["steps","deliverable","criteria"],"additionalProperties":false}`)

// reviewSchema is what a review turn must return: a verdict and the defects
// behind it.
var reviewSchema = json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string","enum":["pass","revise"]},"summary":{"type":"string"},"defects":{"type":"array","items":{"type":"object","properties":{"where":{"type":"string"},"what":{"type":"string"}},"required":["where","what"],"additionalProperties":false}}},"required":["verdict","summary","defects"],"additionalProperties":false}`)

// maxReviews is how many reviews a job allows: the first, and one after a fix.
const maxReviews = 2

// reviewPrep is what starting a reviewer produced.
type reviewPrep struct {
	job     jobs.Job
	session agent.SessionID
	prompt  string
}

// sourceFile is one document written into the job's source pack.
type sourceFile struct {
	Name  string // in the source pack
	From  string // the document it came from
	Chars int
}

// prepared is what reading the folder and starting the participant produced:
// the session to follow and the prompt to plan with.
type prepared struct {
	job     jobs.Job
	session agent.SessionID
	prompt  string
	sources int
	chars   int
}

// collected is what the participant left behind.
type collected struct {
	text  string
	files []jobs.Deliverable
}

// launch starts a job's first worker: read the folder, then start the team.
func (p *Plugin) launch(job jobs.Job, parent agent.SessionID) {
	kernel.Go(p.ctx, &p.work, func(ctx context.Context) (prepared, error) {
		return p.prepare(ctx, job, parent)
	}, p.prepared)
}

// prepare reads the folder, writes its documents into the job's source pack
// and starts the participant. It runs on a worker.
func (p *Plugin) prepare(ctx context.Context, job jobs.Job, parent agent.SessionID) (prepared, error) {
	ctx, cancel := context.WithTimeout(ctx, turnWait)
	defer cancel()
	// A new chat has no conversation yet, and a child needs one to hang
	// under. It is created here, invisible to the app's own record: it owns
	// the job's children, it is not a chat the user asked for.
	if parent == "" {
		owner, err := p.agent.Create(ctx, agent.CreateSession{
			Title: "Jobs in " + filepath.Base(job.Folder), Directory: job.Folder,
		})
		if err != nil {
			return prepared{job: job}, err
		}
		parent = owner.ID
	}
	res, err := p.docs.Ingest(ctx, job.Folder)
	if err != nil {
		return prepared{job: job}, err
	}
	dir, pack, err := p.writeSources(job, res)
	if err != nil {
		return prepared{job: job}, err
	}
	child, err := p.children.Spawn(ctx, agent.Spawn{
		Parent: parent, Name: "reader", Directory: job.Folder, Policy: jobPolicy(),
	})
	if err != nil {
		return prepared{job: job}, err
	}
	return prepared{
		job:     job,
		session: child.ID,
		prompt:  planPrompt(job, res, dir, pack),
		sources: res.Read,
		chars:   res.Chars,
	}, nil
}

// prepared records the participant and asks it for a plan. It runs on the UI
// thread, before the send, so the job is known when the turn answers.
func (p *Plugin) prepared(prep prepared, err error) {
	if err != nil {
		p.setState(prep.job, jobs.Failed, err.Error())
		return
	}
	job := prep.job
	job.Session = prep.session
	job.Sources = prep.sources
	job.Chars = prep.chars
	job.State = jobs.Planning
	p.upsert(job)
	p.emit(job)
	p.dispatch(job, prep.prompt, planSchema)
}

// execute starts the work of a job whose plan the user approved.
func (p *Plugin) execute(job jobs.Job) {
	job.State = jobs.Running
	p.upsert(job)
	p.emit(job)
	p.dispatch(job, workPrompt(job), nil)
}

// dispatch admits one turn of a job, on a worker. A failed send has an
// unknown outcome and is reported, never retried.
func (p *Plugin) dispatch(job jobs.Job, text string, schema json.RawMessage) {
	kernel.Go(p.ctx, &p.send, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.agent.Send(ctx, agent.Send{ID: job.ID, Session: job.Session, Text: text, ResultSchema: schema})
	}, func(_ agent.SendDisposition, err error) {
		if err != nil {
			p.setState(job, jobs.Failed, err.Error())
		}
	})
}

// planned keeps the plan the agent returned and waits for the user.
func (p *Plugin) planned(job jobs.Job, ev agent.Event) {
	if len(ev.Result) == 0 {
		p.setState(job, jobs.Failed, "The plan came back empty")
		return
	}
	var plan jobs.Plan
	if err := json.Unmarshal(ev.Result, &plan); err != nil || len(plan.Steps) == 0 {
		p.setState(job, jobs.Failed, "The plan came back unreadable")
		return
	}
	if plan.Deliverable == "" {
		plan.Deliverable = briefName
	}
	job.Plan = &plan
	p.setState(job, jobs.PlanReview, "")
}

// finish settles a job from its participant's terminal event.
func (p *Plugin) finish(job jobs.Job, ev agent.Event) {
	if ev.Kind == agent.TurnSucceeded {
		p.collect(job)
		return
	}
	if ev.Kind == agent.TurnFailed {
		p.setState(job, jobs.Failed, ev.Error)
		return
	}
	// A turn that ended any other way — interrupted, above all — was stopped
	// rather than finished.
	p.setState(job, jobs.Cancelled, "")
}

// workDone checks that the deliverable exists, then starts a reviewer. It
// runs on the UI thread after the work turn succeeded.
func (p *Plugin) workDone(job jobs.Job) {
	job.State = jobs.Reviewing
	p.upsert(job)
	p.emit(job)
	kernel.Go(p.ctx, &p.review, func(ctx context.Context) (reviewPrep, error) {
		path := filepath.Join(job.Folder, deliverableName(job))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return reviewPrep{}, errors.New("the deliverable was not written")
		}
		child, err := p.children.Spawn(ctx, agent.Spawn{
			Parent: job.Parent, Name: "reviewer", Directory: job.Folder, Policy: reviewPolicy(),
		})
		if err != nil {
			return reviewPrep{}, err
		}
		return reviewPrep{job: job, session: child.ID, prompt: reviewPrompt(job, path)}, nil
	}, func(prep reviewPrep, err error) {
		if err != nil {
			p.setState(job, jobs.Failed, err.Error())
			return
		}
		job.Reviewer = prep.session
		p.upsert(job)
		p.emit(job)
		p.dispatch(job, prep.prompt, reviewSchema)
	})
}

// reviewed keeps the verdict: a pass finishes, a revise asks for one fix, and
// a second revise accepts the work with its findings.
func (p *Plugin) reviewed(job jobs.Job, ev agent.Event) {
	if len(ev.Result) == 0 {
		p.setState(job, jobs.Failed, "The review came back empty")
		return
	}
	var answer struct {
		Verdict string `json:"verdict"`
		Summary string `json:"summary"`
		Defects []struct {
			Where string `json:"where"`
			What  string `json:"what"`
		} `json:"defects"`
	}
	if err := json.Unmarshal(ev.Result, &answer); err != nil {
		p.setState(job, jobs.Failed, "The review came back unreadable")
		return
	}
	job.Reviews++
	job.Summary = answer.Summary
	job.Findings = nil
	for _, defect := range answer.Defects {
		job.Findings = append(job.Findings, jobs.Finding{Where: defect.Where, What: defect.What})
	}
	if answer.Verdict == "pass" || job.Reviews >= maxReviews {
		p.collect(job)
		return
	}
	job.State = jobs.Running
	job.Reviewer = ""
	p.upsert(job)
	p.emit(job)
	p.dispatch(job, fixPrompt(job), nil)
}

// collect reads the participant's answer and the file the plan named.
func (p *Plugin) collect(job jobs.Job) {
	name := deliverableName(job)
	kernel.Go(p.ctx, &p.settle, func(ctx context.Context) (collected, error) {
		messages, err := p.agent.Messages(ctx, job.Session)
		if err != nil {
			return collected{}, err
		}
		return collected{text: answer(messages), files: deliverables(job.Folder, name)}, nil
	}, func(got collected, err error) {
		if err != nil {
			p.setState(job, jobs.Failed, err.Error())
			return
		}
		job.Result = got.text
		job.Files = got.files
		job.State = jobs.Done
		p.upsert(job)
		p.emit(job)
		p.notify(job)
	})
}

// writeSources keeps the converted documents beside the job, so the
// participant reads files instead of a prompt full of text.
func (p *Plugin) writeSources(job jobs.Job, res documents.Result) (string, []sourceFile, error) {
	dir := filepath.Join(job.Folder, ".wada", "jobs", job.ID, "sources")
	var pack []sourceFile
	for _, src := range res.Sources {
		if !src.Converted() {
			continue
		}
		name := filepath.Base(src.Path) + ".md"
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src.Markdown), 0o600); err != nil {
			return "", nil, err
		}
		pack = append(pack, sourceFile{Name: name, From: filepath.Base(src.Path), Chars: src.Chars})
	}
	return dir, pack, nil
}

// reviewPolicy lets the reviewer read the folder and nothing else: it judges,
// it never edits.
func reviewPolicy() agent.ToolPolicy {
	return agent.ToolPolicy{
		Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch},
		Deny:  []agent.ToolKind{agent.ToolEdit, agent.ToolShell, agent.ToolFetch, agent.ToolOther},
	}
}

// jobPolicy lets the participant read and write in the folder and nothing
// else: no shell, no web, no approving its way around it.
func jobPolicy() agent.ToolPolicy {
	return agent.ToolPolicy{
		Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch, agent.ToolEdit},
		Deny:  []agent.ToolKind{agent.ToolShell, agent.ToolFetch, agent.ToolOther},
	}
}

// reviewPrompt asks a reviewer to judge the deliverable against the plan.
func reviewPrompt(job jobs.Job, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are reviewing work on a job. The user asked:\n\n%s\n\n", job.Goal)
	fmt.Fprintf(&b, "Read the deliverable: %s\n\n", path)
	b.WriteString("Judge it against the acceptance criteria:\n\n")
	if job.Plan != nil {
		for _, criterion := range job.Plan.Criteria {
			b.WriteString("- " + criterion + "\n")
		}
	}
	b.WriteString("\nBe strict: an unsupported number or claim, a missing criterion, or placeholder text is a defect. Do not edit anything.\n")
	b.WriteString("Return pass only when every criterion is met; otherwise return revise with each defect's location and what is wrong there.\n")
	return b.String()
}

// fixPrompt tells the worker what the reviewer found.
func fixPrompt(job jobs.Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The reviewer found these problems with %s:\n\n", deliverableName(job))
	for _, finding := range job.Findings {
		fmt.Fprintf(&b, "- %s: %s\n", finding.Where, finding.What)
	}
	b.WriteString("\nFix them and write the file again. Keep what already passes; do not remove citations or sections the reviewer did not mention.\n")
	return b.String()
}

// planPrompt tells the participant what the job is, where the sources are and
// that it should plan rather than work.
func planPrompt(job jobs.Job, res documents.Result, dir string, pack []sourceFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are planning a job before doing any of it. The user wants:\n\n%s\n\n", job.Goal)
	writeSources(&b, res, dir, pack)
	b.WriteString("Do not start the work yet. Read only what you need to plan well, then submit a plan:\n\n")
	b.WriteString("- steps, in order, at most six; each says what it does and where\n")
	fmt.Fprintf(&b, "- deliverable, the file you will write in the job's folder, such as %s\n", briefName)
	b.WriteString("- criteria, how the finished work should be judged\n")
	return b.String()
}

// workPrompt tells the participant to carry out the plan it made.
func workPrompt(job jobs.Job) string {
	var b strings.Builder
	b.WriteString("Execute this plan now.\n\n")
	name := deliverableName(job)
	if job.Plan != nil {
		for i, step := range job.Plan.Steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
		b.WriteString("\nDeliverable: " + name + "\n")
		if len(job.Plan.Criteria) > 0 {
			b.WriteString("Acceptance criteria:\n")
			for _, criterion := range job.Plan.Criteria {
				b.WriteString("- " + criterion + "\n")
			}
		}
	}
	fmt.Fprintf(&b, "\nWrite the result to %s in the job's folder, as Markdown. Back every number and claim with its source, written [file › heading]. Do not state anything the sources do not say.\n", name)
	return b.String()
}

// writeSources lists the source pack in a prompt.
func writeSources(b *strings.Builder, res documents.Result, dir string, pack []sourceFile) {
	if len(pack) > 0 {
		b.WriteString("The folder's documents were already converted to Markdown, one file each. Read them with your file tools:\n\n")
		for _, f := range pack {
			fmt.Fprintf(b, "- %s → %s (%d chars)\n", f.From, filepath.Join(dir, f.Name), f.Chars)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No document in the folder could be converted. Work with what you can read in the folder itself.\n\n")
	}
	for _, src := range res.Sources {
		if src.Skip != "" {
			fmt.Fprintf(b, "- %s was not read: %s\n", filepath.Base(src.Path), src.Skip)
		}
	}
	if len(res.Sources) > 0 {
		b.WriteString("\n")
	}
}

// deliverableName is the file the job writes: the plan's, or the default.
func deliverableName(job jobs.Job) string {
	if job.Plan != nil && job.Plan.Deliverable != "" {
		return filepath.Base(job.Plan.Deliverable)
	}
	return briefName
}

// answer is the last assistant text of messages.
func answer(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != agent.RoleAssistant {
			continue
		}
		var b strings.Builder
		for _, part := range msg.Parts {
			if part.Kind == agent.PartText {
				b.WriteString(part.Text)
			}
		}
		if text := strings.TrimSpace(b.String()); text != "" {
			return text
		}
	}
	return ""
}

// deliverables lists the files a job left in its folder.
func deliverables(folder, name string) []jobs.Deliverable {
	path := filepath.Join(folder, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil
	}
	return []jobs.Deliverable{{Path: path, Bytes: int(info.Size())}}
}
