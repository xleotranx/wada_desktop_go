// Package agent is the backend contract: what a chat backend plugin provides
// and what consumers may rely on. It holds declarations only.
//
// A backend plugin provides ServiceKey, and optionally ModelsKey, ModesKey,
// FileTypesKey and FilesKey.
// It owns its connection: it connects, reconnects, and publishes Status and
// event batches on the UI thread through the kernel. Consumers call Service
// methods off the UI thread (from kernel.Go work) and listen to Events.
package agent

import (
	"context"
	"errors"

	"wada-desktop-go/internal/kernel"
)

var (
	ServiceKey = kernel.NewKey[Service]("agent.service")
	ModelsKey  = kernel.NewKey[Models]("agent.models") // optional
	ModesKey   = kernel.NewKey[Modes]("agent.modes")   // optional
	// FileTypesKey is provided by a backend that takes only some kinds of
	// files. Without it, a backend takes any file.
	FileTypesKey = kernel.NewKey[FileTypes]("agent.file-types") // optional
	// FilesKey is provided by a backend that can find and read the files of a
	// conversation's folder where the backend runs. Without it a message
	// cannot mention a file.
	FilesKey = kernel.NewKey[Files]("agent.files") // optional

	// Events carries ordered batches of normalized events from the current
	// connection, emitted on the UI thread.
	Events = kernel.NewTopic[[]Event]("agent.events")
	// StatusChanged is emitted on the UI thread whenever Status changes.
	StatusChanged = kernel.NewTopic[Status]("agent.status")
)

var (
	// ErrNotConnected is returned by Service methods while there is no connection.
	ErrNotConnected = errors.New("the agent is not connected")
	// ErrNotSent is wrapped by a Send the backend refused before sending
	// anything, such as one with a file it can't take. Unlike other Send
	// errors, its outcome is known: the agent has nothing.
	ErrNotSent = errors.New("the message was not sent")
)

// Service is the chat capability every backend provides.
//
// Methods other than Status are safe to call from any goroutine and block on
// I/O. Backends never retry a mutation (Create, Send, Abort, Reply, Rename,
// Delete) on their own: when a mutation fails, its outcome on the server is
// unknown, and the caller decides what to do.
type Service interface {
	// Status reports the connection. Call it on the UI thread.
	Status() Status

	Sessions(ctx context.Context, q SessionQuery) (SessionPage, error)
	// Create opens a conversation. If it was created but a later step (such as
	// choosing its model) failed, Create returns the session with the error.
	Create(ctx context.Context, req CreateSession) (Session, error)
	Messages(ctx context.Context, session SessionID) ([]Message, error)
	// Send reports admission of one prompt, independently of run status in Events.
	// Its disposition is meaningful only when the error is nil.
	Send(ctx context.Context, req Send) (SendDisposition, error)
	Abort(ctx context.Context, session SessionID) error
	Reply(ctx context.Context, req Reply) error
	Rename(ctx context.Context, session SessionID, title string) error
	Delete(ctx context.Context, session SessionID) error
}

// Models is the optional model catalog capability.
type Models interface {
	Models(ctx context.Context) ([]Model, error)
	// DefaultModel is what the backend runs when no model is chosen.
	DefaultModel(ctx context.Context) (ModelRef, error)
	SetModel(ctx context.Context, session SessionID, model ModelRef) error
}

// Modes is the optional capability of running a conversation under a named
// agent mode (OpenCode's "build" and "plan").
type Modes interface {
	Modes(ctx context.Context) ([]Mode, error)
}

// FileTypes says which files a backend takes.
type FileTypes interface {
	// Accepts reports whether a file of this MIME type can be sent.
	Accepts(mime string) bool
	// Summary names what it takes, for the user: "images and text files".
	Summary() string
}

// Files is the optional capability of finding and reading the files of a
// conversation's folder. The backend may run on another machine than the app,
// so these are the backend's files, not the app's. Like the rest of Service,
// the methods block on I/O and are called off the UI thread.
type Files interface {
	// Find returns the paths of files in directory (the backend's default
	// folder when empty) that match query, best first, at most limit of them.
	// Paths are slash-separated and relative to directory. An empty query
	// returns some files in no particular order.
	Find(ctx context.Context, directory, query string, limit int) ([]string, error)
	// Read returns the contents of the file at path, relative to directory.
	Read(ctx context.Context, directory, path string) ([]byte, error)
}

// ---- Connection

type ConnState uint8

const (
	Stopped    ConnState = iota // not trying to connect
	Connecting                  // an attempt is starting
	Ready                       // connected; events flow
	Down                        // the attempt failed or dropped; a retry is scheduled
)

