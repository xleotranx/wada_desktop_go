package protocol

// Dotted RPC method names.
const (
	MethodDaemonStatus       = "daemon.status"
	MethodDaemonShutdown     = "daemon.shutdown"
	MethodDaemonPairingOffer = "daemon.pairing.offer"
	MethodDaemonSetPassword  = "daemon.password.set"

	MethodSessionsList    = "agent.sessions.list"
	MethodSessionCreate   = "agent.session.create"
	MethodSessionMessages = "agent.session.messages"
	MethodSessionSend     = "agent.session.send"
	MethodSessionAbort    = "agent.session.abort"
	MethodSessionReply    = "agent.session.reply"
	MethodSessionRename   = "agent.session.rename"
	MethodSessionDelete   = "agent.session.delete"
	MethodSessionSpawn    = "agent.session.spawn"
	MethodSessionRewind   = "agent.session.rewind"
	MethodSessionChanges  = "agent.session.changes"

	MethodModelsList      = "agent.models.list"
	MethodModelsDefault   = "agent.models.default"
	MethodSessionSetModel = "agent.session.set_model"
	MethodModesList       = "agent.modes.list"

	MethodFilesFind  = "agent.files.find"
	MethodFilesRead  = "agent.files.read"
	MethodImagesFit  = "agent.images.fit"
	MethodSkillsList = "agent.skills.list"

	MethodProvidersList       = "agent.providers.list"
	MethodProviderLoginStart  = "agent.provider.login.start"
	MethodProviderLoginAnswer = "agent.provider.login.answer"
	MethodProviderLoginCancel = "agent.provider.login.cancel"
	MethodProviderLogout      = "agent.provider.logout"

	MethodHarnessesList    = "agent.harnesses.list"
	MethodWorkersList      = "agent.workers.list"
	MethodWorktreePR       = "worktree.pr"
	MethodWorktreeCreatePR = "worktree.pr.create"

	MethodProjectsList     = "workspace.projects.list"
	MethodProjectAdd       = "workspace.project.add"
	MethodProjectRename    = "workspace.project.rename"
	MethodProjectArchive   = "workspace.project.archive"
	MethodWorkspacesList   = "workspace.list"
	MethodWorkspaceRename  = "workspace.rename"
	MethodWorkspaceCreate  = "workspace.create"
	MethodWorkspaceArchive = "workspace.archive"
	MethodWorkspaceRestore = "workspace.restore"
	MethodWorkspaceRefs    = "workspace.refs"
	MethodWorkspaceChanges = "workspace.changes"
	MethodWorkspaceConfig  = "workspace.config"
	MethodWorkspaceTrust   = "workspace.trust"
	MethodWorkspaceSetup   = "workspace.setup"
	MethodServiceStart     = "workspace.service.start"
	MethodServiceStop      = "workspace.service.stop"

	MethodTerminalsList   = "terminal.list"
	MethodTerminalCreate  = "terminal.create"
	MethodTerminalAttach  = "terminal.attach"
	MethodTerminalDetach  = "terminal.detach"
	MethodTerminalCapture = "terminal.capture"
	MethodTerminalKill    = "terminal.kill"

	MethodInteractionsPending = "agent.interactions.pending"
	MethodApprovalsPending    = "agent.approvals.pending"
	MethodInteractionsAnswer  = "agent.interactions.answer"

	MethodTimelineFetch     = "agent.timeline.fetch"
	MethodEventsSubscribe   = "events.subscribe"
	MethodEventsUnsubscribe = "events.unsubscribe"
)

// Stable wire string enums.
type (
	SendDisposition       string
	Decision              string
	Role                  string
	PartKind              string
	ToolKind              string
	FileStatus            string
	EventKind             string
	InteractionKind       string
	ProviderAuthType      string
	ProviderAuthEventKind string
	HarnessKind           string
	WorkerStatus          string
	Isolation             string
	StartMode             string
)

const (
	SendAccepted SendDisposition = "accepted"
	SendQueued   SendDisposition = "queued"
	SendHandled  SendDisposition = "handled"
)

