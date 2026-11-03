# Jobs

`internal/contract/jobs` declares a job: a goal, a folder, the plan it waits on,
the participant that works on it, the reviewer that judges the result and what
came out. `internal/plugins/jobs` runs them and keeps the list in `jobs.db`.

## Lifecycle

A job goes `Reading` → `Planning` → `PlanReview` → `Running` → `Reviewing` →
`Done`, with one fix round between reviews, or `Failed` / `Cancelled`. `Start`
refuses a job without a goal, a folder or an open conversation to be its parent,
and refuses a second job while one is active, which includes a job waiting for
its plan.

`launch` starts a worker through `kernel.Go` that:

1. ingests the folder through `documents.Reader`, so the documents are Markdown
   before the participant exists;
2. writes each converted document to
   `<folder>/.wada/jobs/<id>/sources/<name>.md`;
3. spawns one child through `agent.Children` — named `reader`, working in the
   folder — and returns it with the planning prompt.

The result is applied on the UI thread, which records the participant and only
then dispatches the planning turn. Recording before sending is what lets
`events` recognise the answer: it matches terminal events by session, for the
participant and the reviewer alike.

The planning turn carries `planSchema` through `Send.ResultSchema`, so the
backend validates it before the turn can end. `planned` keeps the plan and moves
to `PlanReview`; an empty or unreadable result fails the job rather than
guessing. `Run` (the plan card's Run button) sends the work prompt, which repeats
the steps, the deliverable and the criteria, and moves to `Running`.

## The review

A successful work turn starts `workDone`, which stats the file the plan named
before spending a review turn: a missing or empty deliverable fails the job,
without a reviewer.

Otherwise it spawns a second child — named `reviewer`, under the same parent,
with `reviewPolicy` (read and search only; it judges, it never edits) — and
dispatches `reviewPrompt` with `reviewSchema`. The reviewer reads the
deliverable against the plan's criteria and returns `pass` or `revise` with the
defects, each located and described.

- `pass` finishes the job.
- `revise` sends `fixPrompt` to the participant, which still holds the work in
  its context, and returns to `Running` for one fix round.
- A second `revise` is accepted: the job is `Done` with its `Findings` kept, so
  the panel says what is still wrong instead of pretending it is clean.

`Reviews` counts finished reviews, at most two (`maxReviews`).

## Permissions

`jobPolicy` allows reading, searching and editing in the folder and denies
shell, web and everything else, so a job never waits on an approval the user did
not see. `reviewPolicy` denies editing too. Both are fixed at spawn.

## Restart

`restore` replaces the list with what `jobs.db` held — one row per job, the
record as JSON — and marks any job that was still working as failed, "The app
quit while this was running". A job waiting in `PlanReview` keeps waiting.

## The Jobs page

`jobs.page` lists every job: the newest on the left, and the chosen one on the
right with its plan and buttons, the reviewer's summary, the result, the
findings and the files it left, **Stop** while it is working and **Remove** once
it settled. It opens from
the briefcase in the title strip, from `Show Jobs` (⌘J), and from **All jobs**
under the new chat's block.

A job can be started from the page itself: the goal goes in the input, the
folder comes from `workspace.ChooseFolder` (the last job's folder, or the open
chat's, is offered first), and **Start job** begins it without a chat. With no
job at all, the page says how to start one and offers the way back to a chat.

A job in `PlanReview` can be changed there before it runs: **Edit plan** opens
the steps, the deliverable and the criteria as fields, **Add step** appends one,
and **Save plan** keeps it. An edit with no step left is refused, so the worker
always has something to follow; the prompt it then receives is the edited plan.
Editing lives only on the page, where there is room for fields.

## Removing

`Remove` forgets a settled job and deletes `<folder>/.wada/jobs/<id>`, the source
pack and whatever the plan read. What the job wrote in the folder stays: only
derived data goes. An active job is refused until it is stopped, and the store
deletes its row through the same queue that writes jobs, so a remove and an
update cannot overtake each other.

## Notices

`notify` sends a desktop notice when a job reaches `PlanReview`, `Done` or
`Failed` — unless the user is looking at the Jobs page with the app in front,
where the card already says so. The notice ID is `job:<id>`; clicking it emits
`shell.NoticeClicked`, and `noticeClicked` opens the page and selects the job.
Nothing is announced for a job the user stopped themselves.

## The new chat

`chat.Home` lets the jobs plugin appear under the starting points of a chat
with nothing in it: the newest jobs, with the plan card and its buttons, or —
when a folder is open and no job exists — one line saying how to start one. A
job can be seen and approved before any conversation exists.

A new chat has no conversation for the children to hang under, so `prepare`
creates one through `agent.Create` and keeps it out of the app's own record: it
owns the job's children, it is not a chat the user asked for.

## Where it shows

`jobs.start` ("Start Job from Draft", Agent menu) takes the composer's draft as
the goal and the open conversation's folder. `jobs.cancel` stops the active job:
it is marked first, then aborted on a worker, because `Abort` blocks — the
reviewer's session while it reviews, the participant's otherwise; a job waiting
for its plan has no turn to abort. The Jobs section of the work panel lists the
three newest jobs; a job in `PlanReview` shows its steps, deliverable and
criteria with **Run plan** and **Discard**, and a finished job lists its
findings.

## What is not here yet

Each job allows one participant and one reviewer, spawned fresh per round; the
plan's criteria are judged by that reviewer, not by deterministic checks. There
are no scheduled or repeated jobs, and no way yet to change the owner
conversation a job's children hang under.
