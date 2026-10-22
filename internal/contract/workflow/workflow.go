// Package workflow declares what the workflow plugin lets other plugins use:
// the service that runs flows, and the types a flow is written with.
//
// A flow is Go code. It runs on a worker goroutine and drives the agent
// backend through a Runner: each Step sends one prompt to a conversation the
// user does not see, waits for the agent to finish its turn and hands back its
// final answer. The flow decides what to ask next from those answers, so
// branching and loops are ordinary Go. The text the flow returns is the run's
// result, which the workflow page shows to the user.
//
// Flows are written against the agent contract only, so they run on every
// backend.
package workflow

import (
	"context"
	"errors"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
)

var ServiceKey = kernel.NewKey[Service]("workflow.service")

// ErrStopped is what Step returns once the user has stopped the run. A flow
// returns it, or anything wrapping it, unchanged.
var ErrStopped = errors.New("the run was stopped")

// Service runs flows. Call it on the UI thread.
type Service interface {
	// Prompt opens the dialog that asks for the flow's brief and folder, and
	// starts a run from the answer.
	Prompt(f Flow)
	// Start runs the flow at once and shows the run.
	Start(f Flow, in Input)
}

// Flow is one workflow.
type Flow struct {
	ID      string
	Title   string // "Plan, code, review"
	Summary string // one line, shown where the brief is asked for
	// Run drives the flow on a worker goroutine. ctx ends when the user stops
	// the run. The returned text, markdown, is the run's result; an error
	// other than ErrStopped fails the run and is shown to the user.
	Run func(ctx context.Context, r Runner) (string, error)
}

// Input is what the user asked a flow to do.
type Input struct {
	Brief     string // what to do, in the user's words
	Directory string // where the agent works; empty for the backend's default
}

// Runner is what a flow does its work with. Its methods block and are called
// from the flow's goroutine only.
type Runner interface {
	Input() Input
	// Step sends the prompt to a conversation, waits for the agent's turn to
	// end and returns its final answer. While it waits, the user sees the step
	// on the run's page and answers the agent's permission requests there.
	//
	// A step that fails does not end here: the user chooses between trying it
	// again and stopping the run, because what the failed attempt did on the
	// backend is unknown and the app never retries a mutation on its own. Step
	// returns an error only when the run is stopped, and then it is ErrStopped.
	Step(ctx context.Context, s Step) (Answer, error)
}

// Step is one prompt of a flow.
type Step struct {
	Name   string // shown on the run's page: "Plan", "Review 2"
	Prompt string
	// Mode is the agent mode a new conversation runs under; empty for the
	// backend's default. A flow names a mode only when it knows the backend.
	Mode string
	// Session continues that conversation, as an earlier step's Answer names
	// it, so the agent keeps what it has seen. Empty opens a new one.
	Session agent.SessionID
	Policy  Policy
}

// Policy says how a step answers the agent's permission requests without
// asking. A request of a kind in neither list goes to the user.
type Policy struct {
	Allow []agent.ToolKind // allowed once, each time
	Deny  []agent.ToolKind // rejected; Deny wins over Allow
}

// Answer is what a step produced.
type Answer struct {
	Session agent.SessionID // the conversation the step ran in
	Text    string          // the agent's final words, markdown
}
