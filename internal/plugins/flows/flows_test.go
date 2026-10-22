package flows

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
)

// runner is a scripted wf.Runner: it records each step and answers it with
// what reply says. Every conversation it opens gets its own ID.
type runner struct {
	in    wf.Input
	steps []wf.Step
	reply func(s wf.Step) string
	fail  string // the name of a step that ends the run
}

func (r *runner) Input() wf.Input { return r.in }

func (r *runner) Step(_ context.Context, s wf.Step) (wf.Answer, error) {
	r.steps = append(r.steps, s)
	if s.Name == r.fail {
		return wf.Answer{}, wf.ErrStopped
	}
	session := s.Session
	if session == "" {
		session = agent.SessionID(fmt.Sprintf("s%d", len(r.steps)))
	}
	return wf.Answer{Session: session, Text: r.reply(s)}, nil
}

func (r *runner) names() []string {
	var names []string
	for _, s := range r.steps {
		names = append(names, s.Name)
	}
	return names
}

// reviews answers the nth review with the given verdicts, in order, and every
// other step with a line naming it.
func reviews(verdicts ...string) func(wf.Step) string {
	n := 0
	return func(s wf.Step) string {
		if strings.HasPrefix(s.Name, "Review") {
			v := verdicts[min(n, len(verdicts)-1)]
			n++
			return "Findings of " + s.Name + ".\n\n" + v
		}
		return "Reply of " + s.Name + "."
	}
}

func run(t *testing.T, r *runner) (string, error) {
	t.Helper()
	r.in = wf.Input{Brief: "add a --verbose flag", Directory: "/w/app"}
	return planCodeReview.Run(context.Background(), r)
}

func TestPlanThenCodeThenReview(t *testing.T) {
	r := &runner{reply: reviews("VERDICT: PASS")}
	result, err := run(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.names(), []string{"Plan", "Code", "Review"}) {
		t.Fatalf("steps %q", r.names())
	}
	plan, code, review := r.steps[0], r.steps[1], r.steps[2]
	for _, s := range r.steps {
		if s.Session != "" {
			t.Fatalf("%s continues a conversation", s.Name)
		}
		if !strings.Contains(s.Prompt, "add a --verbose flag") {
			t.Fatalf("%s's prompt does not carry the brief: %q", s.Name, s.Prompt)
		}
	}
	if !strings.Contains(code.Prompt, "Reply of Plan.") {
		t.Fatalf("the coder was not given the plan: %q", code.Prompt)
	}
	if !strings.Contains(review.Prompt, "Reply of Plan.") || !strings.Contains(review.Prompt, "Reply of Code.") {
		t.Fatalf("the reviewer was not given the plan and the coder's report: %q", review.Prompt)
	}
	if !strings.Contains(plan.Prompt, "Do not change any files") || !strings.Contains(review.Prompt, "Do not change any files") {
		t.Fatal("the planner and the reviewer may change files")
	}
	for _, want := range []string{"**Review passed**", "Reply of Code.", "### Review", "Findings of Review."} {
		if !strings.Contains(result, want) {
			t.Fatalf("%q is missing from the result:\n%s", want, result)
		}
	}
	if strings.Contains(result, "VERDICT") {
		t.Fatalf("the verdict line is in the result:\n%s", result)
	}
}

func TestReviewFindingsGoBackToTheCoderInItsConversation(t *testing.T) {
	r := &runner{reply: reviews("VERDICT: FAIL", "VERDICT: PASS")}
	result, err := run(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.names(), []string{"Plan", "Code", "Review", "Fix 1", "Review 2"}) {
		t.Fatalf("steps %q", r.names())
	}
	fix, second := r.steps[3], r.steps[4]
	if fix.Session != "s2" {
		t.Fatalf("the fix goes to %q, not the coder's conversation", fix.Session)
	}
	if !strings.Contains(fix.Prompt, "Findings of Review.") || strings.Contains(fix.Prompt, "VERDICT") {
		t.Fatalf("the fix was not given the findings, or was given the verdict: %q", fix.Prompt)
	}
	if second.Session != "" {
		t.Fatalf("the second review continues the first: %q", second.Session)
	}
	if !strings.Contains(second.Prompt, "Reply of Fix 1.") || strings.Contains(second.Prompt, "Reply of Code.") {
		t.Fatalf("the second review was not given the latest report: %q", second.Prompt)
	}
	for _, want := range []string{"**Review passed after 2 rounds**", "Reply of Fix 1.", "Findings of Review 2."} {
		if !strings.Contains(result, want) {
			t.Fatalf("%q is missing from the result:\n%s", want, result)
		}
	}
}