const (
	DecisionOnce   Decision = "once"
	DecisionAlways Decision = "always"
	DecisionReject Decision = "reject"
)

const (
	RoleOther     Role = "other"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

const (
	PartOther     PartKind = "other"
	PartText      PartKind = "text"
	PartReasoning PartKind = "reasoning"
	PartTool      PartKind = "tool"
)

const (
	ToolOther  ToolKind = "other"
	ToolShell  ToolKind = "shell"
	ToolRead   ToolKind = "read"
	ToolSearch ToolKind = "search"
	ToolEdit   ToolKind = "edit"
	ToolFetch  ToolKind = "fetch"
)

const (
	FileModified FileStatus = "modified"
	FileAdded    FileStatus = "added"
	FileDeleted  FileStatus = "deleted"
)

const (
	EventTextStarted          EventKind = "text_started"
	EventTextDelta            EventKind = "text_delta"
	EventTextEnded            EventKind = "text_ended"
	EventToolStarted          EventKind = "tool_started"
	EventToolCalled           EventKind = "tool_called"
	EventToolFinished         EventKind = "tool_finished"
	EventApprovalRequested    EventKind = "approval_requested"
	EventInteractionRequested EventKind = "interaction_requested"
	EventTurnStarted          EventKind = "turn_started"
	EventTurnSucceeded        EventKind = "turn_succeeded"
	EventTurnFailed           EventKind = "turn_failed"
	EventTurnInterrupted      EventKind = "turn_interrupted"
	EventModelChanged         EventKind = "model_changed"
	EventSessionRenamed       EventKind = "session_renamed"
	EventSessionDeleted       EventKind = "session_deleted"
	EventModelsChanged        EventKind = "models_changed"
	EventProviderAuth         EventKind = "provider_auth"
	EventWorkerUpdated        EventKind = "worker_updated"
	EventTerminalUpdated      EventKind = "terminal_updated"
	EventWorkspaceUpdated     EventKind = "workspace_updated"
)

const (
	IsolationProject  Isolation = "project"  // the project's own directory
	IsolationWorktree Isolation = "worktree" // a Git worktree wadad made
	IsolationExisting Isolation = "existing" // a Git worktree already on disk
)

// How a new worktree starts.
const (
	StartBranchOff      StartMode = "branch-off"      // a new branch from a ref
	StartCheckoutBranch StartMode = "checkout-branch" // the branch itself
	StartCheckoutPR     StartMode = "checkout-pr"     // a pull request's branch
)

const (
	HarnessEmbedded HarnessKind = "embedded"
	HarnessACP      HarnessKind = "acp"
	HarnessClaude   HarnessKind = "claude"
	HarnessCodex    HarnessKind = "codex"
)

const (
	WorkerIdle            WorkerStatus = "idle"
	WorkerRunning         WorkerStatus = "running"
	WorkerWaitingApproval WorkerStatus = "waiting_approval"
	WorkerCompleted       WorkerStatus = "completed"
	WorkerFailed          WorkerStatus = "failed"
	WorkerStopped         WorkerStatus = "stopped"
)

const (
	ProviderAuthAPIKey ProviderAuthType = "api_key"
	ProviderAuthOAuth  ProviderAuthType = "oauth"
)

const (
	ProviderAuthPromptText   ProviderAuthEventKind = "prompt_text"
	ProviderAuthPromptSecret ProviderAuthEventKind = "prompt_secret"
	ProviderAuthPromptCode   ProviderAuthEventKind = "prompt_code"
	ProviderAuthPromptSelect ProviderAuthEventKind = "prompt_select"
	ProviderAuthURL          ProviderAuthEventKind = "url"
	ProviderAuthDeviceCode   ProviderAuthEventKind = "device_code"
	ProviderAuthInfo         ProviderAuthEventKind = "info"
	ProviderAuthProgress     ProviderAuthEventKind = "progress"
	ProviderAuthDone         ProviderAuthEventKind = "done"
	ProviderAuthError        ProviderAuthEventKind = "error"
)

const (
	InteractionConfirm InteractionKind = "confirm"
	InteractionSelect  InteractionKind = "select"
	InteractionInput   InteractionKind = "input"
	InteractionEditor  InteractionKind = "editor"
)

type DaemonStatusResponse struct {
	ServerID       string `json:"serverId"`
	Version        string `json:"version"`
	Origin         string `json:"origin"`
	PID            int    `json:"pid"`
	Epoch          uint64 `json:"epoch"`
	DesktopManaged bool   `json:"desktopManaged"`
}

type ModelRef struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	Variant  string `json:"variant,omitempty"`
}

