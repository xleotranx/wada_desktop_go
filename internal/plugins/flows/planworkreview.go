package flows

import wf "wada-desktop-go/internal/contract/workflow"

var planWorkReview = wf.Flow{
	ID: "flows.plan-work-review", Title: "Plan, code, review", Summary: "Plan the change, implement and verify it, then run an independent code review.", MaxReviews: 3,
	Plan:   "Inspect the relevant codebase files and understand the requested change. Propose a short, concrete engineering plan: affected files, implementation steps, edge cases, and verification commands. Do not edit files. Ask only for a decision that materially blocks the work. Otherwise submit status ready.",
	Work:   "Implement the approved plan in the workspace codebase. Read existing code and conventions before editing, keep changes minimal and clean, and run relevant tests, linters, or builds to verify behavior. Submit ready with changed file paths, a self-contained summary, and verification command output for an independent reviewer; submit blocked only when required input or tooling is unavailable.",
	Review: "You are an independent code reviewer with fresh context. Inspect the actual diff and changed files against the user's request and acceptance criteria. Verify correctness, edge cases, error handling, test coverage, and adherence to project conventions. Treat the author's report as claims to verify. Do not edit files. Report actionable defects with file path, severity, and concrete evidence. Submit pass only when no required corrections remain; otherwise submit issues or incomplete. Return structured findings.",
	Fix:    "Assess the review findings against the codebase and tests. Fix substantiated bugs and gaps while preserving working behavior, and re-run verification commands. Explain any rejected finding with evidence. Submit ready with updated paths, check output, and a disposition for each finding, or blocked when a user decision is necessary.",
	Route:  "Handle the latest message in the context of the reviewed coding task. Do not edit files. Answer an informational question and submit action answer. For a related follow-up change submit action continue with a self-contained description. For a new task submit new_task. If the user explicitly asks to resume the paused task, submit resume. Do not start work or another review during routing.",
	Final:  "Give a concise engineering summary: changed files, checks run and their results, review outcome, and any concrete unresolved limitation. Do not change files or start another workflow. Submit ready.",
}
