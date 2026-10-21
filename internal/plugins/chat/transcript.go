package chat

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
)

type rowKind uint8

const (
	rowUser        rowKind = iota // the user's words
	rowText                       // a finished answer, as markdown
	rowStream                     // an answer still arriving
	rowTool                       // one tool call
	rowApproval                   // a permission waiting on the user
	rowInteraction                // a general question waiting on the user
	rowNote                       // a faint note from the app
)

// row is one transcript entry. Rows are owned by the transcript and read on
// the UI thread.
type row struct {
	id          uint64
	kind        rowKind
	text        string
	files       []agent.File // rowUser: what went with the words
	stream      *strings.Builder
	message     agent.MessageID
	tool        *toolRow
	approval    *agent.Approval
	interaction *interactionState
}

type interactionState struct {
	request    agent.Interaction
	value      string
	submitting bool
}

type toolRow struct {
	label   string
	kind    agent.ToolKind
	command string
	detail  []string
}

type textKey struct {
	message agent.MessageID
	ordinal int
}

// transcript is the open conversation's rows, built from stored messages and
// then from live events. It is pure state: no I/O, no UI.
type transcript struct {
	rows      []*row
	nextID    uint64
	live      map[textKey]*row
	tools     map[string]*row
	started   map[string]agent.Tool // what ToolStarted said of a call, by ID
	busy      bool
}

func (t *transcript) reset() {
	*t = transcript{nextID: t.nextID, live: map[textKey]*row{}, tools: map[string]*row{}, started: map[string]agent.Tool{}}
}

func (t *transcript) add(r *row) *row {
	t.nextID++
	r.id = t.nextID
	t.rows = append(t.rows, r)
	return r
}

func (t *transcript) addUser(text string, files []agent.File) {
	t.add(&row{kind: rowUser, text: text, files: files})
}

func (t *transcript) note(text string) { t.add(&row{kind: rowNote, text: text}) }

// lastUser is the index of the last user row, or -1.
func (t *transcript) lastUser() int {
	for i := len(t.rows) - 1; i >= 0; i-- {
		if t.rows[i].kind == rowUser {
			return i
		}
	}
	return -1
}

// dropLastTurn removes the last user row and everything after it.
func (t *transcript) dropLastTurn() {
	if i := t.lastUser(); i >= 0 {
		t.rows = t.rows[:i]
	}
}

// load replaces the rows with a stored conversation.
func (t *transcript) load(messages []agent.Message) {
	t.reset()
	for _, m := range messages {
		switch m.Role {
		case agent.RoleUser:
			if text := strings.TrimSpace(m.Text); text != "" || len(m.Files) > 0 {
				t.addUser(text, m.Files)
			}
		case agent.RoleAssistant:
			for _, part := range m.Parts {
				switch part.Kind {
				case agent.PartText:
					if text := strings.TrimSpace(part.Text); text != "" {
						t.add(&row{kind: rowText, text: text, message: m.ID})
					}
				case agent.PartTool:
					if part.Tool != nil {
						t.add(&row{kind: rowTool, tool: newToolRow(*part.Tool)})
					}
				case agent.PartReasoning, agent.PartOther:
				}
			}
		case agent.RoleOther:
		}
	}
}

// apply folds one event of this conversation into the rows.
func (t *transcript) apply(ev agent.Event) {
	switch ev.Kind {
	case agent.TextStarted:
		t.liveText(ev.Text.Message, ev.Text.Ordinal)
	case agent.TextDelta:
		r := t.liveText(ev.Text.Message, ev.Text.Ordinal)
		if r.stream == nil {
			r.stream = new(strings.Builder)
			r.stream.WriteString(r.text)
		}
		r.stream.WriteString(ev.Text.Delta)
		r.text = r.stream.String()
	case agent.TextEnded:
		r := t.liveText(ev.Text.Message, ev.Text.Ordinal)
		delete(t.live, textKey{ev.Text.Message, ev.Text.Ordinal})
		r.kind, r.text, r.stream = rowText, ev.Text.Text, nil
	case agent.ToolStarted:
		t.started[ev.Tool.ID] = *ev.Tool
	case agent.ToolCalled:
		t.toolCall(*ev.Tool)
	case agent.ToolFinished:
		r := t.toolCall(*ev.Tool)
		done := t.known(*ev.Tool)
		done.Command = cmp.Or(done.Command, r.tool.command)
		r.tool = newToolRow(done)
		delete(t.tools, ev.Tool.ID)
		delete(t.started, ev.Tool.ID)
	case agent.ApprovalRequested:
		if !slices.ContainsFunc(t.rows, func(r *row) bool { return r.approval != nil && r.approval.ID == ev.Approval.ID }) {
			a := *ev.Approval
			t.add(&row{kind: rowApproval, approval: &a})
		}
	case agent.InteractionRequested:
		if !slices.ContainsFunc(t.rows, func(r *row) bool { return r.interaction != nil && r.interaction.request.ID == ev.Interaction.ID }) {
			t.add(&row{kind: rowInteraction, interaction: &interactionState{request: *ev.Interaction, value: ev.Interaction.Prefill}})
		}
	case agent.TurnStarted:
		t.busy = true
	case agent.TurnSucceeded, agent.TurnInterrupted:
		t.settle()
	case agent.TurnFailed:
		t.settle()
		if ev.Error != "" {
			t.note("The agent stopped with an error: " + ev.Error)
		} else {
			t.note("The agent stopped with an error.")
		}
	case agent.ModelChanged, agent.SessionRenamed:
	}
}