func (m ModelRef) IsZero() bool { return m.ID == "" }

type Model struct {
	Ref      ModelRef `json:"ref"`
	Name     string   `json:"name"`
	Variants []string `json:"variants,omitempty"`
	Free     bool     `json:"free,omitempty"`
	Context  int      `json:"context,omitempty"`
}

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Session struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Directory string   `json:"directory"`
	Workspace string   `json:"workspace,omitempty"` // the workspace at Directory, if any
	Provider  string   `json:"provider,omitempty"`
	Updated   int64    `json:"updated"`
	Model     ModelRef `json:"model"`
	Cost      float64  `json:"cost,omitempty"`
}

type SessionQuery struct {
	Cursor    string `json:"cursor,omitempty"`
	Search    string `json:"search,omitempty"`
	Directory string `json:"directory,omitempty"`
	Workspace string `json:"workspace,omitempty"` // only conversations in this workspace's directory
}

type SessionPage struct {
	Sessions []Session `json:"sessions"`
	Next     string    `json:"next,omitempty"`
}

type CreateSessionRequest struct {
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
	Title          string   `json:"title,omitempty"`
	Directory      string   `json:"directory,omitempty"`
	Workspace      string   `json:"workspace,omitempty"` // instead of Directory: an active workspace's
	Provider       string   `json:"provider,omitempty"`
	Model          ModelRef `json:"model"`
	Mode           string   `json:"mode,omitempty"`
}

type ToolPolicy struct {
	Allow []ToolKind `json:"allow,omitempty"`
	Deny  []ToolKind `json:"deny,omitempty"`
}

type SpawnRequest struct {
	IdempotencyKey string     `json:"idempotencyKey,omitempty"`
	Parent         string     `json:"parent"`
	Name           string     `json:"name"`
	Directory      string     `json:"directory,omitempty"`
	Mode           string     `json:"mode,omitempty"`
	Model          ModelRef   `json:"model"`
	Policy         ToolPolicy `json:"policy"`
}

type File struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data,omitempty"`
}

type SkillRef struct {
	ID string `json:"id"`
}

type SendRequest struct {
	MessageID string     `json:"messageId,omitempty"`
	Session   string     `json:"session"`
	Mode      string     `json:"mode,omitempty"`
	Text      string     `json:"text"`
	Files     []File     `json:"files,omitempty"`
	Skills    []SkillRef `json:"skills,omitempty"`
}

type SendResponse struct {
	Disposition SendDisposition `json:"disposition"`
}

type SessionIDRequest struct {
	Session string `json:"session"`
}

type ReplyRequest struct {
	Session  string   `json:"session"`
	Approval string   `json:"approval"`
	Decision Decision `json:"decision"`
}

type RenameRequest struct {
	Session string `json:"session"`
	Title   string `json:"title"`
}

type RewindRequest struct {
	Session string `json:"session"`
	Message string `json:"message"`
}

type RewindResponse struct {
	Session string `json:"session"`
}

type SetModelRequest struct {
	Session string   `json:"session"`
	Model   ModelRef `json:"model"`
}

type FilesFindRequest struct {
	Directory string `json:"directory,omitempty"`
	Query     string `json:"query,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type FileEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir,omitempty"`
}

