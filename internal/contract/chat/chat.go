// Package chat declares what the chat plugin lets other plugins use and
// extend: the open conversation, the outgoing message pipeline, the
// composer's slots, the actions on selected text and the page's drawers.
package chat

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// PageID is the conversation page. It is the default page, so the shell also
// shows it while no page was chosen.
const PageID = "chat.conversation"

var (
	RequestAnswered = kernel.NewTopic[Answered]("chat.request-answered")
	UserAdmitted    = kernel.NewTopic[Draft]("chat.user-admitted")
	QueueRemoved    = kernel.NewTopic[Draft]("chat.queue-removed")
	ControllerKey   = kernel.NewKey[Controller]("chat.controller")

	// Changed is emitted when the open conversation, its title, its folder
	// or its model changes.
	Changed = kernel.NewTopic[Conversation]("chat.changed")
	// Started is emitted once the backend has created a conversation for the
	// first message of a new chat, also when the user has opened another one
	// meanwhile. Directory is the folder the user chose for it, empty for the
	// backend's default, whatever the backend reports.
	Started = kernel.NewTopic[Conversation]("chat.started")
	// Leaving is emitted just before the chat replaces the open conversation
	// with another, with what the user had typed and not sent, so a plugin
	// can keep it and give it back with Controller.SetDraft.
	Leaving = kernel.NewTopic[Unsent]("chat.leaving")

	// Outgoing runs on the UI thread when the user sends a message, before
	// anything reaches the backend. A transform may rewrite the draft (expand
	// mentions, choose the model of a new conversation) or reject it with an
	// error shown to the user. What it consumes, such as the files waiting in
	// the composer, it gives up in its commit, which the chat runs once every
	// transform has accepted the draft and the message is sent.
	Outgoing = kernel.NewPipeline[Draft]("chat.outgoing")

	// ComposerPrimary views stay visible in the footer below the editor, such
	// as the model and its reasoning effort. Keep these controls compact.
	ComposerPrimary = kernel.NewPoint[shell.View]("chat.composer-primary")
	// ComposerAccessory views sit in the footer below the editor. At narrow
	// widths they move into the composer options popover.
	ComposerAccessory = kernel.NewPoint[shell.View]("chat.composer-accessory")
	// ComposerTrailing views sit at the end of the footer, such as a usage
	// meter. At narrow widths they join the composer options popover.
	ComposerTrailing = kernel.NewPoint[shell.View]("chat.composer-trailing")
	// ComposerAttachments views sit above the composer's editor, such as the
	// files that will go with the message.
	ComposerAttachments = kernel.NewPoint[shell.View]("chat.composer-attachments")
	// FileDrop handlers take the paths of files dropped on the composer, on
	// the UI thread. The composer accepts drops only while one exists.
	FileDrop = kernel.NewPoint[func(paths []string)]("chat.file-drop")
	// Paste handlers see every paste into the composer, in order, with the
	// clipboard's text (empty for an image). The first to return true takes
	// the paste, and the text is not inserted.
	Paste = kernel.NewPoint[func(text string) bool]("chat.paste")
	// Completions offer words while the user types in the composer, such as a
	// file after "@". The composer owns the draft, the caret and the keys;
	// a contribution only says what to offer and what a choice becomes.
	Completions = kernel.NewPoint[Completion]("chat.completions")
	// Workflows are one-task behaviors listed after the access modes.
	Workflows        = kernel.NewPoint[Workflow]("chat.workflows")
	WorkflowSelected = kernel.NewTopic[string]("chat.workflow-selected")
	Incoming         = kernel.NewTopic[[]agent.Event]("chat.incoming") // external child approvals/questions shown in their parent
	DeliveryKey      = kernel.NewKey[Delivery]("chat.delivery")
	DeliveryError    = kernel.NewTopic[DeliveryFailure]("chat.delivery-error")
	DeliveryChanged  = kernel.NewTopic[agent.SessionID]("chat.delivery-changed")
	Activities       = kernel.NewPoint[Activity]("chat.activities")
	// Work sections fill the work panel beside a stored conversation, top to
	// bottom by Order, such as its usage or the files it changed.
	Work = kernel.NewPoint[WorkSection]("chat.work")
	// HideWork entries hide the work panel while one of them reports true,
	// such as a drawer that wants the room; the user's choice to show it
	// stays as it is. They run on the UI thread while drawing and only read
	// state.
	HideWork = kernel.NewPoint[func() bool]("chat.hide-work")
	// SelectionActions are offered in a small popover beside text the user
	// selected in the transcript, by Order and before the chat's own Reply.
	SelectionActions = kernel.NewPoint[SelectionAction]("chat.selection-actions")
	// Drawers sit at the right edge of the conversation page, after the work
	// panel, such as a side chat. Each draws its own column, with its width
	// and its border, and draws nothing while it is closed.
	Drawers = kernel.NewPoint[shell.View]("chat.drawers")
)

