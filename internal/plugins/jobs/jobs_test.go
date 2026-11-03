package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/documents"
	"wada-desktop-go/internal/contract/jobs"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// reader is a documents.Reader that answers what a test sets.
type reader struct {
	res documents.Result
	err error
}

func (r *reader) New() kernel.Plugin { return r }

func (*reader) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-reader", Provides: []kernel.Named{documents.ReaderKey}}
}

func (r *reader) Setup(c *kernel.Context) error {
	return kernel.Provide[documents.Reader](c, documents.ReaderKey, r)
}

func (*reader) Read(context.Context, string) (documents.Source, error) {
	return documents.Source{}, nil
}

func (r *reader) Ingest(context.Context, string) (documents.Result, error) { return r.res, r.err }

// start installs the plugin over a fake agent, chat, reader and workspace.
func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	host := &kittest.Host{Data: t.TempDir()}
	fakeAgent := plugintest.NewAgent()
	fakeChat := &plugintest.Chat{}
	fakeChat.Conv = chat.Conversation{DraftID: 1, Session: "parent", Directory: dir}
	folders := &plugintest.Workspace{}
	docs := &reader{res: documents.Result{
		Folder: dir, Read: 1, Chars: 12,
		Sources: []documents.Source{{
			Path: filepath.Join(dir, "memo.docx"), Format: "docx", Chars: 12, Markdown: "# Hi\n\nbody",
		}},
	}}
	var p *Plugin
	h := kerneltest.New(t, host.New, fakeAgent.New, fakeChat.New, docs.New, folders.New,
		kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	return h, p, fakeAgent, folders, dir
}

// brief writes the file a finished job leaves behind.
func brief(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, briefName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestJobPlansThenWritesABrief(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()

	spawns := fakeAgent.SpawnRequests()
	if len(spawns) != 1 || spawns[0].Name != "reader" || spawns[0].Directory != dir || spawns[0].Parent != "parent" {
		t.Fatalf("spawns %+v", spawns)
	}
	planning, ok := p.find(job.ID)
	if !ok || planning.State != jobs.Planning || planning.Sources != 1 || planning.Session != "child-1" {
		t.Fatalf("planning job %+v", planning)
	}
	pack := filepath.Join(dir, ".wada", "jobs", job.ID, "sources", "memo.docx.md")
	if _, err := os.Stat(pack); err != nil {
		t.Fatalf("source pack: %v", err)
	}

	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read the sources","Write brief.md"],"deliverable":"brief.md","criteria":["Every claim cites a source"]}`)})
	h.Flush()

	review, _ := p.find(job.ID)
	if review.State != jobs.PlanReview || review.Plan == nil || len(review.Plan.Steps) != 2 {
		t.Fatalf("review job %+v", review)
	}
	if err := p.Run(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	running, _ := p.find(job.ID)
	if running.State != jobs.Running {
		t.Fatalf("running job %+v", running)
	}

	fakeAgent.Stored = map[agent.SessionID][]agent.Message{
		"child-1": {{Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartText, Text: "The brief says hi."}}}},
	}
	brief(t, dir, "# The brief")
	fakeAgent.Respond("child-1", "The brief says hi.")
	h.Flush()

	reviewing, _ := p.find(job.ID)
	if reviewing.State != jobs.Reviewing || reviewing.Reviews != 0 {
		t.Fatalf("reviewing job %+v", reviewing)
	}
	if spawns := fakeAgent.SpawnRequests(); len(spawns) != 2 || spawns[1].Name != "reviewer" {
		t.Fatalf("spawns %+v", spawns)
	}
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-2", Result: json.RawMessage(
		`{"verdict":"pass","summary":"Meets the criteria","defects":[]}`)})
	h.Flush()

	done, _ := p.find(job.ID)
	if done.State != jobs.Done || done.Result != "The brief says hi." {
		t.Fatalf("done job %+v", done)
	}
	if len(done.Files) != 1 || filepath.Base(done.Files[0].Path) != briefName {
		t.Fatalf("deliverables %+v", done.Files)
	}
}

