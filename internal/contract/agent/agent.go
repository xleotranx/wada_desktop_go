// Package agent is the backend contract: what a chat backend plugin provides
// and what consumers may rely on. It holds declarations only.
//
// A backend plugin provides ServiceKey, and optionally ModelsKey, ModesKey,
// FileTypesKey, ImagesKey, FilesKey, SkillsKey, RewindKey, ChangesKey and
// ChildrenKey and SnapshotsKey.
// It owns its connection: it connects, reconnects, and publishes Status and
// event batches on the UI thread through the kernel. Consumers call Service
// methods off the UI thread (from kernel.Go work) and listen to Events.
package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	ServiceKey   = kernel.NewKey[Service]("agent.service")
	SnapshotsKey = kernel.NewKey[Snapshots]("agent.snapshots")
	Deleted      = kernel.NewTopic[SessionID]("agent.deleted")
	ModelsKey    = kernel.NewKey[Models]("agent.models") // optional
	ModesKey     = kernel.NewKey[Modes]("agent.modes")   // optional
	// FileTypesKey is provided by a backend that takes only some kinds of
	// files. Without it, a backend takes any file.
	FileTypesKey = kernel.NewKey[FileTypes]("agent.file-types") // optional
	// ImagesKey is provided by a backend that can say how large an image
	// reaches a model. Without it the size a model sees is unknown.
	ImagesKey = kernel.NewKey[Images]("agent.images") // optional
	// FilesKey is provided by a backend that can find and read the files of a
	// conversation's folder where the backend runs. Without it a message
	// cannot mention a file.
	FilesKey = kernel.NewKey[Files]("agent.files") // optional
	// SkillsKey is provided by a backend that can name the agent skills
	// available for a conversation's folder. Without it the composer cannot
	// offer a skill.
	SkillsKey = kernel.NewKey[Skills]("agent.skills") // optional
	// RewindKey is provided by a backend that can take back the latest turns
	// of a conversation, so a message can be retried or edited in place.
	RewindKey = kernel.NewKey[Rewinder]("agent.rewind") // optional
	// ChangesKey is provided by a backend that can tell which files a
	// conversation changed, and how.
	ChangesKey = kernel.NewKey[Changes]("agent.changes") // optional

	// Events carries ordered batches of normalized events from the current
	// connection, emitted on the UI thread.
	Events = kernel.NewTopic[[]Event]("agent.events")
	// StatusChanged is emitted on the UI thread whenever Status changes.
	StatusChanged = kernel.NewTopic[Status]("agent.status")
	// ModelsChanged is emitted on the UI thread when the backend's model
	// catalog changed on the same connection, such as after a provider was
	// connected; consumers of Models read it again.
	ModelsChanged = kernel.NewTopic[struct{}]("agent.models")
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
	// Messages returns a conversation's stored messages, oldest first.
	Messages(ctx context.Context, session SessionID) ([]Message, error)
	// Send reports admission of one prompt, independently of run status in Events.
	// Its disposition is meaningful only when the error is nil.
	Send(ctx context.Context, req Send) (SendDisposition, error)
	Abort(ctx context.Context, session SessionID) error
	Reply(ctx context.Context, req Reply) error
	Rename(ctx context.Context, session SessionID, title string) error
	Delete(ctx context.Context, session SessionID) error
}

// ChildrenKey is provided only by backends that can create real child agents.
// Children have independent context and belong to a parent session; they are
// excluded from the backend's top-level session list.
var ChildrenKey = kernel.NewKey[Children]("agent.children")

// Children creates child agents. Resume, observe, and stop them through Service
// Send, Events, and Abort using the returned session ID. Spawn never retries.
type Children interface {
	Spawn(ctx context.Context, req Spawn) (Session, error)
}

// Spawn starts a fresh child under Parent without copying the parent's history.
// Name identifies its role. A failed Spawn may return a created session with the
// error; callers must retain its ID instead of creating another child on retry.
type Spawn struct {
	Parent    SessionID
	Name      string
	Directory string
	Mode      string
	Model     ModelRef
	Policy    ToolPolicy
}

// ToolPolicy fixes a child's permissions. Deny wins over Allow; unlisted kinds
// require approval. A backend must apply the policy when creating the child,
// rather than relying on its user's potentially permissive defaults.
type ToolPolicy struct {
	Allow []ToolKind
	Deny  []ToolKind
}

// Models is the optional model catalog capability.
type Models interface {
	Models(ctx context.Context) ([]Model, error)
	// DefaultModel is what the backend runs when no model is chosen.
	DefaultModel(ctx context.Context) (ModelRef, error)
	SetModel(ctx context.Context, session SessionID, model ModelRef) error
}

// Modes is the optional capability of running a conversation under a named
// agent mode, such as a planning mode that doesn't change files.
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

// Images is the optional capability of saying how large an image reaches a
// model, after the backend scales it down to the model's limits. Like
// Service, call it off the UI thread.
type Images interface {
	// Fit returns the size in pixels file is sent to model at, accounting
	// for dimension and encoded byte limits; the zero model is the backend's
	// default. File bytes are read-only. An image that cannot be prepared
	// returns an error.
	Fit(ctx context.Context, model ModelRef, file File) (int, int, error)
}

// Files is the optional capability of finding and reading the files of a
// conversation's folder. The backend may run on another machine than the app,
// so these are the backend's files, not the app's. Like the rest of Service,
// the methods block on I/O and are called off the UI thread.
type Files interface {
	// Find returns the files and folders in directory (the backend's default
	// folder when empty) that match query, best first, at most limit of them.
	// Paths are slash-separated and relative to directory, without a trailing
	// slash. An empty query returns some entries in no particular order.
	Find(ctx context.Context, directory, query string, limit int) ([]FileEntry, error)
	// Read returns the contents of the file at path, relative to directory.
	Read(ctx context.Context, directory, path string) ([]byte, error)
}

