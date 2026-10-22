// Package chat declares what the chat plugin lets other plugins use and
// extend: the open conversation, the outgoing message pipeline and the
// composer's slots.
package chat

import (
	"errors"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

// PageID is the conversation page. It is the default page, so the shell also
// shows it while no page was chosen.
const PageID = "chat.conversation"

var (
	ControllerKey = kernel.NewKey[Controller]("chat.controller")

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
	// error shown to the user.
	Outgoing = kernel.NewPipeline[Draft]("chat.outgoing")

	// ComposerAccessory views sit in the composer's leading row, such as a
	// model or mode picker.
	ComposerAccessory = kernel.NewPoint[shell.View]("chat.composer-accessory")
	// ComposerTrailing views sit just before Send or Stop, such as a usage
	// meter. Unlike accessories they keep their width.
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
	// Targets are other places a new chat's first message can go than a new
	// conversation with the agent, such as a workflow. The composer's mode
	// picker lists them after the backend's modes.
	Targets = kernel.NewPoint[Target]("chat.targets")
)

// ErrTaken is what an Outgoing transform returns once it has taken the message
// itself, as a Target does: nothing goes to the backend, no error is shown and
// the composer empties.
var ErrTaken = errors.New("the message was taken")

// Target is somewhere else a message can go. Choosing it in the mode picker
// applies to the next message only; the picker goes back to the chosen mode
// once that message is sent.
type Target struct {
	Group   string // what the picker calls its section, such as "Workflows"
	Title   string
	Summary string // one line, shown under the title
	// Take receives the message instead of the backend, on the UI thread, with
	// the folder the user chose for the new chat.
	Take func(Draft)
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
	Session   agent.SessionID // empty starts a new conversation
	Directory string          // for a new conversation
	Model     agent.ModelRef  // for a new conversation; zero for the default
	Mode      string          // for a new conversation; empty for the default
	Text      string
	Files     []agent.File // sent with the message
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
}