func TestReviewSendsOneFixRound(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Write brief.md"],"deliverable":"brief.md","criteria":["Every claim cites a source"]}`)})
	h.Flush()
	if err := p.Run(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	brief(t, dir, "# The brief")
	fakeAgent.Respond("child-1", "The brief is ready.")
	h.Flush()

	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-2", Result: json.RawMessage(
		`{"verdict":"revise","summary":"One claim has no source","defects":[{"where":"Reach section","what":"the 18% number cites nothing"}]}`)})
	h.Flush()
	fixing, _ := p.find(job.ID)
	if fixing.State != jobs.Running || fixing.Reviews != 1 || len(fixing.Findings) != 1 {
		t.Fatalf("fixing job %+v", fixing)
	}

	fakeAgent.Respond("child-1", "Added the citation.")
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-3", Result: json.RawMessage(
		`{"verdict":"pass","summary":"Clean","defects":[]}`)})
	h.Flush()
	done, _ := p.find(job.ID)
	if done.State != jobs.Done || done.Reviews != 2 || len(done.Findings) != 0 {
		t.Fatalf("done job %+v", done)
	}
}

func TestMissingDeliverableFailsBeforeReview(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Write brief.md"],"deliverable":"brief.md","criteria":["c"]}`)})
	h.Flush()
	if err := p.Run(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Respond("child-1", "I forgot the file.")
	h.Flush()
	failed, _ := p.find(job.ID)
	if failed.State != jobs.Failed || failed.Problem != "the deliverable was not written" {
		t.Fatalf("failed job %+v", failed)
	}
	if spawns := fakeAgent.SpawnRequests(); len(spawns) != 1 {
		t.Fatalf("a reviewer was spawned without a deliverable: %+v", spawns)
	}
}

func TestPlanIsRequiredBeforeWork(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if err := p.Run(job.ID); err == nil {
		t.Fatal("work ran before a plan waited")
	}
	if _, ok := p.find(job.ID); !ok {
		t.Fatal("the job disappeared")
	}
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1"})
	h.Flush()
	failed, _ := p.find(job.ID)
	if failed.State != jobs.Failed || failed.Problem != "The plan came back empty" {
		t.Fatalf("failed job %+v", failed)
	}
}

func TestFailedTurnIsReported(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnFailed, Session: "child-1", Error: "ran out of context"})
	h.Flush()
	failed, _ := p.find(job.ID)
	if failed.State != jobs.Failed || failed.Problem != "ran out of context" {
		t.Fatalf("failed job %+v", failed)
	}
}

func TestCancelStopsTheParticipant(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if err := p.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	cancelled, _ := p.find(job.ID)
	if cancelled.State != jobs.Cancelled {
		t.Fatalf("cancelled job %+v", cancelled)
	}
	if calls := fakeAgent.Calls(); !contains(calls, "abort child-1") {
		t.Fatalf("abort was not called: %q", calls)
	}
}

func TestStartNeedsAGoalFolderAndTeam(t *testing.T) {
	_, p, _, _, dir := start(t)
	if _, err := p.Start("", dir); err == nil {
		t.Fatal("started without a goal")
	}
	if _, err := p.Start("goal", ""); err == nil {
		t.Fatal("started without a folder")
	}
}