type Status struct {
	State ConnState
	// Attempt increases with every connection attempt. Consumers holding
	// server state reload it when a new attempt becomes Ready, since events
	// may have been missed in between.
	Attempt  uint64
	Endpoint string // where the backend listens, for display
	Version  string // the backend's own version, when known
	Err      error  // why the attempt is Down
}

// ---- Identity

type (
	SessionID  string
	MessageID  string
	ApprovalID string
)

type ModelRef struct{ Provider, ID, Variant string }

func (m ModelRef) IsZero() bool { return m.ID == "" }

type Model struct {
	Ref      ModelRef // Variant empty
	Name     string
	Variants []string
	Free     bool
}

type Mode struct{ ID, Name, Description string }

// ---- Sessions and messages

type Session struct {
	ID        SessionID
	Title     string
	Directory string // where the conversation works; empty for the backend's default
	Updated   int64  // Unix milliseconds
	Model     ModelRef
	Cost      float64
}

type SessionQuery struct {
	Cursor    string // empty for the newest page
	Search    string // matches titles
	Directory string // only conversations in this directory
}

type SessionPage struct {
	Sessions []Session // newest first
	Next     string    // cursor of the next page; empty at the end
}

type CreateSession struct {
	Directory string
	Model     ModelRef // zero for the backend's default
	Mode      string   // empty for the backend's default
}

type Send struct {
	Session SessionID
	Text    string
	Files   []File
}

type SendDisposition uint8

const (
	SendAccepted SendDisposition = iota // admitted; execution is observed through Events
	SendQueued                          // admitted to a running agent's queue
	SendHandled                         // consumed without requiring a new run
)

// File is a file sent with a user message. Data is set when sending; stored
// messages carry only Name and MIME.
type File struct {
	Name string // the file's base name
	MIME string // e.g. "image/png", "text/plain"
	Data []byte
}

type Decision uint8

const (
	Once Decision = iota
	Always
	Reject
)

type Reply struct {
	Session  SessionID
	Approval ApprovalID
	Decision Decision
}

type Role uint8

const (
	RoleOther Role = iota // a stored entry the transcript doesn't show
	RoleUser
	RoleAssistant
)

type Message struct {
	ID     MessageID
	Role   Role
	Text   string // the user's words
	Files  []File // the files sent with the user's words, without Data
	Parts  []Part // the assistant's answer
	Model  ModelRef
	Cost   float64
	Tokens Tokens
}

type Tokens struct{ Output, Reasoning int }

type PartKind uint8

const (
	PartOther PartKind = iota
	PartText
	PartReasoning
	PartTool
)

type Part struct {
	Kind PartKind
	Text string // PartText, PartReasoning
	Tool *Tool  // PartTool
}

// Tool is one tool call, complete in a stored message or partial in an event.
type Tool struct {
	ID        string
	Name      string
	Command   string   // the shell command, for shell-like tools
	Output    []string // text output, in the pieces the backend produced
	Status    string   // the backend's own word, e.g. "completed"
	Exit      int
	Truncated bool
}

// ---- Events

type EventKind uint8

const (
	TextStarted          EventKind = iota // Text: Message, Ordinal
	TextDelta                             // Text: Message, Ordinal, Delta
	TextEnded                             // Text: Message, Ordinal, Text (complete)
	ToolStarted                           // Tool: ID, Name
	ToolCalled                            // Tool: ID, Name, Command
	ToolFinished                          // Tool: complete
	ApprovalRequested                     // Approval
	InteractionRequested                  // Interaction
	TurnStarted
	TurnSucceeded
	TurnFailed // Error, when the backend says why
	TurnInterrupted
	ModelChanged   // Model
	SessionRenamed // Title
)

// Event is one normalized backend event. Only the payload of its kind is set.
// Events are immutable once emitted.
type Event struct {
	Kind        EventKind
	Session     SessionID
	Text        *TextEvent
	Tool        *Tool
	Approval    *Approval
	Interaction *Interaction
	Model       ModelRef
	Title       string
	Error       string // TurnFailed: the backend's reason, for the user
	Bytes       int    // wire size, for batching
}

type TextEvent struct {
	Message MessageID
	Ordinal int // which text block of the message
	Delta   string
	Text    string
}

// Approval is a permission the agent waits for before acting.
type Approval struct {
	ID        ApprovalID
	Action    string   // e.g. "shell"
	Resources []string // what it wants to act on
	Save      []string // the patterns Always would remember
}
