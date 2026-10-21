package agent

type EventKind string

const (
	TextStarted          EventKind = "text.started"
	TextDelta            EventKind = "text.delta"
	TextEnded            EventKind = "text.ended"
	ToolStarted          EventKind = "tool.started"
	ToolCalled           EventKind = "tool.called"
	ToolSucceeded        EventKind = "tool.succeeded"
	PermissionAsked      EventKind = "approval.requested"
	ExecutionStarted     EventKind = "turn.started"
	ExecutionSucceeded   EventKind = "turn.succeeded"
	ExecutionFailed      EventKind = "turn.failed"
	ExecutionInterrupted EventKind = "turn.interrupted"
	ModelSelected        EventKind = "model.selected"
	SessionRenamed       EventKind = "conversation.renamed"
)

type TextData struct {
	SessionID, MessageID string
	Ordinal              int
	Delta, Text          string
}
type ToolData struct {
	SessionID, ID, Name, Command string
	Content                      []ToolOutput
	Status                       string
	Exit                         int
	Truncated                    bool
}
type PermissionData struct {
	SessionID, ID, Action string
	Resources, Save       []string
}

// Update is an immutable publication. Payloads are populated only for their kind.
type Update struct {
	Kind       EventKind
	SessionID  string
	Text       *TextData
	Tool       *ToolData
	Permission *PermissionData
	Model      ModelRef
	Title      string
	Bytes      int
}
