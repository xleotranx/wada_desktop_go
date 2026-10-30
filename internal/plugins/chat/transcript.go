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
	loadFailed  bool  // rowNote: a read of history failed; a later read replaces it
	reveal      *pace // an answer of the last turn with words still to show
}

type interactionState struct {
	request    agent.Interaction
	answers    map[string][]string
	custom     map[string]*string
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
	rows    []*row
	nextID  uint64
	live    map[textKey]*row
	tools   map[string]*row
	started map[string]agent.Tool // what ToolStarted said of a call, by ID
	busy    bool
	// read is the readSum of the read the rows were built from, until
	// anything changes them; loaded tells that a read built them once.
	read   uint64
	loaded bool
	folded folding
}

// folding keeps the items built from the rows until their shape changes.
type folding struct {
	shape []uint64 // each row's id and kind, as built from
	busy  bool
	items []item
}

func (t *transcript) reset() {
	*t = transcript{nextID: t.nextID, live: map[textKey]*row{}, tools: map[string]*row{}, started: map[string]agent.Tool{}}
}

func (t *transcript) add(r *row) *row {
	t.read = 0
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
		t.rows, t.read = t.rows[:i], 0
	}
}

// load replaces the rows with a stored conversation, which sum identifies.
func (t *transcript) load(messages []agent.Message, sum uint64) {
	t.reset()
	defer func() { t.read, t.loaded = sum, true }()
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
	t.read = 0
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
		if ev.Interaction.Settled {
			t.rows = slices.DeleteFunc(t.rows, func(r *row) bool { return r.interaction != nil && r.interaction.request.ID == ev.Interaction.ID })
			break
		}
		if !slices.ContainsFunc(t.rows, func(r *row) bool { return r.interaction != nil && r.interaction.request.ID == ev.Interaction.ID }) {
			state := &interactionState{request: *ev.Interaction, value: ev.Interaction.Prefill, custom: map[string]*string{}}
			for _, q := range ev.Interaction.Questions {
				state.custom[q.Key] = new(string)
			}
			t.add(&row{kind: rowInteraction, interaction: state})
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
	t.read = 0
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
	t.read = 0
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

// item is one entry of the list the page draws: a row, or the work of a
// finished turn folded under one header.
type item struct {
	row    *row
	work   []*row // the turn's tool calls and the words between them
	answer bool   // row is the final answer of a finished turn
	// key identifies the item across frames; a fold takes its first row's
	// id, negated so it never meets a row's.
	key any
}

// items folds each finished turn's work, from its first answer or tool call
// through its last tool call, under one entry, so the answer that follows
// stands alone as the answer. The turn in flight stays unfolded, so its
// steps show as they happen, and has no answer yet.
//
// Folding depends only on the rows' order, ids and kinds and on busy, so the
// items are built again only when those change; between changes, as while
// words stream into a row, a frame reuses them. Callers must not change the
// slice.
func (t *transcript) items() []item {
	f := &t.folded
	same := f.items != nil && f.busy == t.busy && len(f.shape) == len(t.rows)
	for i := 0; same && i < len(t.rows); i++ {
		same = f.shape[i] == shapeOf(t.rows[i])
	}
	if same {
		return f.items
	}
	f.shape, f.busy = f.shape[:0], t.busy
	for _, r := range t.rows {
		f.shape = append(f.shape, shapeOf(r))
	}
	f.items = t.fold()
	return f.items
}

// shapeOf is what folding reads of a row: its id and kind.
func shapeOf(r *row) uint64 { return r.id<<8 | uint64(r.kind) }

func (t *transcript) fold() []item {
	out := make([]item, 0, len(t.rows))
	for start := 0; start < len(t.rows); {
		end := start + 1
		for end < len(t.rows) && t.rows[end].kind != rowUser {
			end++
		}
		last := -1 // the turn's last tool call
		settled := !t.busy || end < len(t.rows)
		if settled {
			for i := end - 1; i >= start && last < 0; i-- {
				if t.rows[i].kind == rowTool {
					last = i
				}
			}
		}
		fold := -1 // where the turn's fold stands in out
		for i := start; i < end; i++ {
			r := t.rows[i]
			if i > last || (r.kind != rowTool && r.kind != rowText) {
				out = append(out, item{row: r, answer: settled && r.kind == rowText})
				continue
			}
			if fold < 0 {
				fold = len(out)
				out = append(out, item{})
			}
			out[fold].work = append(out[fold].work, r)
		}
		start = end
	}
	for i := range out {
		if out[i].work != nil {
			out[i].key = -int64(out[i].work[0].id)
		} else {
			out[i].key = out[i].row.id
		}
	}
	return out
}

// workLabel heads a fold: how many tool calls the turn made.
func workLabel(work []*row) string {
	calls := 0
	for _, r := range work {
		if r.kind == rowTool {
			calls++
		}
	}
	if calls == 1 {
		return "Worked · 1 step"
	}
	return fmt.Sprintf("Worked · %d steps", calls)
}
