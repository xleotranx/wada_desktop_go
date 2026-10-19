package runner

import (
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
)

type Generation uint64
type Admission uint8

const (
	Accepted Admission = iota
	Busy
	Closed
	Invalid
)

type Decision string

const (
	Once   Decision = "once"
	Always Decision = "always"
	Reject Decision = "reject"
)

type Kind uint8

const (
	KindPrompt      Kind = 0
	KindOpen        Kind = 1
	KindStop        Kind = 2
	KindAnswer      Kind = 3
	KindRename      Kind = 4
	KindDelete      Kind = 5
	KindSelectModel Kind = 6
	KindMore        Kind = 7
	KindRefresh     Kind = 8
	KindUsage       Kind = 9
)

// Command is closed to this package. Each operation carries only its payload.
type Command interface{ kind() Kind }
type Prompt struct {
	Session         chat.SessionID
	Text, Directory string
	// Agent names the persona a conversation is born under ("plan"
	// answers read-only). The server folds it in at creation only; it is
	// dropped by the transport when the chat already stands.
	Agent string
	Draft uint64
	Model opencode.ModelRef
}

func (Prompt) kind() Kind { return KindPrompt }

type Open struct {
	Session  chat.SessionID
	Revision uint64
}

func (Open) kind() Kind { return KindOpen }

type Stop struct{ Session chat.SessionID }

func (Stop) kind() Kind { return KindStop }

type Answer struct {
	Session  chat.SessionID
	Request  chat.PermissionID
	Decision Decision
}

func (Answer) kind() Kind { return KindAnswer }

type Rename struct {
	Session chat.SessionID
	Title   string
}

func (Rename) kind() Kind { return KindRename }

type Delete struct{ Session chat.SessionID }

func (Delete) kind() Kind { return KindDelete }

type SelectModel struct {
	Session chat.SessionID
	Model   opencode.ModelRef
}

func (SelectModel) kind() Kind { return KindSelectModel }

type More struct{ Cursor string }

func (More) kind() Kind { return KindMore }

type Refresh struct{}

func (Refresh) kind() Kind { return KindRefresh }

type Usage struct {
	Session chat.SessionID
	Message chat.MessageID
}

func (Usage) kind() Kind { return KindUsage }

func valid(command Command) bool {
	switch c := command.(type) {
	case Prompt:
		return c.Text != ""
	case Open:
		return c.Session != ""
	case Stop:
		return c.Session != ""
	case Answer:
		return c.Session != "" && c.Request != "" && (c.Decision == Once || c.Decision == Always || c.Decision == Reject)
	case Rename:
		return c.Session != "" && c.Title != ""
	case Delete:
		return c.Session != ""
	case SelectModel:
		return c.Session != "" && c.Model.ID != "" && c.Model.ProviderID != ""
	case More:
		return c.Cursor != ""
	case Refresh:
		return true
	case Usage:
		return c.Session != "" && c.Message != ""
	default:
		return false
	}
}

func mutation(command Command) bool {
	switch command.kind() {
	case KindPrompt, KindAnswer, KindRename, KindDelete, KindSelectModel, KindStop:
		return true
	case KindOpen, KindMore, KindRefresh, KindUsage:
		return false
	}
	return false
}
func commandOrigin(command Command) Origin {
	switch c := command.(type) {
	case Prompt:
		return Origin{Session: c.Session, Draft: c.Draft, IsDraft: c.Session == ""}
	case Open:
		return Origin{Session: c.Session}
	case Stop:
		return Origin{Session: c.Session}
	case Answer:
		return Origin{Session: c.Session}
	case Rename:
		return Origin{Session: c.Session}
	case Delete:
		return Origin{Session: c.Session}
	case SelectModel:
		return Origin{Session: c.Session}
	case Usage:
		return Origin{Session: c.Session}
	default:
		return Origin{}
	}
}
