package runner

import "wada-desktop-go/internal/opencode"

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
	Session, Text, Directory string
	Draft                    uint64
	Model                    opencode.ModelRef
}

func (Prompt) kind() Kind { return KindPrompt }

type Open struct {
	Session  string
	Revision uint64
}

func (Open) kind() Kind { return KindOpen }

type Stop struct{ Session string }

func (Stop) kind() Kind { return KindStop }

type Answer struct {
	Session, Request string
	Decision         Decision
}

func (Answer) kind() Kind { return KindAnswer }

type Rename struct{ Session, Title string }

func (Rename) kind() Kind { return KindRename }

type Delete struct{ Session string }

func (Delete) kind() Kind { return KindDelete }

type SelectModel struct {
	Session string
	Model   opencode.ModelRef
}

func (SelectModel) kind() Kind { return KindSelectModel }

type More struct{ Cursor string }

func (More) kind() Kind { return KindMore }

type Refresh struct{}

func (Refresh) kind() Kind { return KindRefresh }

type Usage struct{ Session, Message string }

func (Usage) kind() Kind { return KindUsage }

func valid(command Command) bool {
	if command == nil {
		return false
	}
	switch command.kind() {
	case KindPrompt:
		c := command.(Prompt)
		return c.Text != ""
	case KindOpen:
		return command.(Open).Session != ""
	case KindStop:
		return command.(Stop).Session != ""
	case KindAnswer:
		c := command.(Answer)
		return c.Session != "" && c.Request != "" && (c.Decision == Once || c.Decision == Always || c.Decision == Reject)
	case KindRename:
		c := command.(Rename)
		return c.Session != "" && c.Title != ""
	case KindDelete:
		return command.(Delete).Session != ""
	case KindSelectModel:
		c := command.(SelectModel)
		return c.Session != "" && c.Model.ID != "" && c.Model.ProviderID != ""
	case KindMore:
		return command.(More).Cursor != ""
	case KindRefresh:
		return true
	case KindUsage:
		c := command.(Usage)
		return c.Session != "" && c.Message != ""
	}
	return false
}
