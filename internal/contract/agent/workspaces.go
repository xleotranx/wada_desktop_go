package agent

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	// WorkspacesKey is provided by a backend that keeps the projects the user
	// works in and their workspaces. Without it the app knows no projects.
	WorkspacesKey = kernel.NewKey[Workspaces]("agent.workspaces") // optional
	// WorkspaceUpdated is emitted on the UI thread when the backend reports a
	// project or workspace was added or changed.
	WorkspaceUpdated = kernel.NewTopic[WorkspaceUpdate]("agent.workspace-updated")
)

// ProjectID and WorkspaceID are opaque; neither is a path.
type (
	ProjectID   string
	WorkspaceID string
)

// Isolation is how a workspace keeps its files apart from its project's.
type Isolation uint8

const (
	IsolateProject Isolation = iota // the project's own directory
)

// Project is a folder the user works in.
type Project struct {
	ID       ProjectID
	Root     string // absolute, where the backend runs
	Name     string
	Inbox    bool  // where conversations started without a project go
	Created  int64 // Unix milliseconds
	Archived int64 // Unix milliseconds; 0 while active
}

// Workspace is one working directory of a project. No two active workspaces
// share a Directory, and a conversation or terminal works in the workspace at
// its directory.
type Workspace struct {
	ID        WorkspaceID
	Project   ProjectID
	Isolation Isolation
	Directory string
	Title     string // the user's; empty shows the project's name
	Created   int64
	Archived  int64
}

// WorkspaceUpdate carries one changed project or workspace.
type WorkspaceUpdate struct {
	Project   *Project
	Workspace *Workspace
}

// Workspaces is the backend's record of projects and workspaces. Like Service,
// its methods block on I/O and never retry a mutation.
type Workspaces interface {
	// Projects lists the active projects, and with archived the archived ones.
	Projects(ctx context.Context, archived bool) ([]Project, error)
	// Workspaces lists every project's active workspaces, and with archived
	// the archived ones.
	Workspaces(ctx context.Context, archived bool) ([]Workspace, error)
	// AddProject returns the active project at root and its project-directory
	// workspace, restoring an archived one or creating one.
	AddProject(ctx context.Context, root string) (Project, Workspace, error)
	// RenameProject sets a name; empty goes back to the folder's name.
	RenameProject(ctx context.Context, id ProjectID, name string) error
	// ArchiveProject archives a project and its workspaces. Their
	// conversations stay with the backend.
	ArchiveProject(ctx context.Context, id ProjectID) error
	RenameWorkspace(ctx context.Context, id WorkspaceID, title string) error
}
