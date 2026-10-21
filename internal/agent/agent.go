// Package agent defines the application's backend contract. It contains no wire
// formats, native UI state, or concrete backend implementation.
package agent

import "context"

type Session struct {
	ID, Title, Directory string
	Updated              int64
	Model                ModelRef
	Cost                 float64
}
type ModelRef struct{ ProviderID, ID, Variant string }
type Model struct {
	ProviderID, ID, Name string
	Variants             []string
	Free                 bool
}
type Persona struct{ ID, Name, Description string }
type Facts struct {
	Version string
	URLs    []string
	PID     int
	PTY     bool
}
type TokenSpend struct{ Output, Reasoning int }
type Message struct {
	ID, Type, Text string
	Cost           float64
	Tokens         TokenSpend
	Model          ModelRef
	Content        []Part
}
type Part struct {
	Type, Text, ID, Name, Command string
	Output                        []ToolOutput
	Exit                          int
	Truncated                     bool
}
type ToolOutput struct{ Type, Text string }

// Client is the chat capability. Workspace discovery and persona catalogs are
// optional; a backend implementing only Client supports a complete chat session.
type Client interface {
	Stop()
	Info() Facts
	Endpoint() string
	Updates(context.Context, func(Update), func(string)) error
	ListSessions(context.Context, string, string, string) ([]Session, string, error)
	ListModels(context.Context) ([]Model, error)
	DefaultModel(context.Context) (ModelRef, error)
	CreateSession(context.Context, string, string) (string, ModelRef, error)
	SetModel(context.Context, string, string, string, string) error
	Prompt(context.Context, string, string) error
	SessionMessages(context.Context, string) ([]Message, error)
	Interrupt(context.Context, string) error
	ReplyPermission(context.Context, string, string, string) error
	RenameSession(context.Context, string, string) error
	DeleteSession(context.Context, string) error
	SessionMessage(context.Context, string, string) (Message, error)
}
type Workspaces interface {
	ListProjects(context.Context) ([]string, error)
}
type Personas interface {
	ListAgents(context.Context) ([]Persona, error)
}
