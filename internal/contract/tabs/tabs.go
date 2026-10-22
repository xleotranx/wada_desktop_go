// Package tabs declares the window's tabs: the conversations, new chats and
// pages the user has open, each conversation and new chat in the space of its
// folder; the strip shows one space at a time. A tab only
// points at what it shows; the chat plugin still has one open conversation,
// and selecting a tab opens it there.
package tabs

import (
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
)

var (
	ServiceKey = kernel.NewKey[Service]("tabs.service")

	// Changed is emitted on the UI thread after the tabs, their order, their
	// titles or states, or the active tab change.
	Changed = kernel.NewTopic[struct{}]("tabs.changed")
)

// Kind is what a tab shows.
type Kind uint8

const (
	Conversation Kind = iota // a stored conversation: Session
	Draft                    // a new chat not sent yet, in Folder
	Page                     // a shell page other than the chat: Page
)

// State is what a tab's conversation is doing, as its marker shows it.
type State uint8

const (
	Idle    State = iota
	Working       // a turn runs
	Waiting       // the agent waits for an approval or an answer
	Unread        // a turn ended while the tab was not on show
)

// Tab is one open tab.
type Tab struct {
	ID      string
	Kind    Kind
	Session agent.SessionID // Conversation
	Folder  string          // its space: a folder, or "" for the agent's default
	Page    string          // Page: a shell.Pages ID
	Title   string
	State   State
}

// Service is the open tabs. Call it on the UI thread.
type Service interface {
	// Tabs lists the open tabs in order; a folder's tabs are adjacent.
	Tabs() []Tab
	// Active is the tab on show.
	Active() (Tab, bool)
	// Select shows a tab.
	Select(id string)
	// Close closes a tab; nothing it shows is deleted. Closing the active tab
	// shows its right neighbor on the strip, else its left one, else a new
	// chat in its space.
	Close(id string)
	// Move puts a tab at index among its folder's tabs.
	Move(id string, index int)
	// Space is the folder whose tabs the strip shows; empty for the agent's
	// default folder.
	Space() string
}
