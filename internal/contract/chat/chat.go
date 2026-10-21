// Package chat declares what the chat plugin lets other plugins use and
// extend: the open conversation, the outgoing message pipeline and the
// composer's accessory slot.
package chat

import (
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
)

// PageID is the conversation page. It is the default page, so the shell also
// shows it while no page was chosen.
const PageID = "chat.conversation"

var (
	ControllerKey = kernel.NewKey[Controller]("chat.controller")

	// Changed is emitted when the open conversation, its title or its model changes.
	Changed = kernel.NewTopic[Conversation]("chat.changed")

	// Outgoing runs on the UI thread when the user sends a message, before
	// anything reaches the backend. A transform may rewrite the draft (expand
	// mentions, choose the model of a new conversation) or reject it with an
	// error shown to the user.
	Outgoing = kernel.NewPipeline[Draft]("chat.outgoing")

	// ComposerAccessory views sit in the composer's leading row, such as a
	// model or mode picker.
	ComposerAccessory = kernel.NewPoint[shell.View]("chat.composer-accessory")
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
)

// Conversation is the conversation on show. An empty Session is a new chat
// that has not been sent yet.
type Conversation struct {
	Session   agent.SessionID
	Title     string
	Directory string
	Model     agent.ModelRef // the conversation's model; zero when unknown or new
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
}
