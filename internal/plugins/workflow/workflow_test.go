package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func fixture(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, chan agent.Send) {
	t.Helper()
	a := plugintest.NewAgent()
	sent := make(chan agent.Send, 30)
	a.OnSend = func(req agent.Send) { sent <- req }
	var p *Plugin
	h := kerneltest.New(t, a.New, (&kittest.Host{}).New, (&plugintest.Chat{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	kernel.Contribute(h, wf.Definitions, kernel.HostPrefix+"test", 0, wf.Flow{ID: kernel.HostPrefix + "test", Title: "Test flow", Plan: "plan", Work: "work", Review: "review", Fix: "fix", Route: "route", Final: "final", MaxReviews: 3})
	return h, p, a, sent
}
func next(t *testing.T, h *kerneltest.Harness, sent chan agent.Send) agent.Send {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.Flush()
		select {
		case req := <-sent:
			return req
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("no next turn")
	return agent.Send{}
}
func finish(h *kerneltest.Harness, a *plugintest.Agent, req agent.Send, result string) {
	a.Emit(agent.Event{Kind: agent.TurnSucceeded, Session: req.Session, RequestID: req.ID, Result: json.RawMessage(result)})
	h.Flush()
}
func TestMainOwnsTaskAndReviewIsIndependent(t *testing.T) {
	h, p, a, sent := fixture(t)
	if err := p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement", Mode: "full", Model: agent.ModelRef{ID: "model", Variant: "high"}, Anchor: 1}); err != nil {
		t.Fatal(err)
	}
	plan := next(t, h, sent)
	if plan.Session != "main" || plan.Text != "implement" || plan.Policy == nil || plan.Internal {
		t.Fatalf("plan %+v", plan)
	}
	finish(h, a, plan, `{"status":"ready","summary":"the plan"}`)
	code := next(t, h, sent)
	if code.Session != "main" || !code.Internal || code.Policy != nil {
		t.Fatalf("code %+v", code)
	}
	finish(h, a, code, `{"status":"ready","summary":"changes and passing checks"}`)
	review := next(t, h, sent)
	if review.Session == "main" || review.Policy == nil {
		t.Fatalf("review %+v", review)
	}
	children := a.SpawnRequests()
	if len(children) != 1 || children[0].Parent != "main" || children[0].Model.Variant != "high" {
		t.Fatalf("children %+v", children)
	}
	finish(h, a, review, `{"status":"issues","summary":"bug","findings":[{"file":"x.go","severity":"major","evidence":"fails on empty input"}]}`)
	fix := next(t, h, sent)
	if fix.Session != "main" {
		t.Fatal("fix left main conversation")
	}
	finish(h, a, fix, `{"status":"ready","summary":"fixed and checked"}`)
	second := next(t, h, sent)
	if second.Session == review.Session {
		t.Fatal("review context reused")
	}
	finish(h, a, second, `{"status":"pass","summary":"verified","findings":[]}`)
	final := next(t, h, sent)
	finish(h, a, final, `{"status":"ready","summary":"finished"}`)
	if p.Busy("main") || p.lanes["main"].task().Outcome != "Passed" {
		t.Fatalf("lane %+v", p.lanes["main"])
	}
}
func TestQueueQuestionsAndRelatedChanges(t *testing.T) {
	h, p, a, sent := fixture(t)
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	plan := next(t, h, sent)
	_ = p.Admit(chat.Draft{Session: "main", Text: "why?"})
	h.Flush()
	select {
	case req := <-sent:
		t.Fatalf("concurrent send %+v", req)
	default:
	}
	finish(h, a, plan, `{"status":"ready","summary":"plan"}`)
	question := next(t, h, sent)
	if question.Text != "why?" || question.Policy == nil {
		t.Fatalf("question %+v", question)
	}
	finish(h, a, question, `{"action":"answer","summary":"because"}`)
	code := next(t, h, sent)
	if code.Instructions != "work" {
		t.Fatalf("did not continue: %+v", code)
	}
	finish(h, a, code, `{"status":"ready","summary":"work"}`)
	review := next(t, h, sent)
	_ = p.Admit(chat.Draft{Session: "main", Text: "fix edge case"})
	finish(h, a, review, `{"status":"pass","summary":"good","findings":[]}`)
	route := next(t, h, sent)
	finish(h, a, route, `{"action":"continue","summary":"handle edge case"}`)
	changed := next(t, h, sent)
	if changed.Instructions != "work" || p.lanes["main"].task().Round != 0 {
		t.Fatal("related change did not start a new review cycle")
	}
}
func TestMissingResultDoesNotPass(t *testing.T) {
	h, p, a, sent := fixture(t)
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	req := next(t, h, sent)
	finish(h, a, req, "")
	if p.lanes["main"].task().State != "failed" || p.Busy("main") {
		t.Fatalf("missing result accepted: %+v", p.lanes["main"])
	}
}

func TestReviewLimitAndIncompleteAreExplicit(t *testing.T) {
	for _, status := range []string{"issues", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			h, p, a, sent := fixture(t)
			_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
			finish(h, a, next(t, h, sent), `{"status":"ready","summary":"plan"}`)
			finish(h, a, next(t, h, sent), `{"status":"ready","summary":"work"}`)
			reviews := 1
			if status == "issues" {
				reviews = 3
			}
			for i := 0; i < reviews; i++ {
				req := next(t, h, sent)
				result := `{"status":"incomplete","summary":"cannot inspect","findings":[]}`
				if status == "issues" {
					result = `{"status":"issues","summary":"still broken","findings":[{"file":"a.go","severity":"blocker","evidence":"crashes"}]}`
				}
				finish(h, a, req, result)
				if i+1 < reviews {
					finish(h, a, next(t, h, sent), `{"status":"ready","summary":"fixed and checked"}`)
				}
			}
			finish(h, a, next(t, h, sent), `{"status":"ready","summary":"reported"}`)
			outcome := "Issues remain"
			if status == "incomplete" {
				outcome = "Review incomplete"
			}
			if got := p.lanes["main"].task().Outcome; got != outcome {
				t.Fatalf("outcome %q", got)
			}
			if len(a.SpawnRequests()) != reviews {
				t.Fatal("wrong review count")
			}
		})
	}
}
func TestUnrelatedTaskPausesThenResumeUsesMain(t *testing.T) {
	h, p, a, sent := fixture(t)
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	plan := next(t, h, sent)
	_ = p.Admit(chat.Draft{Session: "main", Text: "different task"})
	finish(h, a, plan, `{"status":"ready","summary":"plan"}`)
	route := next(t, h, sent)
	finish(h, a, route, `{"action":"new_task","summary":"unrelated"}`)
	ordinary := next(t, h, sent)
	if len(ordinary.ResultSchema) > 0 || ordinary.Policy != nil || !ordinary.Internal {
		t.Fatalf("ordinary %+v", ordinary)
	}
	finish(h, a, ordinary, "")
	l := p.lanes["main"]
	if l.task().State != "paused" || l.Associated != "" || p.Busy("main") {
		t.Fatalf("lane %+v", l)
	}
	p.resume(l, l.task())
	resumed := next(t, h, sent)
	if resumed.Session != "main" || resumed.Instructions != "work" {
		t.Fatalf("resume %+v", resumed)
	}
}
func TestStopPreservesQueueAndRejectsLateResults(t *testing.T) {
	h, p, a, sent := fixture(t)
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	first := next(t, h, sent)
	_ = p.Admit(chat.Draft{Session: "main", Text: "queued"})
	p.Stop("main")
	h.Flush()
	finish(h, a, first, `{"status":"ready","summary":"late"}`)
	l := p.lanes["main"]
	if len(l.Queue) != 1 || l.task().State != "stopped" || p.Busy("main") {
		t.Fatalf("late result resumed task %+v", l)
	}
	select {
	case req := <-sent:
		t.Fatalf("automatic send %+v", req)
	default:
	}
	p.resume(l, l.task())
	retry := next(t, h, sent)
	if retry.ID == first.ID || retry.Session != first.Session {
		t.Fatal("retry identity invalid")
	}
	finish(h, a, first, `{"status":"ready","summary":"stale"}`)
	if !l.Working || l.Pending.Request.ID != retry.ID {
		t.Fatal("stale turn affected retry")
	}
}
func TestFailureNeverRetriesAndPartialSpawnIsRetained(t *testing.T) {
	h, p, a, sent := fixture(t)
	a.OnSpawn = func(req agent.Spawn) (agent.Session, error) {
		return agent.Session{ID: "partial-reviewer"}, errors.New("unknown creation outcome")
	}
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"plan"}`)
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"work"}`)
	h.Flush()
	l := p.lanes["main"]
	if l.Pending.Request.Session != "partial-reviewer" || l.task().State != "failed" {
		t.Fatalf("partial creation lost %+v", l)
	}
	calls := a.Called("spawn")
	h.Flush()
	if a.Called("spawn") != calls {
		t.Fatal("automatically retried creation")
	}
	p.resume(l, l.task())
	req := next(t, h, sent)
	if req.Session != "partial-reviewer" || a.Called("spawn") != calls {
		t.Fatal("retry created another reviewer")
	}
}
func TestReviewerQuestionsAndPermissionsUseRealOwner(t *testing.T) {
	h, p, a, sent := fixture(t)
	_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"plan"}`)
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"work"}`)
	review := next(t, h, sent)
	var incoming []agent.Event
	kernel.On(h, chat.Incoming, func(batch []agent.Event) { incoming = append(incoming, batch...) })
	a.Emit(agent.Event{Kind: agent.ApprovalRequested, Session: review.Session, RequestID: review.ID, Approval: &agent.Approval{ID: "approve"}}, agent.Event{Kind: agent.InteractionRequested, Session: review.Session, RequestID: review.ID, Interaction: &agent.Interaction{ID: "question", Kind: agent.Confirm, Title: "Need a decision"}})
	h.Flush()
	if len(incoming) != 2 || incoming[0].Session != "main" || incoming[0].Approval.Session != review.Session || incoming[1].Interaction.Session != review.Session {
		t.Fatalf("requests %+v", incoming)
	}
	if a.Called("answer") != 0 {
		t.Fatal("question was dismissed")
	}
	kernel.Emit(h, chat.RequestAnswered, chat.Answered{Session: review.Session, Approval: "approve"})
	if len(p.lanes["main"].requests) != 1 || p.lanes["main"].requests[0].Interaction == nil {
		t.Fatal("answered approval stayed in replay state")
	}

}
func TestDeletionAndDisposalAbortChild(t *testing.T) {
	for _, dispose := range []bool{false, true} {
		t.Run(fmt.Sprint(dispose), func(t *testing.T) {
			h, p, a, sent := fixture(t)
			_ = p.Admit(chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Text: "implement"})
			finish(h, a, next(t, h, sent), `{"status":"ready","summary":"plan"}`)
			finish(h, a, next(t, h, sent), `{"status":"ready","summary":"work"}`)
			req := next(t, h, sent)
			if dispose {
				if err := h.Disable("workflow"); err != nil {
					t.Fatal(err)
				}
			} else {
				kernel.Emit(h, agent.Deleted, agent.SessionID("main"))
			}
			deadline := time.Now().Add(time.Second)
			for a.Called("abort "+string(req.Session)) == 0 && time.Now().Before(deadline) {
				h.Flush()
				time.Sleep(time.Millisecond)
			}
			if a.Called("abort "+string(req.Session)) == 0 {
				t.Fatal("child left running")
			}
		})
	}
}

