package flows

import wf "wada-desktop-go/internal/contract/workflow"

var planWorkReview = wf.Flow{
	ID: "flows.plan-work-review", Title: "Plan, work, review", Summary: "Plan the task, create the deliverable, and get an independent accuracy and completeness review.", MaxReviews: 3,
	Plan:   "Understand the requested outcome using this conversation and the relevant work documents. Propose a short, concrete plan: the deliverable, sources needed, steps and acceptance criteria. Do not edit files. Ask only for a decision that materially blocks the work. Otherwise submit status ready.",
	Work:   "Carry out the approved plan using the current user instructions and relevant source documents. Create or revise the requested deliverable. Check facts, calculations, citations, completeness and readability. Submit ready with the deliverable path, a self-contained summary and verification evidence for an independent reviewer; submit blocked only when necessary input is unavailable.",
	Review: "You are an independent reviewer with fresh context. Read the actual deliverable and relevant source documents. Verify it against the user's request and acceptance criteria. Check factual accuracy, calculations, source attribution, missing requirements and clarity. Treat the author's report as claims to verify. Do not edit files. Report actionable defects with the file or section, severity and supporting evidence. Submit pass only after the review finds no required corrections; otherwise submit issues or incomplete. Return structured findings.",
	Fix:    "Assess the review findings against the deliverable and sources. Correct substantiated errors and omissions, preserve what already meets the request, and verify the revised result. Explain any rejected finding with evidence. Submit ready with the updated deliverable path, checks and a disposition for each finding, or blocked when a user decision is necessary.",
	Route:  "Handle the latest message in the context of the reviewed task. Do not edit files. Answer an informational question and submit action answer. For a related revision submit action continue with a self-contained description. For a new task submit new_task. If the user explicitly asks to resume the paused task, submit resume. Do not start work or another review during routing.",
	Final:  "Give a concise outcome report: the deliverable and its path, the checks and review result, and any concrete unresolved limitation. Do not change files or start another workflow. Submit ready.",
}
