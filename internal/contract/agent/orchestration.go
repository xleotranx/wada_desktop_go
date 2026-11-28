package agent

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	// HarnessesKey is an optional backend capability listing embedded and external
	// coding-agent harnesses (PiG, Claude Code, Codex, ACP).
	HarnessesKey = kernel.NewKey[Harnesses]("agent.harnesses")
	// ProfilesKey is an optional backend capability listing agent profiles.
	ProfilesKey = kernel.NewKey[Profiles]("agent.profiles")
	// WorkersKey is an optional backend capability listing supervised external workers.
	WorkersKey = kernel.NewKey[Workers]("agent.workers")
	// WorktreesKey is an optional backend capability reading a checkout's pull
	// request.
	WorktreesKey = kernel.NewKey[Worktrees]("agent.worktrees")

	// WorkerUpdated is emitted on the UI thread whenever a managed worker's status changes.
	WorkerUpdated = kernel.NewTopic[Worker]("agent.worker-updated")
)

type HarnessKind uint8

const (
	HarnessEmbedded HarnessKind = iota
	HarnessACP
	HarnessClaude
	HarnessCodex
)

// EmbeddedHarness is the ID of the embedded PiG harness.
const EmbeddedHarness = "pig"

// ExternalHarness reports that a conversation's provider is an external CLI
// harness (Claude Code, Codex, ACP). Such a conversation takes a message's
// text only: no files, skills, child conversations or model changes. Its mode
// is chosen when it starts.
func ExternalHarness(provider string) bool {
	return provider != "" && provider != EmbeddedHarness
}

// Harness describes an available agent runtime/CLI adapter.
type Harness struct {
	ID          string
	Name        string
	Kind        HarnessKind
	Binary      string
	Available   bool
	Description string
}

type WorkerStatus uint8

const (
	WorkerIdle WorkerStatus = iota
	WorkerRunning
	WorkerWaitingApproval
	WorkerCompleted
	WorkerFailed
	WorkerStopped
)

// Worker represents a managed external coding CLI worker session.
type Worker struct {
	ID            SessionID
	ParentSession SessionID
	Provider      string
	Name          string
	Directory     string
	Branch        string
	WorktreePath  string
	Mode          string
	Status        WorkerStatus
	LastError     string
	Updated       int64
}

// PRStatus summarizes a GitHub/Forge pull request associated with a worktree branch.
type PRStatus struct {
	Number         int
	Title          string
	State          string
	Draft          bool
	URL            string
	Checks         string
	ReviewDecision string
}

// Worktree describes one Git checkout or linked worktree.
type Worktree struct {
	RepoRoot   string
	Path       string
	Branch     string
	HeadCommit string
	IsMain     bool
	Dirty      bool
	PR         *PRStatus
}

type Harnesses interface {
	Harnesses(ctx context.Context) ([]Harness, error)
}

// ProfileSource is where an agent profile is defined.
type ProfileSource uint8

const (
	ProfileUser    ProfileSource = iota // the user's, in the backend's data
	ProfileProject                      // the folder's wada.json
)

// Profile is a named way to start a coding agent: its harness (empty or
// EmbeddedHarness for the embedded agent), mode, and the instructions its
// first message begins with.
type Profile struct {
	Name         string
	Description  string
	Provider     string
	Mode         string
	Instructions string
	Source       ProfileSource
}

type Profiles interface {
	// Profiles is the profiles for an agent starting in directory, or the
	// user's only when it is empty.
	Profiles(ctx context.Context, directory string) ([]Profile, error)
}

type Workers interface {
	Workers(ctx context.Context, parent SessionID) ([]Worker, error)
}

type Worktrees interface {
	// PRStatus is the pull request of the branch checked out at directory,
	// or nil for none.
	PRStatus(ctx context.Context, directory string) (*PRStatus, error)
}