func TestContradictoryReviewResultsCannotPass(t *testing.T) {
	for _, raw := range []string{
		`{"status":"pass","summary":"good","findings":[{"file":"a","severity":"major","evidence":"broken"}]}`,
		`{"status":"issues","summary":"bad","findings":[]}`,
		`{"status":"pass","summary":"","findings":[]}`,
		`{"status":"unknown","summary":"unknown","findings":[]}`,
	} {
		if _, err := decodeResult(&operation{Phase: "review"}, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestInstructionsFollowOrdinaryDeliveryAndWorkflowPhases(t *testing.T) {
	h, p, a, sent := fixture(t)
	d := chat.Draft{Session: "main", Directory: t.TempDir(), Text: "Fix bug", Instructions: "Use only the selected project."}
	if err := p.Admit(d); err != nil {
		t.Fatal(err)
	}
	ordinary := next(t, h, sent)
	if ordinary.Instructions != d.Instructions {
		t.Fatal("ordinary delivery dropped draft instructions")
	}
	finish(h, a, ordinary, "")
	d.Workflow = kernel.HostPrefix + "test"
	if err := p.Admit(d); err != nil {
		t.Fatal(err)
	}
	plan := next(t, h, sent)
	if plan.Instructions != "Use only the selected project.\nplan" {
		t.Fatalf("plan lost instructions: %+v", plan)
	}
	finish(h, a, plan, `{"status":"ready","summary":"plan"}`)
	code := next(t, h, sent)
	if code.Instructions != "Use only the selected project.\nwork" {
		t.Fatal("code lost instructions")
	}
	finish(h, a, code, `{"status":"ready","summary":"created"}`)
	review := next(t, h, sent)
	if review.Policy == nil {
		t.Fatal("review child lost read-only policy")
	}
}

func TestTheCardOpensTheDeliverableAndItsSourceFolder(t *testing.T) {
	a := plugintest.NewAgent()
	sent := make(chan agent.Send, 30)
	a.OnSend = func(req agent.Send) { sent <- req }
	host := &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, a.New, host.New, (&plugintest.Chat{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	kernel.Contribute(h, wf.Definitions, kernel.HostPrefix+"test", 0, wf.Flow{ID: kernel.HostPrefix + "test", Title: "Test flow", Plan: "plan", Work: "work", Review: "review", Fix: "fix", Route: "route", Final: "final", MaxReviews: 3})
	dir := t.TempDir()
	a.Changed["main"] = []agent.FileChange{{Path: "report.md", Status: agent.FileAdded, Added: 8}}
	if err := p.Admit(chat.Draft{Session: "main", Directory: dir, Workflow: kernel.HostPrefix + "test", Text: "write", Anchor: 1}); err != nil {
		t.Fatal(err)
	}
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"plan"}`)
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"work"}`)
	finish(h, a, next(t, h, sent), `{"status":"pass","summary":"reviewed","findings":[]}`)
	finish(h, a, next(t, h, sent), `{"status":"ready","summary":"finished"}`)
	task := p.lanes["main"].task()
	if len(task.Changes) != 1 || task.Changes[0].Path != "report.md" {
		t.Fatalf("task changes %+v", task.Changes)
	}
	items := p.activities("main")
	if len(items) != 1 {
		t.Fatalf("activities %+v", items)
	}
	tt := ui.NewTester(func(c *ui.Context) { items[0].Render(c) }, 700, 500)
	tt.Frame()
	for _, want := range []string{"Deliverable", "report.md", "Open", "Sources"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Open"); err != nil {
		t.Fatalf("open: %v in %q", err, tt.Texts())
	}
	wantFile := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dir, "report.md"))}).String()
	if !slices.Contains(host.Opened, wantFile) {
		t.Fatalf("opened %q, want %q", host.Opened, wantFile)
	}
	if err := tt.Click("Reveal"); err != nil {
		t.Fatalf("reveal: %v in %q", err, tt.Texts())
	}
	wantDir := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}).String()
	if !slices.Contains(host.Opened, wantDir) {
		t.Fatalf("revealed %q, want %q", host.Opened, wantDir)
	}
}

func TestAPathOutsideTheFolderIsNotOpened(t *testing.T) {
	a := plugintest.NewAgent()
	host := &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, a.New, host.New, (&plugintest.Chat{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	kernel.Contribute(h, wf.Definitions, kernel.HostPrefix+"test", 0, wf.Flow{ID: kernel.HostPrefix + "test"})
	p.openPath(t.TempDir(), "../outside.md")
	if len(host.Opened) != 0 || len(host.Toasts) != 1 {
		t.Fatalf("opened %q, toasts %q", host.Opened, host.Toasts)
	}
}
