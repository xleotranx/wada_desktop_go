// Package jobs declares the app's jobs: a goal and a folder that a team of
// agents plans and works on, and what came out of it. The jobs plugin runs
// them; other plugins read the list and the results.
package jobs

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel"
)

// State is how far a job got.
type State uint8

const (
	Draft      State = iota // made, not started
	Reading                 // reading the folder's documents
	Planning                // the agent is planning the work
	PlanReview              // the plan waits for the user
	Running                 // the agent is working
	Reviewing               // a reviewer is judging the deliverable
	Done                    // finished with a result
	Failed                  // the agent reported a failure
	Cancelled               // the user stopped it
)

// String names the state for the user.
func (s State) String() string {
	switch s {
	case Draft:
		return "Waiting"
	case Reading:
		return "Reading documents"
	case Planning:
		return "Planning"
	case PlanReview:
		return "Plan ready"
	case Running:
		return "Working"
	case Reviewing:
		return "Reviewing"
	case Done:
		return "Done"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Stopped"
	}
	return "Unknown"
}

// Working reports that a turn is in flight.
func (s State) Working() bool {
	return s == Reading || s == Planning || s == Running || s == Reviewing
}

// Active reports that the job has not settled: it is working or waits for
// the user to approve its plan.
func (s State) Active() bool { return s.Working() || s == PlanReview }

// Plan is what the agent intends to do, shown before it starts.
type Plan struct {
	Steps       []string // in order; each says what to do and where
	Deliverable string   // the file it will write, such as brief.md
	Criteria    []string // how the result will be judged
}

// Finding is one defect a reviewer reported in the deliverable.
type Finding struct {
	Where string // the file, line or section
	What  string // what is wrong there
}

// Deliverable is a file a job produced.
type Deliverable struct {
	Path  string // absolute
	Bytes int
}

// Job is one piece of work.
type Job struct {
	ID      string
	Goal    string
	Folder  string
	State   State
	Session agent.SessionID // the participant, once it started
	Sources int             // documents read for it
	Chars   int             // their Markdown's length
	Plan    *Plan           // set while the plan waits and after
	Result  string          // the final answer, when it finished
	Problem string          // why it failed
	Files   []Deliverable
	// Parent is the conversation the participant was spawned under, so a
	// reviewer can be spawned under it too.
	Parent agent.SessionID
	// Reviewer is the child judging the deliverable, while Reviewing.
	Reviewer agent.SessionID
	// Reviews counts finished reviews; a job allows two, the second after a
	// fix.
	Reviews int
	// Summary is the reviewer's verdict in its own words, and Findings what
	// it found wrong.
	Summary  string
	Findings []Finding
	// Created and Updated are Unix milliseconds.
	Created, Updated int64
}

// Service starts and reports jobs. Call it on the UI thread.
type Service interface {
	// Jobs lists jobs newest first.
	Jobs() []Job
	// Start begins a job for goal in folder and returns it: it reads the
	// folder and plans, then waits for Run.
	Start(goal, folder string) (Job, error)
	// Run approves the plan of a job that waits in PlanReview and starts the
	// work.
	Run(id string) error
	// Cancel stops a job, whether it is working or waiting for its plan.
	Cancel(id string) error
	// Remove forgets a settled job and deletes the source pack it left in the
	// folder. A job that is still active must be stopped first.
	Remove(id string) error
}

// PageID is the Jobs page in shell.Pages.
const PageID = "jobs.page"

var (
	ServiceKey = kernel.NewKey[Service]("jobs.service")
	// Changed is emitted on the UI thread when a job is added or changes.
	Changed = kernel.NewTopic[Job]("jobs.changed")
)