type FilesReadRequest struct {
	Directory string `json:"directory,omitempty"`
	Path      string `json:"path"`
}

type FilesReadResponse struct {
	Data []byte `json:"data"`
}

type FitImageRequest struct {
	Model ModelRef `json:"model"`
	File  File     `json:"file"`
}

type FitImageResponse struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ProviderInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Source         string `json:"source,omitempty"`
	SupportsAPIKey bool   `json:"supportsApiKey,omitempty"`
	SupportsOAuth  bool   `json:"supportsOAuth,omitempty"`
	Connected      bool   `json:"connected,omitempty"`
}

type ProviderSelectOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ProviderAuthEvent struct {
	LoginID    string                 `json:"loginId"`
	ProviderID string                 `json:"providerId"`
	Kind       ProviderAuthEventKind  `json:"kind"`
	Message    string                 `json:"message,omitempty"`
	URL        string                 `json:"url,omitempty"`
	UserCode   string                 `json:"userCode,omitempty"`
	Options    []ProviderSelectOption `json:"options,omitempty"`
}

type ProviderLoginStartRequest struct {
	LoginID    string           `json:"loginId,omitempty"`
	ProviderID string           `json:"providerId"`
	AuthType   ProviderAuthType `json:"authType"`
}

type ProviderLoginStartResponse struct {
	LoginID string `json:"loginId"`
}

type ProviderLoginAnswerRequest struct {
	LoginID string `json:"loginId"`
	Value   string `json:"value"`
}

type ProviderLoginCancelRequest struct {
	LoginID string `json:"loginId"`
}

type ProviderLogoutRequest struct {
	ProviderID string `json:"providerId"`
}

type HarnessInfo struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Kind        HarnessKind `json:"kind"`
	Binary      string      `json:"binary,omitempty"`
	Available   bool        `json:"available"`
	Description string      `json:"description,omitempty"`
}

type WorkerInfo struct {
	ID            string       `json:"id"`
	ParentSession string       `json:"parentSession,omitempty"`
	Provider      string       `json:"provider"`
	Name          string       `json:"name"`
	Directory     string       `json:"directory"`
	Branch        string       `json:"branch,omitempty"`
	WorktreePath  string       `json:"worktreePath,omitempty"`
	Mode          string       `json:"mode,omitempty"`
	Status        WorkerStatus `json:"status"`
	LastError     string       `json:"lastError,omitempty"`
	Updated       int64        `json:"updated"`
}

type WorkersListRequest struct {
	ParentSession string `json:"parentSession,omitempty"`
}

type PRStatus struct {
	Number         int    `json:"number"`
	Title          string `json:"title,omitempty"`
	State          string `json:"state"`
	Draft          bool   `json:"draft,omitempty"`
	URL            string `json:"url"`
	Checks         string `json:"checks,omitempty"` // pass | fail | pending | none
	ReviewDecision string `json:"reviewDecision,omitempty"`
}

type WorktreeInfo struct {
	RepoRoot   string    `json:"repoRoot"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch,omitempty"`
	HeadCommit string    `json:"headCommit,omitempty"`
	IsMain     bool      `json:"isMain,omitempty"`
	Dirty      bool      `json:"dirty,omitempty"`
	PR         *PRStatus `json:"pr,omitempty"`
}

type WorktreePRRequest struct {
	Directory string `json:"directory"`
}

type WorktreeCreatePRRequest struct {
	Directory string `json:"directory"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Base      string `json:"base,omitempty"`
	Draft     bool   `json:"draft,omitempty"`
}

type TerminalInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Directory string `json:"directory"`
	Workspace string `json:"workspace,omitempty"` // the active workspace at Directory, if any
	Shell     string `json:"shell,omitempty"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	PID       int    `json:"pid,omitempty"`
	Running   bool   `json:"running"`
	ExitCode  int    `json:"exitCode,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	Command   string `json:"command,omitempty"` // what the shell runs instead of being interactive
	Role      string `json:"role,omitempty"`    // what made it: "setup", or "service:<name>"
}

type TerminalCreateRequest struct {
	Name      string `json:"name,omitempty"`
	Directory string `json:"directory,omitempty"`
	Workspace string `json:"workspace,omitempty"` // instead of Directory: an active workspace's
	Shell     string `json:"shell,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	// Command, when set, is run by the shell (as a login shell where it can
	// be) instead of an interactive session; the terminal exits with it.
	Command string   `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"` // KEY=value added to the daemon's environment
	Role    string   `json:"role,omitempty"`
}

