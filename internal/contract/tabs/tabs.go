// Package tabs declares the window's tabs: the conversations, new chats,
// panes and pages the user has open. Conversations, new chats and panes are
// each in the space of their folder; the strip shows one space at a time. A
// tab only points at what it shows; the chat plugin still has one open
// conversation, and selecting a tab opens it there. A pane is a view another
// plugin contributes to Panes, such as a terminal or a workspace's changes.
package tabs

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

var (
	ServiceKey = kernel.NewKey[Service]("tabs.service")
	Changed    = kernel.NewTopic[struct{}]("tabs.changed")

	// EndActions are buttons at the end of the strip, before Settings, such as
	// the search of All chats.
	EndActions = kernel.NewPoint[EndAction]("tabs.end-actions")

	// Panes are the kinds of pane a tab can show, by ID. A pane tab whose
	// kind is no longer contributed closes.
	Panes = kernel.NewPoint[PaneKind]("tabs.panes")
)

// PaneKind draws the pane tabs of one kind. Ref says which of its things a
// tab shows, such as a terminal's ID; it is the plugin's own.
type PaneKind struct {
	// Title names a tab; called while drawing the strip, so it reads memory
	// only. "" keeps the title the tab had, as saved from the last run.
	Title  func(ref string) string
	Icon   func() *ui.SVG
	Render func(c *ui.Context, t Tab)
	// Show runs on the UI thread when a tab of the kind comes on show, and
	// Hide when the one on show goes; both may be nil. Show runs again for
	// each tab shown, without a Hide between two tabs of the kind.
	Show func(t Tab)
	Hide func()
}

// EndAction is a button at the end of the strip. Run is posted after the
// click, so it may open a dialog.
type EndAction struct {
	Label   string // also its accessible name
	Tooltip string // such as "All chats  ⌘K"
	Icon    func() *ui.SVG
	Run     func()
}

// Kind is what a tab shows.
type Kind uint8

const (
	Conversation Kind = iota // a stored conversation: Session
	Draft                    // a new chat not sent yet, in Folder
	Page                     // a shell page other than the chat: Page
	Pane                     // a plugin's view in Folder: Pane and Ref
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
	Pane    string          // Pane: a Panes ID
	Ref     string          // Pane: which of its things it shows
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
	// Space is the folder whose tabs the strip shows; empty for the agent's
	// default folder.
	Space() string
	// Open shows the tab of a pane and ref in a folder's space, opening one
	// beside the active tab when there is none.
	Open(pane, ref, folder string)
}
