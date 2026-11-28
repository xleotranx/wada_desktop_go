package agent

import (
	"context"
	"errors"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	// WorkspacesKey is provided by a backend that keeps the projects the user
	// works in and their workspaces. Without it the app knows no projects.
	WorkspacesKey = kernel.NewKey[Workspaces]("agent.workspaces") // optional
	// WorkspaceUpdated is emitted on the UI thread when the backend reports a
	// project or workspace was added or changed.
	WorkspaceUpdated = kernel.NewTopic[WorkspaceUpdate]("agent.workspace-updated")

	// ErrWorkUnsaved reports that archiving a workspace would lose uncommitted
	// changes or unpushed commits; archiving it with force discards them.
	ErrWorkUnsaved = errors.New("the workspace has work that isn't saved anywhere else")
)

// ProjectID and WorkspaceID are opaque; neither is a path.
type (
	ProjectID   string
	WorkspaceID string
)

// Isolation is how a workspace keeps its files apart from its project's.
type Isolation uint8

const (
	IsolateProject  Isolation = iota // the project's own directory
	IsolateWorktree                  // a Git worktree the backend made, and removes
	IsolateExisting                  // a Git worktree already on disk
)

// StartMode is how a new worktree starts.
type StartMode uint8

const (
	BranchOff      StartMode = iota // a new branch from a ref
	CheckoutBranch                  // the branch itself
	CheckoutPR                      // a pull request's branch
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
	ID           WorkspaceID
	Project      ProjectID
	Isolation    Isolation
	Directory    string
	WorktreeRoot string // the checkout Directory is in, for a worktree
	Branch       string
	BaseRef      string // what its changes compare against; empty for none
	PR           int    // the pull request it checks out
	Title        string // the user's; empty shows its branch, else its project's name
	Created      int64
	Archived     int64
}

// CreateWorkspace asks for a workspace in a worktree of a project's repository.
type CreateWorkspace struct {
	Project   ProjectID
	Isolation Isolation // IsolateWorktree or IsolateExisting
	Mode      StartMode // IsolateWorktree
	From      string    // BranchOff: the ref, empty for the default; CheckoutBranch: the branch
	PR        int       // CheckoutPR
	Path      string    // IsolateExisting: the worktree's directory
	Slug      string    // names the worktree and its new branch; empty derives one
	Title     string
}

// PullRequest is an open pull request a workspace can check out.
type PullRequest struct {
	Number int
	Title  string
	Branch string
	Fork   bool
}

// Refs is what a project's repository offers a new workspace.
type Refs struct {
	Default   string   // the ref a new branch starts from by default
	Branches  []string // local
	Remotes   []string // remote-tracking, such as origin/main
	PRs       []PullRequest
	Worktrees []Worktree
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
	// CreateWorkspace makes a workspace in a worktree; a failure may leave
	// nothing behind or a workspace whose creation went unreported.
	CreateWorkspace(ctx context.Context, req CreateWorkspace) (Workspace, error)
	// ArchiveWorkspace archives a worktree workspace, removing a worktree the
	// backend made. Without force, one with unsaved work is refused with
	// ErrWorkUnsaved.
	ArchiveWorkspace(ctx context.Context, id WorkspaceID, force bool) error
	// RestoreWorkspace makes an archived workspace active again.
	RestoreWorkspace(ctx context.Context, id WorkspaceID) (Workspace, error)
	// Refs lists what a project's repository offers a new workspace.
	Refs(ctx context.Context, project ProjectID) (Refs, error)
	// BaseChanges lists a workspace's changes since it left its BaseRef:
	// commits and uncommitted work, with paths relative to its Directory.
	BaseChanges(ctx context.Context, id WorkspaceID) ([]FileChange, error)
}