type TerminalAttachRequest struct {
	ID   string `json:"id"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type TerminalAttachResponse struct {
	Channel  uint8        `json:"channel"`
	Info     TerminalInfo `json:"info"`
	Snapshot []byte       `json:"snapshot,omitempty"`
}

type TerminalDetachRequest struct {
	ID string `json:"id"`
}

type TerminalInputRequest struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}

type TerminalResizeRequest struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type TerminalCaptureRequest struct {
	ID    string `json:"id"`
	Lines int    `json:"lines,omitempty"`
}

type TerminalCaptureResponse struct {
	Text     string `json:"text"`
	Snapshot []byte `json:"snapshot,omitempty"`
}

type TerminalKillRequest struct {
	ID string `json:"id"`
}

// ProjectInfo is a folder the user works in. ID is opaque, never a path.
type ProjectInfo struct {
	ID       string `json:"id"`
	Root     string `json:"root"`            // absolute and clean, as added
	Name     string `json:"name"`            // the user's name, else Root's base name
	Inbox    bool   `json:"inbox,omitempty"` // the project of conversations started without one
	Created  int64  `json:"created"`
	Archived int64  `json:"archived,omitempty"` // Unix ms; 0 while active
}

// WorkspaceInfo is one working directory of a project. No two active
// workspaces share a Directory.
type WorkspaceInfo struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	Isolation    Isolation `json:"isolation"`
	Directory    string    `json:"directory"`              // where agents and terminals run
	WorktreeRoot string    `json:"worktreeRoot,omitempty"` // the checkout Directory is in, for a worktree
	Owned        bool      `json:"owned,omitempty"`        // wadad made WorktreeRoot and removes it
	Branch       string    `json:"branch,omitempty"`
	BaseRef      string    `json:"baseRef,omitempty"` // what its changes compare against
	PR           int       `json:"pr,omitempty"`      // the pull request it checks out
	Title        string    `json:"title,omitempty"`   // the user's title
	Created      int64     `json:"created"`
	Archived     int64     `json:"archived,omitempty"`
}

// WorkspaceCreateRequest makes a worktree workspace in a project.
type WorkspaceCreateRequest struct {
	IdempotencyKey string    `json:"idempotencyKey,omitempty"`
	Project        string    `json:"project"`
	Isolation      Isolation `json:"isolation"`       // worktree or existing
	Mode           StartMode `json:"mode,omitempty"`  // worktree: default branch-off
	From           string    `json:"from,omitempty"`  // branch-off: the ref; checkout-branch: the branch
	PR             int       `json:"pr,omitempty"`    // checkout-pr
	Path           string    `json:"path,omitempty"`  // existing: the worktree's directory
	Slug           string    `json:"slug,omitempty"`  // worktree: names its directory and new branch
	Title          string    `json:"title,omitempty"` // the user's title
}

type WorkspaceArchiveRequest struct {
	Workspace string `json:"workspace"`
	// Force discards a wadad-made worktree's uncommitted changes and
	// unpushed commits; without it such a worktree is refused (conflict).
	Force bool `json:"force,omitempty"`
}

type WorkspaceIDRequest struct {
	Workspace string `json:"workspace"`
}

type WorkspaceRefsRequest struct {
	Project string `json:"project"`
}

// PullRequestRef is an open pull request a workspace can check out.
type PullRequestRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
	Fork   bool   `json:"fork,omitempty"`
}

// WorkspaceRefs is what a project's repository offers a new workspace.
type WorkspaceRefs struct {
	Default   string           `json:"default,omitempty"` // the ref to branch off by default
	Branches  []string         `json:"branches,omitempty"`
	Remotes   []string         `json:"remotes,omitempty"` // remote-tracking branches, such as origin/main
	PRs       []PullRequestRef `json:"prs,omitempty"`
	Worktrees []WorktreeInfo   `json:"worktrees,omitempty"`
}

// WorkspaceConfig is a workspace's wada.json as read now, with what runs.
type WorkspaceConfig struct {
	Found    bool     `json:"found"`
	Hash     string   `json:"hash,omitempty"`    // of the file; trusting names it
	Trusted  bool     `json:"trusted,omitempty"` // its project's user allowed this file
	Error    string   `json:"error,omitempty"`   // the file didn't parse
	Setup    []string `json:"setup,omitempty"`
	Teardown []string `json:"teardown,omitempty"` // runs before wadad removes the worktree
	// SetupTerminal is the last setup started in the workspace, if any.
	SetupTerminal *TerminalInfo   `json:"setupTerminal,omitempty"`
	Services      []ServiceStatus `json:"services,omitempty"`
}

// ServiceStatus is a service of a wada.json, its port and its terminal.
type ServiceStatus struct {
	Name     string        `json:"name"`
	Command  string        `json:"command"`
	Port     int           `json:"port"`
	Terminal *TerminalInfo `json:"terminal,omitempty"` // the last one started; Running says whether it is up
}

type WorkspaceTrustRequest struct {
	Workspace string `json:"workspace"`
	Hash      string `json:"hash"` // the WorkspaceConfig.Hash the user saw
}

type WorkspaceServiceRequest struct {
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
}

type ProjectsListRequest struct {
	Archived bool `json:"archived,omitempty"` // include archived projects
}

type ProjectAddRequest struct {
	Root string `json:"root"`
}

// ProjectAddResponse is the project and its project-directory workspace.
type ProjectAddResponse struct {
	Project   ProjectInfo   `json:"project"`
	Workspace WorkspaceInfo `json:"workspace"`
}

type ProjectRenameRequest struct {
	Project string `json:"project"`
	Name    string `json:"name"` // empty for the root's base name
}

type ProjectArchiveRequest struct {
	Project string `json:"project"`
}

type WorkspacesListRequest struct {
	Project  string `json:"project,omitempty"`  // empty for every project's
	Archived bool   `json:"archived,omitempty"` // include archived workspaces
}

type WorkspaceRenameRequest struct {
	Workspace string `json:"workspace"`
	Title     string `json:"title"`
}

type PairingOfferRequest struct {
	AppURL string `json:"appUrl,omitempty"`
}

type PairingOfferResponse struct {
	Offer            string `json:"offer"`
	URL              string `json:"url"`
	RelayURL         string `json:"relayUrl,omitempty"`
	DirectURL        string `json:"directUrl,omitempty"`
	ServerID         string `json:"serverId"`
	ServerPubKey     string `json:"serverPubKey"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
}