func (t *transcript) liveText(message agent.MessageID, ordinal int) *row {
	key := textKey{message, ordinal}
	if r := t.live[key]; r != nil {
		return r
	}
	r := t.add(&row{kind: rowStream, message: message})
	t.live[key] = r
	return r
}

func (t *transcript) toolCall(tool agent.Tool) *row {
	if r := t.tools[tool.ID]; r != nil {
		return r
	}
	r := t.add(&row{kind: rowTool, tool: newToolRow(t.known(tool))})
	t.tools[tool.ID] = r
	return r
}

// settle ends the turn: answers still streaming stand as they are, and
// approvals it was waiting on no longer apply.
func (t *transcript) settle() {
	for _, r := range t.live {
		r.kind, r.stream = rowText, nil
		if strings.TrimSpace(r.text) == "" {
			r.text = "(the answer was cut short)"
		}
	}
	t.rows = slices.DeleteFunc(t.rows, func(r *row) bool { return r.kind == rowApproval || r.kind == rowInteraction })
	clear(t.live)
	clear(t.tools)
	clear(t.started)
	t.busy = false
}

func (t *transcript) removeApproval(id agent.ApprovalID) {
	t.rows = slices.DeleteFunc(t.rows, func(r *row) bool { return r.approval != nil && r.approval.ID == id })
}

// streaming reports whether words are arriving right now.
func (t *transcript) streaming() bool {
	for _, r := range t.live {
		if strings.TrimSpace(r.text) != "" {
			return true
		}
	}
	return false
}

func (t *transcript) waitingApproval() bool {
	return slices.ContainsFunc(t.rows, func(r *row) bool { return r.kind == rowApproval || r.kind == rowInteraction })
}

// known fills what a later event of a call leaves out from its ToolStarted.
func (t *transcript) known(tool agent.Tool) agent.Tool {
	start := t.started[tool.ID]
	tool.Name = cmp.Or(tool.Name, start.Name)
	tool.Kind = cmp.Or(tool.Kind, start.Kind)
	return tool
}

// newToolRow labels a tool call and cuts its detail: the command, then the
// last lines of output, the exit status and a truncation mark.
func newToolRow(tool agent.Tool) *toolRow {
	r := &toolRow{label: toolLabel(tool), kind: tool.Kind, command: tool.Command}
	if tool.Command != "" {
		r.detail = append(r.detail, "$ "+tool.Command)
	}
	var out []string
	for _, piece := range tool.Output {
		out = append(out, strings.Split(strings.TrimRight(piece, "\n"), "\n")...)
	}
	if len(out) > 6 {
		out = out[len(out)-6:]
	}
	for _, line := range out {
		r.detail = append(r.detail, strings.Clone(line))
	}
	if tool.Exit != 0 {
		r.detail = append(r.detail, fmt.Sprintf("exit %d", tool.Exit))
	}
	if tool.Truncated {
		r.detail = append(r.detail, "…")
	}
	return r
}

// toolLabel says what a call did, from its kind; only an unknown kind needs
// the backend's name for it.
func toolLabel(tool agent.Tool) string {
	switch tool.Kind {
	case agent.ToolShell:
		return "Ran command"
	case agent.ToolRead:
		return "Read files"
	case agent.ToolSearch:
		return "Searched"
	case agent.ToolEdit:
		return "Edited files"
	case agent.ToolFetch:
		return "Fetched from the web"
	case agent.ToolOther:
	}
	if tool.Name == "" {
		return "Used tool"
	}
	return "Used " + tool.Name
}