// SelectionAction is one choice for selected text. Run is called on the UI
// thread after the click, and the popover closes.
type SelectionAction struct {
	Title string
	Icon  func() *ui.SVG // optional
	Run   func(Selection)
}

// Selection is text the user selected in the open conversation's transcript.
type Selection struct {
	Conversation Conversation
	Text         string // the selected words; paragraphs on lines of their own
}

// WorkSection is one section of the work panel. Shown and Render run on the
// UI thread while drawing, so they only read state.
type WorkSection struct {
	Title string
	// Shown reports that the section has something to show now; nil means
	// always. The panel shows while at least one section does.
	Shown  func() bool
	Render func(c *ui.Context)
}

// Workflow is a one-message selection. It never changes the backend access mode.
type Workflow struct{ Title, Summary string }

// Delivery owns admission and serialization on the UI thread. Validate runs
// before outgoing commits; Admit receives the fully prepared, created session.
type DeliveryFailure struct {
	Session agent.SessionID
	Text    string
}

type Delivery interface {
	Validate(Draft) error
	Admit(Draft) error
	Busy(agent.SessionID) bool
	Stop(agent.SessionID)
}

// Activity contributes stable transcript entries after a visible user message.
type Activity struct {
	Items func(agent.SessionID) []ActivityItem
}
type ActivityItem struct {
	ID     string
	After  int // one-based visible user-message ordinal
	Render func(*ui.Context)
}

// Completion suggests words for the composer. While the caret is in a word
// that starts with Trigger, at the start of the text or after whitespace,
// the composer lists Candidates above the editor: Up and Down move through
// them, Enter or Tab choose one, Escape hides the list until the word changes.
//
// Everything runs on the UI thread. Candidates is called while drawing, so it
// only reads state; Query is where a contribution starts its lookups.
type Completion struct {
	Trigger rune
	// Hint says what typing Trigger does, for the empty composer's hint, such
	// as "mentions a file"; empty adds none.
	Hint string
	// Query runs after the frame in which the typed part changed, with the
	// text after Trigger, up to the caret. active is false once the caret is
	// no longer in such a word, so a lookup in flight can be dropped.
	Query func(typed string, active bool)
	// Candidates is what to offer now, best first; the composer lists the
	// first eight. An empty list shows nothing.
	Candidates func() []Candidate
	// Accept runs when the user chooses a candidate. It returns the text that
	// replaces the whole word, Trigger included; the caret lands after it.
	Accept func(c Candidate) string
}

// Candidate is one suggestion.
type Candidate struct {
	ID     string         // given back to Accept
	Label  string         // what it is called
	Detail string         // quieter text after the label, such as its folder
	Icon   func() *ui.SVG // optional
}

// Conversation is the conversation on show. An empty Session is a new chat
// that has not been sent yet.
type Conversation struct {
	// DraftID identifies a new chat within the chat plugin's lifetime. It
	// follows its first message through Outgoing, Changed and Started even
	// after navigation; opening a stored conversation gives it zero.
	DraftID   uint64
	Session   agent.SessionID
	Title     string
	Directory string
	Model     agent.ModelRef // the conversation's model; zero when unknown or new
	// Starting is set while the first message of a new chat is creating its
	// conversation; its folder can no longer change.
	Starting bool
}

// Unsent is the text left in the composer of a conversation the chat is
// leaving. Text is empty when nothing was typed, and while an earlier
// message was being edited, since that text is not a new message.
type Unsent struct {
	Conversation Conversation
	Text         string
}

// Draft is one message on its way out.
type Draft struct {
	Workflow  string          // selected for this submitted task only
	Anchor    int             // one-based visible user message for transcript activities
	DraftID   uint64          // the source new chat's identity; zero for stored conversations
	Session   agent.SessionID // empty starts a new conversation
	Directory string          // the chat's working folder
	Model     agent.ModelRef  // for a new conversation; zero for the default
	Mode      string          // mode for this message; empty keeps the backend's choice
	Text      string
	Files     []agent.File     // sent with the message
	Skills    []agent.SkillRef // attached to the message
}

// Controller navigates the chat view. Call it on the UI thread.
type Controller interface {
	Current() Conversation
	// Open shows a stored conversation and loads its messages.
	Open(s agent.Session)
	// NewChat shows an empty conversation that will work in directory.
	NewChat(directory string)
	// SetDirectory changes the folder of a new chat that has not been sent,
	// keeping the draft. It does nothing while a stored conversation is open
	// or the first message is Starting one.
	SetDirectory(directory string)
	// SetDraft puts text in the composer of the open conversation, replacing
	// what is there, and focuses it.
	SetDraft(text string)
	// DraftText returns the unsent composer text. An earlier message being
	// edited is not a new draft and returns empty.
	DraftText() string
	// WorkShown reports that the work panel is on show beside the open
	// conversation. Call it on the UI thread; it only reads state.
	WorkShown() bool
}

// Answered names a successfully delivered response, including a child's owner.
type Answered struct {
	Session     agent.SessionID
	Approval    agent.ApprovalID
	Interaction agent.InteractionID
}