type SetPasswordRequest struct {
	Password string `json:"password,omitempty"`
}

type SkillsListRequest struct {
	Directory string `json:"directory,omitempty"`
}

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type FileChange struct {
	Path    string     `json:"path"`
	Status  FileStatus `json:"status"`
	Added   int        `json:"added,omitempty"`
	Removed int        `json:"removed,omitempty"`
	Patch   string     `json:"patch,omitempty"`
}

type Tokens struct {
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}

type Tool struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      ToolKind `json:"kind"`
	Command   string   `json:"command,omitempty"`
	Output    []string `json:"output,omitempty"`
	Status    string   `json:"status,omitempty"`
	Exit      int      `json:"exit,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

type Part struct {
	Kind PartKind `json:"kind"`
	Text string   `json:"text,omitempty"`
	Tool *Tool    `json:"tool,omitempty"`
}

type Message struct {
	ID     string   `json:"id"`
	Role   Role     `json:"role"`
	Text   string   `json:"text,omitempty"`
	Files  []File   `json:"files,omitempty"`
	Parts  []Part   `json:"parts,omitempty"`
	Model  ModelRef `json:"model"`
	Cost   float64  `json:"cost,omitempty"`
	Tokens Tokens   `json:"tokens"`
}

type TextEvent struct {
	Message string `json:"message"`
	Ordinal int    `json:"ordinal,omitempty"`
	Delta   string `json:"delta,omitempty"`
	Text    string `json:"text,omitempty"`
}

type Approval struct {
	Session   string   `json:"session"`
	ID        string   `json:"id"`
	Kind      ToolKind `json:"kind"`
	Action    string   `json:"action,omitempty"`
	Resources []string `json:"resources,omitempty"`
	Save      []string `json:"save,omitempty"`
}

// PendingApproval is an approval still waiting for its reply. ShownIn is the
// conversation that presents it: the approval's own session, or the parent
// of the worker that asked. Replies go to Approval.Session.
type PendingApproval struct {
	ShownIn  string   `json:"shownIn"`
	Approval Approval `json:"approval"`
}

type QuestionOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	Key         string           `json:"key"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
	Multiple    bool             `json:"multiple,omitempty"`
	Custom      bool             `json:"custom,omitempty"`
}