// FileEntry is one file or folder a lookup found.
type FileEntry struct {
	Path  string // relative to the folder searched, without a trailing slash
	IsDir bool   // the path names a folder, which Read can't read
}

// Skills is the optional capability of naming the agent skills a conversation
// can use. The backend may run on another machine than the app, so these are
// its skills, discovered in its folders. Like the rest of Service, it blocks
// on I/O and is called off the UI thread.
type Skills interface {
	// Skills returns the skills available for directory (the backend's
	// default folder when empty), in no particular order.
	Skills(ctx context.Context, directory string) ([]Skill, error)
}

// Skill is one agent skill the backend can attach to a message.
type Skill struct {
	ID          string // what the backend takes when attaching it
	Name        string // what the user types after "/"
	Description string
}

// Changes is the optional capability of listing the files a conversation
// changed. Like the rest of Service, it blocks on I/O and is called off the
// UI thread.
type Changes interface {
	// Changes lists the files the conversation changed, each as it differs
	// now from before the conversation touched it.
	Changes(ctx context.Context, session SessionID) ([]FileChange, error)
}

// FileChange is one file a conversation changed.
type FileChange struct {
	Path           string // relative to the conversation's folder
	Status         FileStatus
	Added, Removed int    // lines
	Patch          string // a unified diff; empty when the backend sends none
}

type FileStatus uint8

const (
	FileModified FileStatus = iota
	FileAdded
	FileDeleted
)

// Rewinder is the optional capability of taking back a conversation's latest
// turns. Like the rest of Service, Rewind blocks on I/O and is called off the
// UI thread; it is a mutation, never retried, and a failure has an unknown
// outcome.
type Rewinder interface {
	// Rewind removes message, a user message, and everything after it. Files
	// the agent changed stay as they are. It returns the conversation to go on
	// in: session itself when the backend rewinds in place, or a new
	// conversation holding what came before message when it can only fork,
	// leaving session as it was.
	Rewind(ctx context.Context, session SessionID, message MessageID) (SessionID, error)
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
	// Origin names where the backend keeps its conversations, such as
	// "pig /path/to/app/pig". A session ID means
	// something only under the origin it came from. Empty while stopped.
	Origin string
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
	Context  int // the context window in tokens; 0 when unknown
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
	Title     string // empty for the backend's default
	Directory string
	Model     ModelRef // zero for the backend's default
	Mode      string   // empty for the backend's default
}

// Snapshot is a bounded copy of the project files at a task boundary.
type Snapshot map[string][]byte

// Snapshots compares task changes against the working files, including existing edits.
type Snapshots interface {
	Snapshot(context.Context, SessionID) (Snapshot, error)
	Diff(context.Context, SessionID, Snapshot) ([]FileChange, error)
}

type Send struct {
	ID string // correlates this admission with its events
	// Instructions and Policy apply to this turn only. Internal prompts stay out of user history.
	Instructions string
	Policy       *ToolPolicy
	ResultSchema json.RawMessage // optional schema for the host submit_result tool
	Internal     bool
	Session      SessionID
	Mode         string // select a mode before sending; empty keeps the session's current mode
	Text         string
	Files        []File
	Skills       []SkillRef // skills the backend attaches to this message
}

// SkillRef is one skill attached to a message.
type SkillRef struct {
	ID string
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

// Tokens is what one assistant message used. Output does not include
// Reasoning, whatever the backend counts.
type Tokens struct {
	Input, Output, Reasoning int
	CacheRead, CacheWrite    int
}

// Context is how much of the context window the message filled: everything
// it read and everything it wrote.
func (t Tokens) Context() int {
	return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning
}

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

// ToolKind is what a tool does. Each backend maps its own tool and permission
// names onto it, so the app words and draws a call without knowing any
// backend's names.
type ToolKind uint8

const (
	ToolOther  ToolKind = iota // anything else; Name or Action says what
	ToolShell                  // runs a command
	ToolRead                   // reads files
	ToolSearch                 // finds files, or text in them
	ToolEdit                   // creates or changes files
	ToolFetch                  // reaches the web
)

// Tool is one tool call, complete in a stored message or partial in an event.
type Tool struct {
	ID        string
	Name      string   // the backend's own name, shown for ToolOther
	Kind      ToolKind // ToolOther in an event that doesn't say; ToolStarted does
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
	RequestID   string // the Send.ID that owns this event, empty for unmanaged events
	Kind        EventKind
	Session     SessionID
	Text        *TextEvent
	Tool        *Tool
	Approval    *Approval
	Interaction *Interaction
	Model       ModelRef
	Title       string
	Error       string          // TurnFailed: the backend's reason, for the user
	Result      json.RawMessage // TurnSucceeded: validated structured result, if requested
	Bytes       int             // wire size, for batching
}

type TextEvent struct {
	Message MessageID
	Ordinal int // which text block of the message
	Delta   string
	Text    string
}

// Approval is a permission the agent waits for before acting.
type Approval struct {
	Session   SessionID // actual owner when presented inside another conversation
	ID        ApprovalID
	Kind      ToolKind // what the agent wants to do
	Action    string   // the backend's own word for it, shown for ToolOther
	Resources []string // what it wants to act on
	Save      []string // the patterns Always would remember
}