func TestTheReviewLoopIsBounded(t *testing.T) {
	r := &runner{reply: reviews("VERDICT: FAIL")}
	result, err := run(t, r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Plan", "Code", "Review", "Fix 1", "Review 2", "Fix 2", "Review 3"}
	if !slices.Equal(r.names(), want) {
		t.Fatalf("steps %q, want %q", r.names(), want)
	}
	if !strings.Contains(result, "**Review still found problems after 3 rounds**") || !strings.Contains(result, "Findings of Review 3.") {
		t.Fatalf("result:\n%s", result)
	}
}

func TestAReviewWithoutAVerdictEndsTheRun(t *testing.T) {
	r := &runner{reply: reviews("Looks fine to me.")}
	result, err := run(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.steps) != 3 {
		t.Fatalf("steps %q", r.names())
	}
	if !strings.Contains(result, "**The reviewer gave no verdict**") || !strings.Contains(result, "Looks fine to me.") {
		t.Fatalf("result:\n%s", result)
	}
}

func TestAStoppedStepEndsTheFlowThere(t *testing.T) {
	for _, step := range []string{"Plan", "Code", "Review", "Fix 1"} {
		r := &runner{reply: reviews("VERDICT: FAIL"), fail: step}
		if _, err := run(t, r); !errors.Is(err, wf.ErrStopped) {
			t.Fatalf("%s: error %v", step, err)
		}
		if r.steps[len(r.steps)-1].Name != step {
			t.Fatalf("%s: the flow went on: %q", step, r.names())
		}
	}
}

func TestPoliciesKeepPlanningAndReviewReadOnly(t *testing.T) {
	r := &runner{reply: reviews("VERDICT: PASS")}
	if _, err := run(t, r); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.steps {
		if !slices.Contains(s.Policy.Allow, agent.ToolRead) || !slices.Contains(s.Policy.Allow, agent.ToolSearch) {
			t.Fatalf("%s must read and search without asking: %+v", s.Name, s.Policy)
		}
		if slices.Contains(s.Policy.Allow, agent.ToolShell) || slices.Contains(s.Policy.Allow, agent.ToolEdit) {
			t.Fatalf("%s may edit or run commands without asking: %+v", s.Name, s.Policy)
		}
	}
	for _, i := range []int{0, 2} {
		if !slices.Contains(r.steps[i].Policy.Deny, agent.ToolEdit) {
			t.Fatalf("%s may edit files", r.steps[i].Name)
		}
	}
	if len(r.steps[1].Policy.Deny) != 0 {
		t.Fatalf("the coder is denied something: %+v", r.steps[1].Policy)
	}
}

func TestJudgeTakesTheLastVerdictLine(t *testing.T) {
	for _, c := range []struct {
		review   string
		verdict  verdict
		findings string
	}{
		{"All good.\nVERDICT: PASS", passed, "All good."},
		{"Bad.\n\n**VERDICT: FAIL**\n", failed, "Bad."},
		{"verdict : pass", passed, ""},
		{"VERDICT: FAIL\nMore thoughts.\nVERDICT: PASS", passed, "VERDICT: FAIL\nMore thoughts."},
		{"No verdict here, but the word verdict appears.", undecided, "No verdict here, but the word verdict appears."},
		{"VERDICT: PASSING", undecided, "VERDICT: PASSING"},
		{"", undecided, ""},
	} {
		v, findings := judge(c.review)
		if v != c.verdict || findings != c.findings {
			t.Errorf("judge(%q) = %d, %q; want %d, %q", c.review, v, findings, c.verdict, c.findings)
		}
	}
}

// ---- The command

type fakeService struct {
	prompted []wf.Flow
	started  []wf.Input
}

func (*fakeService) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-workflow", Provides: []kernel.Named{wf.ServiceKey}}
}

func (f *fakeService) Setup(c *kernel.Context) error {
	return kernel.Provide[wf.Service](c, wf.ServiceKey, f)
}

func (f *fakeService) Prompt(flow wf.Flow) { f.prompted = append(f.prompted, flow) }

func (f *fakeService) Start(_ wf.Flow, in wf.Input) { f.started = append(f.started, in) }

func TestTheCommandAsksForTheBriefOfItsFlow(t *testing.T) {
	svc := &fakeService{}
	h := kerneltest.New(t, svc, &Plugin{})
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID != "flows.plan-code-review" {
			continue
		}
		if e.Value.Title != "Plan, code, review…" || e.Value.Menu != "Agent" {
			t.Fatalf("command %+v", e.Value)
		}
		kernel.Invoke(h, e, func(c kernel.Command) { c.Run() })
		if len(svc.prompted) != 1 || svc.prompted[0].ID != "plan-code-review" || svc.prompted[0].Run == nil {
			t.Fatalf("prompted %+v", svc.prompted)
		}
		return
	}
	t.Fatal("the flow has no command")
}

func TestTheFlowTakesTheFirstMessageOfANewChat(t *testing.T) {
	svc := &fakeService{}
	h := kerneltest.New(t, svc, &Plugin{})
	targets := kernel.Items(h, chat.Targets)
	if len(targets) != 1 {
		t.Fatalf("targets %+v", targets)
	}
	target := targets[0].Value
	if target.Group != "Workflows" || target.Title != "Plan, code, review" || target.Summary == "" {
		t.Fatalf("target %+v", target)
	}
	target.Take(chat.Draft{Directory: "/w/app", Text: "add a flag"})
	if len(svc.started) != 1 || svc.started[0] != (wf.Input{Brief: "add a flag", Directory: "/w/app"}) {
		t.Fatalf("started %+v", svc.started)
	}
}
