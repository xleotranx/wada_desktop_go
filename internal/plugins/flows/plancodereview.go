package flows

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	wf "wada-desktop-go/internal/contract/workflow"
)

// maxReviews bounds the review loop: a reviewer who is never satisfied ends the
// run with the last review instead of looping for ever.
const maxReviews = 3

var planCodeReview = wf.Flow{
	ID:      "plan-code-review",
	Title:   "Plan, code, review",
	Summary: "One agent plans the work, a second makes the changes, a third reviews them. Problems the review finds go back to the coder, up to three rounds.",
	Run:     runPlanCodeReview,
}

// What each step may do without asking. The agent asks for the rest, and the
// user answers on the run's page.
var (
	planning  = wf.Policy{Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch}, Deny: []agent.ToolKind{agent.ToolEdit}}
	coding    = wf.Policy{Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch}}
	reviewing = planning
)

func runPlanCodeReview(ctx context.Context, r wf.Runner) (string, error) {
	brief := r.Input().Brief
	plan, err := r.Step(ctx, wf.Step{Name: "Plan", Prompt: planPrompt(brief), Policy: planning})
	if err != nil {
		return "", err
	}
	code, err := r.Step(ctx, wf.Step{Name: "Code", Prompt: codePrompt(brief, plan.Text), Policy: coding})
	if err != nil {
		return "", err
	}
	for round := 1; ; round++ {
		name := "Review"
		if round > 1 {
			name = fmt.Sprintf("Review %d", round)
		}
		review, err := r.Step(ctx, wf.Step{Name: name, Prompt: reviewPrompt(brief, plan.Text, code.Text), Policy: reviewing})
		if err != nil {
			return "", err
		}
		verdict, findings := judge(review.Text)
		switch {
		case verdict == passed && round == 1:
			return report("Review passed", code.Text, findings), nil
		case verdict == passed:
			return report(fmt.Sprintf("Review passed after %d rounds", round), code.Text, findings), nil
		case verdict == undecided:
			return report("The reviewer gave no verdict", code.Text, findings), nil
		case round == maxReviews:
			return report(fmt.Sprintf("Review still found problems after %d rounds", round), code.Text, findings), nil
		}
		// The coder goes on in its own conversation, so it remembers what it did.
		code, err = r.Step(ctx, wf.Step{Name: fmt.Sprintf("Fix %d", round), Prompt: fixPrompt(findings), Session: code.Session, Policy: coding})
		if err != nil {
			return "", err
		}
	}
}

func planPrompt(brief string) string {
	return "You are the planning step of a workflow: plan, code, review. Another agent will make the changes from your plan.\n\n" +
		"Task:\n\n" + brief + "\n\n" +
		"Look through the project in this folder and write an implementation plan. Do not change any files.\n" +
		"End with a numbered list of the concrete steps to take, naming the files each one touches, and a short list of how to check the work."
}

func codePrompt(brief, plan string) string {
	return "You are the coding step of a workflow: plan, code, review. Carry out the task in this folder by following the plan.\n\n" +
		"Task:\n\n" + brief + "\n\n" +
		"Plan:\n\n" + plan + "\n\n" +
		"Make the changes and run the project's tests or checks when there are any. " +
		"Finish with a short summary of what you changed and anything you left undone."
}

func reviewPrompt(brief, plan, report string) string {
	return "You are the review step of a workflow: plan, code, review. Another agent worked on a task in this folder.\n\n" +
		"Task:\n\n" + brief + "\n\n" +
		"Plan it followed:\n\n" + plan + "\n\n" +
		"What it reported:\n\n" + report + "\n\n" +
		"Review its work. Look at the changes it made, for example with git status and git diff, and run the project's checks when there are any. Do not change any files.\n" +
		"List the problems you found, most serious first, each naming the file and what is wrong. If there are none, say so.\n" +
		"End with exactly one last line: \"VERDICT: PASS\" if the work does the task and nothing needs fixing, or \"VERDICT: FAIL\" if something must be fixed."
}

func fixPrompt(findings string) string {
	return "A reviewer looked at your work and found problems:\n\n" + findings + "\n\n" +
		"Fix them, run the project's checks again, and finish with a short summary of what you changed."
}

type verdict uint8

const (
	undecided verdict = iota // the reviewer gave none
	passed
	failed
)

var verdictLine = regexp.MustCompile(`(?i)^\W*verdict\W*:\W*(pass|fail)\b`)

// judge splits a review into its verdict and the findings before it. The last
// line that states a verdict counts, and is not part of the findings.
func judge(review string) (verdict, string) {
	lines := strings.Split(review, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := verdictLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		findings := strings.TrimSpace(strings.Join(append(lines[:i:i], lines[i+1:]...), "\n"))
		if strings.EqualFold(m[1], "pass") {
			return passed, findings
		}
		return failed, findings
	}
	return undecided, strings.TrimSpace(review)
}

// report is the run's result: how the review ended, what the coder says it
// did, and what the reviewer found.
func report(headline, code, findings string) string {
	var b strings.Builder
	b.WriteString("**" + headline + "**\n\n")
	b.WriteString(strings.TrimSpace(code))
	b.WriteString("\n\n---\n\n### Review\n\n")
	b.WriteString(findings)
	return b.String()
}
