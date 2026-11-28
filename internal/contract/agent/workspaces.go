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
	// ErrUntrusted reports that a workspace's wada.json may not run until the
	// user trusts it.
	ErrUntrusted = errors.New("this wada.json hasn't been trusted to run")
)

// WorkspaceConfig is a workspace's wada.json as the backend read it: the
// setup a new worktree runs and the services it can start, each with a port
// of the workspace's own.
type WorkspaceConfig struct {
	Found   bool
	Hash    string // of the file; TrustConfig names it
	Trusted bool   // its project's user allowed this file to run
	Problem string // the file didn't parse
	Setup   []string
	// Teardown runs before the backend removes the workspace's worktree.
	Teardown []string
	// SetupRun is the last setup started in the workspace; nil for none.
	SetupRun *Run
	Services []WorkspaceService
}

// WorkspaceService is a service of a wada.json.
type WorkspaceService struct {
	Name    string
	Command string
	Port    int
	Run     *Run // the last one started; nil for none
}

// Run is a command running, or run, in a terminal.
type Run struct {
	Terminal string // the terminal's ID
	Running  bool
	ExitCode int
}

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
	// Config reads a workspace's wada.json and what of it runs.
	Config(ctx context.Context, id WorkspaceID) (WorkspaceConfig, error)
	// TrustConfig allows the wada.json with this hash to run in the
	// workspace's project.
	TrustConfig(ctx context.Context, id WorkspaceID, hash string) error
	// RunSetup starts the workspace's setup in a terminal, or returns the
	// one running; it fails with ErrUntrusted until the file is trusted.
	RunSetup(ctx context.Context, id WorkspaceID) (terminal string, err error)
	// StartService starts a service in a terminal, or returns the one it runs
	// in; it fails with ErrUntrusted until the file is trusted.
	StartService(ctx context.Context, id WorkspaceID, name string) (terminal string, err error)
	// StopService ends a running service.
	StopService(ctx context.Context, id WorkspaceID, name string) error
	// DraftCommit has the model write a commit message for everything
	// changed in the workspace.
	DraftCommit(ctx context.Context, id WorkspaceID) (string, error)
	// Commit commits everything changed in the workspace; it returns the
	// commit's hash.
	Commit(ctx context.Context, id WorkspaceID, message string) (string, error)
	// DraftPR has the model write a pull request's title and body for the
	// workspace's commits since its base.
	DraftPR(ctx context.Context, id WorkspaceID) (title, body string, err error)
	// CreatePR pushes the workspace's branch and opens a pull request
	// against its base.
	CreatePR(ctx context.Context, id WorkspaceID, title, body string, draft bool) (*PRStatus, error)
}
