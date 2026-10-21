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
	rowUser     rowKind = iota // the user's words
	rowText                    // a finished answer, as markdown
	rowStream                  // an answer still arriving
	rowTool                    // one tool call
	rowApproval                // a permission waiting on the user
	rowNote                    // a faint note from the app
)

// row is one transcript entry. Rows are owned by the transcript and read on
// the UI thread.
type row struct {
	id       uint64
	kind     rowKind
	text     string
	files    []agent.File // rowUser: what went with the words
	stream   *strings.Builder
	message  agent.MessageID
	tool     *toolRow
	approval *agent.Approval
}

type toolRow struct {
	label, icon string
	command     string
	detail      []string
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
	toolNames map[string]string
	busy      bool
}

func (t *transcript) reset() {
	*t = transcript{nextID: t.nextID, live: map[textKey]*row{}, tools: map[string]*row{}, toolNames: map[string]string{}}
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
						t.add(&row{kind: rowTool, tool: newToolRow(part.Tool.Name, *part.Tool)})
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
		t.toolNames[ev.Tool.ID] = ev.Tool.Name
	case agent.ToolCalled:
		t.toolCall(*ev.Tool)
	case agent.ToolFinished:
		r := t.toolCall(*ev.Tool)
		name := cmp.Or(ev.Tool.Name, t.toolNames[ev.Tool.ID])
		done := *ev.Tool
		done.Command = cmp.Or(done.Command, r.tool.command)
		r.tool = newToolRow(name, done)
		delete(t.tools, ev.Tool.ID)
		delete(t.toolNames, ev.Tool.ID)
	case agent.ApprovalRequested:
		if !slices.ContainsFunc(t.rows, func(r *row) bool { return r.approval != nil && r.approval.ID == ev.Approval.ID }) {
			a := *ev.Approval
			t.add(&row{kind: rowApproval, approval: &a})
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
	r := t.add(&row{kind: rowTool, tool: newToolRow(cmp.Or(tool.Name, t.toolNames[tool.ID]), tool)})
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
	t.rows = slices.DeleteFunc(t.rows, func(r *row) bool { return r.kind == rowApproval })
	clear(t.live)
	clear(t.tools)
	clear(t.toolNames)
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
	return slices.ContainsFunc(t.rows, func(r *row) bool { return r.kind == rowApproval })
}

// newToolRow labels a tool call and cuts its detail: the command, then the
// last lines of output, the exit status and a truncation mark.
func newToolRow(name string, tool agent.Tool) *toolRow {
	label, icon := toolLook(name)
	r := &toolRow{label: label, icon: icon, command: tool.Command}
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

func toolLook(name string) (label, icon string) {
	switch name {
	case "shell", "bash":
		return "Ran command", "shell"
	case "read":
		return "Read files", "read"
	case "grep", "glob", "list", "find":
		return "Searched", "read"
	case "":
		return "Used tool", "tool"
	}
	return "Used " + name, "tool"
}