func TestOnlyOneJobRunsAtATime(t *testing.T) {
	h, p, _, _, dir := start(t)
	if _, err := p.Start("first", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if _, err := p.Start("second", dir); err == nil {
		t.Fatal("started a second job while one is running")
	}
}

func TestRestartReportsRunningJobsAsStopped(t *testing.T) {
	_, p, _, _, _ := start(t)
	p.restore([]jobs.Job{{ID: "job-1", State: jobs.Running, Goal: "old"}}, nil)
	if len(p.jobs) != 1 || p.jobs[0].State != jobs.Failed || p.jobs[0].Problem == "" {
		t.Fatalf("restored %+v", p.jobs)
	}
}

// contains reports that calls holds call.
func contains(calls []string, call string) bool {
	for _, c := range calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestPlanCardShowsStepsBeforeRunning(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	if _, err := p.Start("Summarise the folder", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read the sources","Write brief.md"],"deliverable":"brief.md","criteria":["Every claim cites a source"]}`)})
	h.Flush()

	tt := ui.NewTester(p.view, 360, 640)
	tt.Frame()
	for _, want := range []string{"Read the sources", "Write brief.md", "Run plan", "Discard"} {
		if !tt.HasText(want) {
			t.Fatalf("the card does not show %q: %q", want, tt.Texts())
		}
	}
}

func TestRestoreKeepsJobsStartedBeforeItLoaded(t *testing.T) {
	_, p, _, _, dir := start(t)
	live, err := p.Start("started early", dir)
	if err != nil {
		t.Fatal(err)
	}
	p.restore([]jobs.Job{{ID: "stored", State: jobs.PlanReview, Goal: "old"}}, nil)
	if _, ok := p.find(live.ID); !ok {
		t.Fatal("the job started before the load was dropped")
	}
	if _, ok := p.find("stored"); !ok {
		t.Fatal("the stored job was not loaded")
	}
}

func TestJobCreatesAnOwnerWhenTheChatIsNew(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	fresh := &plugintest.Chat{}
	fresh.Conv = chat.Conversation{DraftID: 2, Directory: dir}
	p.chat = fresh
	if _, err := p.Start("Summarise the folder", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	spawns := fakeAgent.SpawnRequests()
	if len(spawns) != 1 || spawns[0].Parent != "new" {
		t.Fatalf("spawns %+v", spawns)
	}
	if calls := fakeAgent.Calls(); !contains(calls, "create "+dir) {
		t.Fatalf("no owner conversation was created: %q", calls)
	}
}

func TestJobsPageShowsTheChosenPlan(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	if _, err := p.Start("Summarise the folder", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read the sources","Write brief.md"],"deliverable":"brief.md","criteria":["Every claim cites a source"]}`)})
	h.Flush()

	tt := ui.NewTester(p.page, 1000, 700)
	tt.Frame()
	for _, want := range []string{"Summarise the folder", "Read the sources", "Deliverable · brief.md", "Run plan"} {
		if !tt.HasText(want) {
			t.Fatalf("the page does not show %q: %q", want, tt.Texts())
		}
	}
}

func TestHomeOffersTheJobsPage(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	if _, err := p.Start("Summarise the folder", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Write brief.md"],"deliverable":"brief.md","criteria":["c"]}`)})
	h.Flush()
	tt := ui.NewTester(p.home, 640, 400)
	tt.Frame()
	if !tt.HasText("All jobs") || !tt.HasText("Write brief.md") {
		t.Fatalf("the home block does not show the job and the page link: %q", tt.Texts())
	}
}

func TestTitleButtonOpensThePage(t *testing.T) {
	_, p, _, _, _ := start(t)
	tt := ui.NewTester(p.titleButton, 60, 40)
	tt.Frame()
	if !tt.HasText("Jobs") {
		t.Fatalf("the title button has no label: %q", tt.Texts())
	}
}

func TestJobsPageStartsAJob(t *testing.T) {
	h, p, _, _, dir := start(t)
	p.folder = dir
	p.draft = "Summarise the folder"
	tt := ui.NewTester(p.page, 1200, 800)
	tt.Frame()
	if !tt.HasText("Start job") || !tt.HasText(dir) {
		t.Fatalf("the head does not offer the controls: %q", tt.Texts())
	}
	if err := tt.Click("Start job"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	list := p.Jobs()
	if len(list) != 1 || list[0].Goal != "Summarise the folder" || list[0].Folder != dir {
		t.Fatalf("jobs %+v", list)
	}
	if p.draft != "" {
		t.Fatalf("the goal stayed in the field: %q", p.draft)
	}
}

func TestJobsPageChoosesAFolder(t *testing.T) {
	_, p, _, folders, dir := start(t)
	folders.Chosen = dir
	tt := ui.NewTester(p.page, 1200, 800)
	tt.Frame()
	if err := tt.Click("Choose folder"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.folder != dir {
		t.Fatalf("folder %q, want %q", p.folder, dir)
	}
}

func TestSeedFolderPrefersTheLastJob(t *testing.T) {
	_, p, _, _, dir := start(t)
	p.jobs = []jobs.Job{{ID: "old", Folder: "/last/job"}}
	p.seedFolder()
	if p.folder != "/last/job" {
		t.Fatalf("seeded %q", p.folder)
	}
	p.folder = ""
	p.jobs = nil
	fresh := &plugintest.Chat{}
	fresh.Conv = chat.Conversation{DraftID: 3, Directory: dir}
	p.chat = fresh
	p.seedFolder()
	if p.folder != dir {
		t.Fatalf("seeded %q from the chat", p.folder)
	}
}

// hasCall reports that some recorded call contains text.
func hasCall(calls []string, text string) bool {
	for _, call := range calls {
		if strings.Contains(call, text) {
			return true
		}
	}
	return false
}

func TestPlanCanBeEditedBeforeRunning(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read everything"],"deliverable":"brief.md","criteria":["cites sources"]}`)})
	h.Flush()

	review, _ := p.find(job.ID)
	p.startEdit(review)
	if p.editing != job.ID {
		t.Fatal("the plan did not open for editing")
	}
	p.planSteps = []string{"Read the invoices only", "Cite the totals"}
	p.planDeliverable = "summary.md"
	p.savePlan(job.ID)
	h.Flush()

	edited, _ := p.find(job.ID)
	if len(edited.Plan.Steps) != 2 || edited.Plan.Steps[0] != "Read the invoices only" ||
		edited.Plan.Deliverable != "summary.md" {
		t.Fatalf("edited plan %+v", edited.Plan)
	}
	if p.editing != "" {
		t.Fatal("the editor stayed open after saving")
	}
	if err := p.Run(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if calls := fakeAgent.Calls(); !hasCall(calls, "Read the invoices only") || !hasCall(calls, "summary.md") {
		t.Fatalf("the work prompt does not follow the edited plan: %q", calls)
	}
}

func TestPlanEditNeedsAStep(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read everything"],"deliverable":"brief.md","criteria":[]}`)})
	h.Flush()

	review, _ := p.find(job.ID)
	p.startEdit(review)
	p.planSteps = []string{"   "}
	p.savePlan(job.ID)
	if p.editing != job.ID {
		t.Fatal("the editor closed on an empty plan")
	}
	kept, _ := p.find(job.ID)
	if kept.Plan.Steps[0] != "Read everything" {
		t.Fatalf("the plan changed: %+v", kept.Plan)
	}
}

func TestPlanIsEditableOnlyOnThePage(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	if _, err := p.Start("Summarise the folder", dir); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read everything"],"deliverable":"brief.md","criteria":[]}`)})
	h.Flush()

	page := ui.NewTester(p.page, 1200, 800)
	page.Frame()
	if !page.HasText("Edit plan") {
		t.Fatalf("the page hides the editor: %q", page.Texts())
	}
	home := ui.NewTester(p.home, 640, 400)
	home.Frame()
	if home.HasText("Edit plan") {
		t.Fatalf("the new chat block offers editing: %q", home.Texts())
	}
	review, _ := p.find(p.Jobs()[0].ID)
	p.startEdit(review)
	page = ui.NewTester(p.page, 1200, 800)
	page.Frame()
	if !page.HasText("Save plan") || !page.HasText("Add step") || !page.HasText("Deliverable") {
		t.Fatalf("the editor does not show its fields: %q", page.Texts())
	}
}

// planJob starts a job and lets its plan arrive, so a test can watch what the
// state change does.
func planJob(t *testing.T, h *kerneltest.Harness, p *Plugin, fakeAgent *plugintest.Agent, dir string) jobs.Job {
	t.Helper()
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read everything"],"deliverable":"brief.md","criteria":["cites sources"]}`)})
	h.Flush()
	return job
}

func TestPlanReadyNotifiesWhenTheAppIsBehind(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	host := p.host.(*kittest.Host)
	host.Background = true
	job := planJob(t, h, p, fakeAgent, dir)
	if len(host.Notices) != 1 {
		t.Fatalf("notices %+v", host.Notices)
	}
	notice := host.Notices[0]
	if notice.ID != "job:"+job.ID || notice.Title != "Plan ready to run" || notice.Subtitle != job.Goal {
		t.Fatalf("notice %+v", notice)
	}
}

func TestNoNoticeWhileLookingAtThePage(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	host := p.host.(*kittest.Host)
	host.ShowPage(jobs.PageID)
	planJob(t, h, p, fakeAgent, dir)
	if len(host.Notices) != 0 {
		t.Fatalf("notices %+v while the page was on show", host.Notices)
	}
}

func TestNoticeClickOpensTheJob(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	host := p.host.(*kittest.Host)
	job := planJob(t, h, p, fakeAgent, dir)
	p.selected = ""
	kernel.Emit(h, shell.NoticeClicked, "job:"+job.ID)
	if p.selected != job.ID || host.PageID != jobs.PageID {
		t.Fatalf("selected %q, page %q", p.selected, host.PageID)
	}
}

func TestFinishedJobNotifiesOnce(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	host := p.host.(*kittest.Host)
	host.Background = true
	job := planJob(t, h, p, fakeAgent, dir)
	if err := p.Run(job.ID); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	brief(t, dir, "# Brief")
	fakeAgent.Respond("child-1", "Travel spend came to 4,210.50.")
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-2", Result: json.RawMessage(
		`{"verdict":"pass","summary":"ok","defects":[]}`)})
	h.Flush()
	var finished int
	for _, notice := range host.Notices {
		if notice.Title == "Job finished" {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("finished notices %d in %+v", finished, host.Notices)
	}
}

func TestRemoveForgetsAJobAndItsPack(t *testing.T) {
	h, p, _, _, dir := start(t)
	pack := filepath.Join(dir, ".wada", "jobs", "job-old")
	if err := os.MkdirAll(filepath.Join(pack, "sources"), 0o700); err != nil {
		t.Fatal(err)
	}
	p.jobs = []jobs.Job{{ID: "job-old", Goal: "old", Folder: dir, State: jobs.Done}}
	p.selected = "job-old"
	if err := p.Remove("job-old"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if len(p.jobs) != 0 || p.selected != "" {
		t.Fatalf("jobs %+v, selected %q", p.jobs, p.selected)
	}
	if _, err := os.Stat(pack); !os.IsNotExist(err) {
		t.Fatalf("the source pack is still there: %v", err)
	}
}

func TestRemoveRefusesAnActiveJob(t *testing.T) {
	h, p, fakeAgent, _, dir := start(t)
	job, err := p.Start("Summarise the folder", dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Flush()
	fakeAgent.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: "child-1", Result: json.RawMessage(
		`{"steps":["Read"],"deliverable":"brief.md","criteria":[]}`)})
	h.Flush()
	if err := p.Remove(job.ID); err == nil {
		t.Fatal("an active job was removed")
	}
	if _, ok := p.find(job.ID); !ok {
		t.Fatal("the job is gone after a refused removal")
	}
}

func TestPageOffersRemoveForSettledJobs(t *testing.T) {
	_, p, _, _, _ := start(t)
	p.jobs = []jobs.Job{{ID: "job-done", Goal: "Finished work", State: jobs.Done}}
	tt := ui.NewTester(p.page, 1200, 800)
	tt.Frame()
	if !tt.HasText("Remove") {
		t.Fatalf("the page does not offer removal: %q", tt.Texts())
	}
	if tt.HasText("Stop") {
		t.Fatalf("a settled job offers Stop: %q", tt.Texts())
	}
}
