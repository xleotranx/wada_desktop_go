// Package workspace declares the app's view of where the user works: the
// backend's projects, their workspaces, and the conversations in those
// workspaces. The tabs, All chats and notifications list only these; the
// backend keeps the record and every conversation's messages.
package workspace

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	ServiceKey = kernel.NewKey[Service]("workspace.service")
	// Changed is emitted on the UI thread after the projects, workspaces or
	// listed conversations change, including when the record has been read
	// and when another backend origin becomes current.
	Changed = kernel.NewTopic[struct{}]("workspace.changed")
)

// Conversation is a conversation in an active workspace.
type Conversation struct {
	ID        agent.SessionID
	Workspace agent.WorkspaceID
	Directory string // its workspace's
	Title     string // the backend's title as last seen; empty until it has one
	Model     agent.ModelRef
	Updated   int64 // Unix milliseconds of the last activity seen
}

// Session is the conversation as chat.Controller.Open takes it.
func (c Conversation) Session() agent.Session {
	return agent.Session{ID: c.ID, Title: c.Title, Directory: c.Directory, Workspace: c.Workspace, Updated: c.Updated, Model: c.Model}
}

// Service is the record as last read from the backend. Call it on the UI
// thread: reads come from memory, and changes go to the backend in the
// background.
type Service interface {
	// Loaded reports that the record has been read from the current
	// connection; until then the lists are empty.
	Loaded() bool
	// Projects lists the active projects by name, the inbox last.
	Projects() []agent.Project
	// Workspaces lists the active workspaces in the order of their projects.
	Workspaces() []agent.Workspace
	Workspace(id agent.WorkspaceID) (agent.Workspace, bool)
	Project(id agent.ProjectID) (agent.Project, bool)
	// At returns the active workspace at a directory; "" is the inbox's.
	At(dir string) (agent.Workspace, bool)
	// Name is what the app calls the workspace at a directory: its title,
	// else a worktree's branch, else its project's name, else the
	// directory's own name.
	Name(dir string) string
	// Conversations lists the conversations in active workspaces, newest
	// first.
	Conversations() []Conversation
	Lookup(id agent.SessionID) (Conversation, bool)

	// ChooseProject asks the user for a folder with the system dialog and adds
	// it as a project; done runs on the UI thread with its workspace.
	ChooseProject(done func(agent.Workspace))
	// ArchiveProject archives a project and its workspaces; done runs on the
	// UI thread once the backend has. A failure is reported in a toast.
	ArchiveProject(id agent.ProjectID, done func())
	// CreateWorkspace makes a workspace in a worktree; done runs on the UI
	// thread with it, or with the error, which the caller reports.
	CreateWorkspace(req agent.CreateWorkspace, done func(agent.Workspace, error))
	// ArchiveWorkspace archives a worktree workspace; done runs on the UI
	// thread with nil once the backend has, or with the error, which may be
	// agent.ErrWorkUnsaved.
	ArchiveWorkspace(id agent.WorkspaceID, force bool, done func(error))
	// Refs reads what a project's repository offers a new workspace; done
	// runs on the UI thread.
	Refs(project agent.ProjectID, done func(agent.Refs, error))
	// Retitle records a title the backend accepted for a conversation.
	Retitle(id agent.SessionID, title string)
	// Forget drops a conversation from the list, as after deleting it.
	Forget(id agent.SessionID)
}