type Interaction struct {
	Session     string          `json:"session"`
	Questions   []Question      `json:"questions,omitempty"`
	Settled     bool            `json:"settled,omitempty"`
	ID          string          `json:"id"`
	Kind        InteractionKind `json:"kind"`
	Title       string          `json:"title"`
	Message     string          `json:"message,omitempty"`
	Options     []string        `json:"options,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
	Prefill     string          `json:"prefill,omitempty"`
}

type InteractionAnswer struct {
	Answers   map[string][]string `json:"answers,omitempty"`
	Session   string              `json:"session"`
	ID        string              `json:"id"`
	Cancelled bool                `json:"cancelled,omitempty"`
	Confirmed bool                `json:"confirmed,omitempty"`
	Value     string              `json:"value,omitempty"`
}

type Event struct {
	Epoch        uint64             `json:"epoch,omitempty"`
	Seq          uint64             `json:"seq,omitempty"`
	Kind         EventKind          `json:"kind"`
	Session      string             `json:"session,omitempty"`
	Text         *TextEvent         `json:"text,omitempty"`
	Tool         *Tool              `json:"tool,omitempty"`
	Approval     *Approval          `json:"approval,omitempty"`
	Interaction  *Interaction       `json:"interaction,omitempty"`
	ProviderAuth *ProviderAuthEvent `json:"providerAuth,omitempty"`
	Worker       *WorkerInfo        `json:"worker,omitempty"`
	Terminal     *TerminalInfo      `json:"terminal,omitempty"`
	Project      *ProjectInfo       `json:"project,omitempty"`
	Workspace    *WorkspaceInfo     `json:"workspace,omitempty"`
	Model        ModelRef           `json:"model"`
	Title        string             `json:"title,omitempty"`
	Error        string             `json:"error,omitempty"`
}

type SubscribeRequest struct {
	// SubscriptionID is optional and chosen by the client, so it can route
	// events that arrive before the response. Empty asks the server for one.
	SubscriptionID string   `json:"subscriptionId,omitempty"`
	Sessions       []string `json:"sessions,omitempty"` // empty subscribes to all sessions
}

type SubscribeResponse struct {
	SubscriptionID string `json:"subscriptionId"`
}

type UnsubscribeRequest struct {
	SubscriptionID string `json:"subscriptionId"`
}

type TimelineFetchRequest struct {
	Session  string `json:"session"`
	Epoch    uint64 `json:"epoch,omitempty"`
	AfterSeq uint64 `json:"afterSeq,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type TimelineFetchResponse struct {
	Epoch   uint64  `json:"epoch"`
	Events  []Event `json:"events"`
	HasMore bool    `json:"hasMore,omitempty"`
}
