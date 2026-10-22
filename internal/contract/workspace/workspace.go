// Package workspace declares the app's own record of where the user works:
// the folders they chose and the conversations started in this app. The
// tabs and All chats list only these, grouped by folder; the backend
// still owns each conversation's messages.
package workspace

import (
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
)

var (
	ServiceKey = kernel.NewKey[Service]("workspace.service")

	// Changed is emitted on the UI thread after the folders or the listed
	// conversations change, including when the record has been read and
	// when another backend origin becomes current.
	Changed = kernel.NewTopic[struct{}]("workspace.changed")
)

// Folder is a folder the user works in.
type Folder struct {
	Path      string // absolute and clean; what a conversation's Folder names
	Collapsed bool   // its tab group shows only its label
	Added     int64  // Unix milliseconds
}

// Conversation is the app's record of a conversation it started.
type Conversation struct {
	ID     agent.SessionID
	Folder string // a Folder's Path; empty for the backend's default folder
	Title  string // the backend's title as last seen; empty until it has one
	Model  agent.ModelRef
	// Created and Updated are Unix milliseconds; Updated is the last
	// activity seen.
	Created, Updated int64
}

// Session is the conversation as chat.Controller.Open takes it.
func (c Conversation) Session() agent.Session {
	return agent.Session{ID: c.ID, Title: c.Title, Directory: c.Folder, Updated: c.Updated, Model: c.Model}
}

// Service is the record. Call it on the UI thread: reads come from memory,
// and changes are saved in the background.
type Service interface {
	// Loaded reports that the record has been read; until then the lists
	// are empty.
	Loaded() bool
	// Folders lists the folders by name.
	Folders() []Folder
	// Conversations lists the conversations of the current backend origin,
	// newest first.
	Conversations() []Conversation
	Lookup(id agent.SessionID) (Conversation, bool)

	// AddFolder adds an absolute folder; adding a known one does nothing.
	AddFolder(path string) error
	// RemoveFolder forgets a folder and the app's record of its
	// conversations, under every origin. The backend keeps them.
	RemoveFolder(path string)
	SetCollapsed(path string, collapsed bool)
	// ChooseFolder asks the user for a folder with the system dialog. A
	// chosen folder is added, then done runs with it on the UI thread.
	ChooseFolder(done func(path string))

	// Retitle records a title the user gave a conversation.
	Retitle(id agent.SessionID, title string)
	// Forget drops a conversation from the record, as after deleting it.
	Forget(id agent.SessionID)
}
