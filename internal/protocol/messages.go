package protocol

import "encoding/json"

// Dotted RPC method names.
const (
	MethodDaemonStatus   = "daemon.status"
	MethodDaemonShutdown = "daemon.shutdown"

	MethodSessionsList   = "agent.sessions.list"
	MethodSessionCreate  = "agent.session.create"
	MethodSessionMessages = "agent.session.messages"
	MethodSessionSend    = "agent.session.send"
	MethodSessionAbort   = "agent.session.abort"
	MethodSessionReply   = "agent.session.reply"
	MethodSessionRename  = "agent.session.rename"
	MethodSessionDelete  = "agent.session.delete"
	MethodSessionSpawn   = "agent.session.spawn"
	MethodSessionRewind  = "agent.session.rewind"
	MethodSessionChanges = "agent.session.changes"
	MethodSessionSnapshot = "agent.session.snapshot"
	MethodSessionDiff    = "agent.session.diff"

	MethodModelsList     = "agent.models.list"
	MethodModelsDefault  = "agent.models.default"
	MethodSessionSetModel = "agent.session.set_model"
	MethodModesList      = "agent.modes.list"

	MethodFilesFind  = "agent.files.find"
	MethodFilesRead  = "agent.files.read"
	MethodImagesFit  = "agent.images.fit"
	MethodSkillsList = "agent.skills.list"

	MethodProvidersList       = "agent.providers.list"
	MethodProviderLoginStart  = "agent.provider.login.start"
	MethodProviderLoginAnswer = "agent.provider.login.answer"
	MethodProviderLoginCancel = "agent.provider.login.cancel"
	MethodProviderLogout      = "agent.provider.logout"

	MethodInteractionsPending = "agent.interactions.pending"
	MethodInteractionsAnswer  = "agent.interactions.answer"

	MethodTimelineFetch     = "agent.timeline.fetch"
	MethodEventsSubscribe   = "events.subscribe"
	MethodEventsUnsubscribe = "events.unsubscribe"
)

// Stable wire string enums.
type (
	SendDisposition string
	Decision        string
	Role            string
	PartKind        string
	ToolKind        string
	FileStatus      string
	EventKind       string
	InteractionKind string
	ProviderAuthType      string
	ProviderAuthEventKind string
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
	Updated   int64    `json:"updated"`
	Model     ModelRef `json:"model"`
	Cost      float64  `json:"cost,omitempty"`
}

type SessionQuery struct {
	Cursor    string `json:"cursor,omitempty"`
	Search    string `json:"search,omitempty"`
	Directory string `json:"directory,omitempty"`
}

type SessionPage struct {
	Sessions []Session `json:"sessions"`
	Next     string    `json:"next,omitempty"`
}

type CreateSessionRequest struct {
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
	Title          string   `json:"title,omitempty"`
	Directory      string   `json:"directory,omitempty"`
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

type ProjectScope struct {
	Kind      string `json:"kind"`
	Directory string `json:"directory"`
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
	ID           string          `json:"id,omitempty"`
	MessageID    string          `json:"messageId,omitempty"`
	Session      string          `json:"session"`
	Mode         string          `json:"mode,omitempty"`
	Text         string          `json:"text"`
	Instructions string          `json:"instructions,omitempty"`
	Policy       *ToolPolicy     `json:"policy,omitempty"`
	Project      *ProjectScope   `json:"project,omitempty"`
	ResultSchema json.RawMessage `json:"resultSchema,omitempty"`
	Internal     bool            `json:"internal,omitempty"`
	Files        []File          `json:"files,omitempty"`
	Skills       []SkillRef      `json:"skills,omitempty"`
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

type SnapshotResponse struct {
	Files map[string][]byte `json:"files"`
}

type DiffRequest struct {
	Session string            `json:"session"`
	Before  map[string][]byte `json:"before"`
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
	Epoch       uint64          `json:"epoch,omitempty"`
	Seq         uint64          `json:"seq,omitempty"`
	RequestID   string          `json:"requestId,omitempty"`
	Kind        EventKind       `json:"kind"`
	Session     string          `json:"session,omitempty"`
	Text        *TextEvent      `json:"text,omitempty"`
	Tool        *Tool           `json:"tool,omitempty"`
	Approval     *Approval          `json:"approval,omitempty"`
	Interaction  *Interaction       `json:"interaction,omitempty"`
	ProviderAuth *ProviderAuthEvent `json:"providerAuth,omitempty"`
	Model        ModelRef           `json:"model"`
	Title       string          `json:"title,omitempty"`
	Error       string          `json:"error,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type SubscribeRequest struct {
	Sessions []string `json:"sessions,omitempty"` // empty subscribes to all sessions
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
