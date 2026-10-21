// Package chat declares what the chat plugin lets other plugins use and
// extend: the open conversation, the outgoing message pipeline and the
// composer's accessory slot.
package chat

import (
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
)

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
}

// Controller navigates the chat view. Call it on the UI thread.
type Controller interface {
	Current() Conversation
	// Open shows a stored conversation and loads its messages.
	Open(s agent.Session)
	// NewChat shows an empty conversation that will work in directory.
	NewChat(directory string)
}
