package agent

import (
	"context"

	"wada-desktop-go/internal/kernel"
)

var InteractionsKey = kernel.NewKey[Interactions]("agent.interactions")

// Interactions is optional. General questions are separate from permission
// approvals: answers preserve the backend's question semantics rather than
// imposing Allow/Always decisions on arbitrary choices or text.
type Interactions interface {
	// Answer is safe off the UI thread, blocks on I/O, and never retries.
	Answer(context.Context, InteractionAnswer) error
}

type InteractionID string

type InteractionKind uint8

const (
	Confirm InteractionKind = iota
	Select
	Input
	Editor
)

type Interaction struct {
	ID          InteractionID
	Kind        InteractionKind
	Title       string
	Message     string
	Options     []string // Select: the exact labels accepted by the backend
	Placeholder string   // Input
	Prefill     string   // Editor
}

type InteractionAnswer struct {
	Session   SessionID
	ID        InteractionID
	Cancelled bool   // explicit dismissal; an empty Value is still a submitted answer
	Confirmed bool   // Confirm
	Value     string // Select, Input, Editor
}
