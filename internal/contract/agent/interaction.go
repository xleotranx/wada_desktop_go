package agent

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

var InteractionsKey = kernel.NewKey[Interactions]("agent.interactions")

// Interactions is optional. General questions are separate from permission
// approvals: answers preserve the backend's question semantics rather than
// imposing Allow/Always decisions on arbitrary choices or text.
type Interactions interface {
	// Answer is safe off the UI thread, blocks on I/O, and never retries.
	Answer(context.Context, InteractionAnswer) error
}

// PendingInteractions restores unanswered requests when opening a conversation.
type PendingInteractions interface {
	Pending(context.Context, SessionID) ([]Interaction, error)
}

type InteractionID string

type InteractionKind uint8

const (
	Confirm InteractionKind = iota
	Select
	Input
	Editor
)

type QuestionOption struct {
	Value       string
	Label       string
	Description string
}

// Question is one field in a grouped request. Values use backend option values.
type Question struct {
	Key         string
	Title       string
	Description string
	Options     []QuestionOption
	Multiple    bool
	Custom      bool
}

type Interaction struct {
	Session     SessionID // actual owner when presented inside another conversation
	Questions   []Question
	Settled     bool // removes an existing request answered or cancelled elsewhere
	ID          InteractionID
	Kind        InteractionKind
	Title       string
	Message     string
	Options     []string // Select: the exact labels accepted by the backend
	Placeholder string   // Input
	Prefill     string   // Editor
}

type InteractionAnswer struct {
	Answers   map[string][]string // grouped questions; single choice uses one value
	Session   SessionID
	ID        InteractionID
	Cancelled bool   // explicit dismissal; an empty Value is still a submitted answer
	Confirmed bool   // Confirm
	Value     string // Select, Input, Editor
}
