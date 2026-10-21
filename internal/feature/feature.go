// Package feature is the native shell's small extension boundary. Modules own
// their state and resources; the shell only invokes registered contributions.
package feature

import (
	"context"
	"encoding/json"

	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type Context struct {
	Closed                  bool
	Unavailable             error
	Connection              uint64
	Attempt                 uint64
	Policy                  workspace.Policy
	Ready, IsDraft          bool
	Revision                uint64
	Server, Directory, Home string
	Local                   bool
	Work                    context.Context
	Workspace               workspace.Client
}

// Host state/context callbacks run on the UI thread. Background schedules an
// immutable request and publishes its returned closure on that thread; Post is
// the dispatcher workers may call. Modules derive and cancel a child Lifetime.
type Host struct {
	OpenFolder      func()
	Lifetime        context.Context
	Note            func(string)
	ComposerBlocked func() bool
	TogglePanel     func(string)
	Context         func() Context
	Background      func(func() func())
	Post            func(func())
	OpenPanel       func(string)
	Reveal          func()
	Draft           func() string
	SetDraft        func(string)
	Copy            func(string) error
	Notify          func(ui.Toast)
	Save            func()
}
type Factory func(Host) Module
type Module interface {
	ID() string
	Close()
}
type KeyBinding struct {
	Mods ui.Modifiers
	Key  ui.Key
}
type Action struct {
	ExtraKeys                        []KeyBinding
	ID, Title, Description, Shortcut string
	MenuGroup, MenuLabel, Slash      string
	Mods                             ui.Modifiers
	Key                              ui.Key
	Icon                             *ui.SVG
	Run                              func()
}
type Panel struct {
	ID, Title, Description string
	Icon                   *ui.SVG
	Open                   func()
	Render                 func(*ui.Context)
	Refresh                func()
}

// Contributions are registered once; dynamic UI state belongs inside callbacks.
type Actions interface{ Actions() []Action }
type Panels interface{ Panels() []Panel }
type ContextAware interface{ ContextChanged() }
type Dialogs interface {
	Dialogs(*ui.Context)
	Modal() bool
}
type Storage interface {
	Snapshot() json.RawMessage
	Restore(json.RawMessage)
}

// Bottom owns its complete panel layout, including sizing and restoration.
type Bottom interface {
	Layout(*ui.Context, float32, func())
	IsOpen() bool
	SetOpen(bool)
	ResetSize()
}

type FileOpener interface{ OpenFile(string) }
type FileDrag struct{ Path string }

type Dismissible interface{ Dismiss() }

// Composer contributions receive input only after the shell handles its keys.
type Composer interface {
	SyncComposer()
	ComposerOverlay(*ui.Context)
	ComposerKey(ui.TextKey) bool
	BeforeSend() bool
	ResetComposer()
}
type Header interface{ Header(*ui.Context) }
type Ready interface{ Connected() }
type Accessory interface{ ComposerActions(*ui.Context, bool) }

type Starter interface{ Start() }

type Suggestion struct {
	Title, Detail, Prompt string
	Icon                  *ui.SVG
}
type DraftContent struct {
	Description string
	Suggestions []Suggestion
}
type DraftScreen interface{ DraftContent() DraftContent }
