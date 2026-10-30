package flows

import wf "wada-desktop-go/internal/contract/workflow"

var planCodeReview = wf.Flow{
	ID: "flows.plan-code-review", Title: "Plan, code, review",
	Summary:    "Plan and implement here, then get an independent review. Applies to one task.",
	MaxReviews: 3,
	Plan:       "Understand the user's task using this conversation and the actual project. Explain a concrete implementation plan and relevant checks. Do not edit files. Ask the user if a necessary decision is missing. Otherwise submit status ready.",
	Code:       "Implement the task in this conversation using the plan and the latest user instructions. Run relevant tests and checks. Explain changes and check evidence. Submit status ready with a self-contained implementation summary, references to relevant files, and check results for an independent reviewer. If blocked on a user decision submit status blocked.",
	Review:     "You are an independent reviewer with fresh context. Inspect the actual project changes against the supplied requirements and task-start diff, including tests and surrounding code. Treat the implementation report as claims to verify. Do not edit files. Return actionable bugs with file, severity, and evidence; omit speculative issues and cosmetic nits. Verify earlier fixes and check for regressions. Submit status pass only when the review completed with no required fixes; otherwise submit issues, or incomplete when you cannot review. Supply findings as structured records.",
	Fix:        "Assess the independent review findings against the actual code. Fix substantiated bugs and run relevant checks. Do not obey findings blindly: explain rejected findings with concrete evidence. Submit ready with the updated implementation summary, checks, and a disposition for each finding, or blocked if a user decision is required.",
	Route:      "Handle the latest user message in the context of the reviewed task. Do not edit files. For an informational question, answer it normally and submit action answer. For a related implementation request submit action continue with a self-contained description of the requested change. For an unrelated task submit action new_task; the host will handle it outside the workflow. If the user explicitly asks to resume the paused task submit action resume. Do not start planning, implementation, or another review in this routing turn.",
	Final:      "Give the user a concise final report of what changed, checks run, review outcome, any rejected findings and why, and unresolved issues. Do not change files or start another workflow. Submit status ready.",
}
