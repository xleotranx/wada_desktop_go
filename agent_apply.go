package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// toolRun is the shared outcome of one tool call, as both the live events
// and the stored parts report it.
type toolRun struct {
	command   string
	out       []string
	exit      int
	truncated bool
}

// toolDetailLines cuts a tool run into the lines of its detail box: what
// ran first, then the last of what it printed.
func toolDetailLines(run toolRun) []string {
	var lines []string
	if run.command != "" {
		lines = append(lines, "$ "+run.command)
	}
	out := run.out
	if len(out) > 6 {
		out = out[len(out)-6:]
	}
	// Split lines still point into the complete output. Keep only the
	// small strings the collapsed activity may eventually show.
	for _, line := range out {
		lines = append(lines, strings.Clone(line))
	}
	if run.exit != 0 {
		lines = append(lines, fmt.Sprintf("exit %d", run.exit))
	}
	if run.truncated {
		lines = append(lines, "…")
	}
	return lines
}

// restoreBlocks turns a session's stored messages into the transcript
// blocks the live stream would have drawn.
func restoreBlocks(messages []agentMessage) []block {
	var out []block
	for _, m := range messages {
		switch m.Type {
		case "user":
			t := m.Payload.Text
			if t == "" {
				t = m.Text
			}
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, block{kind: kindUser, text: t})
			}
		case "assistant":
			for i, part := range m.Content {
				switch part.Type {
				case "text":
					if t := strings.TrimSpace(part.Text); t != "" {
						out = append(out, block{kind: kindText, text: t})
					}
				case "tool":
					label, icon := toolLook(part.Name)
					id := part.ID
					if id == "" {
						id = fmt.Sprintf("part-%d", i)
					}
					var cmd struct {
						Command string `json:"command"`
					}
					json.Unmarshal(part.State.Input, &cmd)
					run := toolRun{command: cmd.Command,
						exit: part.State.Metadata.Exit, truncated: part.State.Metadata.Truncated}
					run.out = textOf(part.State.Content)
					out = append(out, block{kind: kindActivity, text: label, icon: icon,
						key: "activity:" + id, detail: commandDetail(toolDetailLines(run)...)})
				}
			}
		}
	}
	return out
}

// textOf joins the text pieces of tool output into lines.
func textOf(content []toolOut) []string {
	var out []string
	for _, o := range content {
		if o.Type == "text" {
			out = append(out, strings.Split(strings.TrimRight(o.Text, "\n"), "\n")...)
		}
	}
	return out
}

// finishCutTurns settles what still streamed when the pipe tears: the
// partial words stand as the answer they were, asks and spins release.
func (a *app) finishCutTurns() {
	for i := range a.blocks {
		if a.blocks[i].kind == kindStream {
			a.blocks[i].kind = kindText
			a.blocks[i].stream = nil
			if strings.TrimSpace(a.blocks[i].text) == "" {
				a.blocks[i].text = "(the answer was cut short)"
			}
		}
	}
	a.live = map[string]int{}
	a.busySession = ""
	a.pending = map[string]*agentPerm{}
}

// streamLine appends a faint note to the transcript for engine trouble
// worth showing in place.
func (a *app) streamLine(text string) {
	a.blocks = append(a.blocks, block{kind: kindNote, text: text})
	a.feed.ScrollToEnd()
}

// evTextData is the payload of the session.text.* events: one running text
// block inside an assistant message, addressed by message and ordinal.
type evTextData struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"assistantMessageID"`
	Ordinal   int    `json:"ordinal"`
	Delta     string `json:"delta"`
	Text      string `json:"text"`
}

// evToolData is the payload of the session.tool.* events: one tool call,
// addressed by its id, whose name, input and output fill in progressively.
type evToolData struct {
	SessionID string          `json:"sessionID"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   []toolOut       `json:"content"`
	Metadata  struct {
		Status    string `json:"status"`
		Truncated bool   `json:"truncated"`
		Exit      int    `json:"exit"`
	} `json:"metadata"`
}

// toolOut is one piece of a tool's output, of which text pieces render.
type toolOut struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// evPermissionData is the payload of permission.asked: one approval the
// agent needs before acting, with what it wants and what "always" saves.
type evPermissionData struct {
	SessionID string   `json:"sessionID"`
	ID        string   `json:"id"`
	Action    string   `json:"action"`
	Resources []string `json:"resources"`
	Save      []string `json:"save"`
}

// agentPerm is an approval request the agent raised mid-turn, held onto so
// its card's buttons know where to answer.
type agentPerm struct {
	id        string
	session   string
	action    string
	resources []string
	save      []string
}

// apply folds one agent event into the transcript. Kinds reuse the
// design's vocabulary: assistant words stream into the answer as it
// lands, and each tool call is an activity row that gains its detail
// once something ran.
func (a *app) apply(ev agentEvent) {
	switch ev.Type {
	case "session.text.started":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		key := d.MessageID + ":" + strconv.Itoa(d.Ordinal)
		a.live[key] = len(a.blocks)
		a.blocks = append(a.blocks, block{kind: kindStream, key: "stream:" + key})
	case "session.text.delta":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if idx, ok := a.live[d.MessageID+":"+strconv.Itoa(d.Ordinal)]; ok {
			b := &a.blocks[idx]
			if b.stream == nil {
				b.stream = &strings.Builder{}
				b.stream.WriteString(b.text)
			}
			b.stream.WriteString(d.Delta)
			b.text = b.stream.String()
		}
	case "session.text.ended":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		key := d.MessageID + ":" + strconv.Itoa(d.Ordinal)
		if idx, ok := a.live[key]; ok {
			delete(a.live, key)
			a.blocks[idx].kind = kindText
			a.blocks[idx].text = d.Text
			a.blocks[idx].stream = nil
		}
	case "session.tool.input.started":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.toolNames[d.ID] = d.Name
	case "session.tool.called":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		label, icon := toolLook(a.toolNames[d.ID])
		var in struct {
			Command string `json:"command"`
		}
		json.Unmarshal(d.Input, &in)
		var lines []string
		if in.Command != "" {
			lines = append(lines, "$ "+in.Command)
			a.toolCmds[d.ID] = in.Command
		}
		a.toolBlocks[d.ID] = len(a.blocks)
		a.blocks = append(a.blocks, block{kind: kindActivity, text: label, icon: icon,
			key: "activity:" + d.ID, detail: commandDetail(lines...)})
	case "session.tool.success":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		idx, ok := a.toolBlocks[d.ID]
		if !ok {
			return
		}
		delete(a.toolBlocks, d.ID)
		run := toolRun{command: a.toolCmds[d.ID],
			exit: d.Metadata.Exit, truncated: d.Metadata.Truncated}
		delete(a.toolCmds, d.ID)
		run.out = textOf(d.Content)
		a.blocks[idx].detail = commandDetail(toolDetailLines(run)...)
	case "permission.asked":
		var d evPermissionData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if _, ok := a.pending[d.ID]; ok {
			return
		}
		perm := &agentPerm{id: d.ID, session: d.SessionID,
			action: d.Action, resources: d.Resources, save: d.Save}
		a.pending[d.ID] = perm
		a.blocks = append(a.blocks, block{kind: kindPermission,
			text: "Approval needed", key: "permission:" + d.ID, req: perm})
	case "session.execution.started":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.busySession = d.SessionID
	case "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if a.busySession == d.SessionID {
			a.busySession = ""
		}
	case "session.model.selected":
		var d struct {
			SessionID string `json:"sessionID"`
			Model     struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		for i := range a.scope().sessions {
			if a.scope().sessions[i].id == d.SessionID {
				a.scope().sessions[i].model = agentModelRef{providerID: d.Model.ProviderID,
					id: d.Model.ID, variant: d.Model.Variant}
			}
		}
	case "session.renamed":
		var d struct {
			SessionID string `json:"sessionID"`
			Title     string `json:"title"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		for i := range a.scope().sessions {
			if a.scope().sessions[i].id == d.SessionID {
				a.scope().sessions[i].title = d.Title
			}
		}
		// The conversation on show takes its new name at once.
		if d.SessionID == a.selectedSession {
			a.selected = d.Title
		}
	}
}

// belongs reports whether a session's stream belongs to the conversation
// on show: only when the transcripts match, or when nothing real is open.
func (a *app) belongs(sid string) bool {
	return a.selectedSession == "" || sid == "" || sid == a.selectedSession
}

// dropPerm takes an approval card out of the transcript once its reply
// has landed; the tool row below it carries the story from there.
func (a *app) dropPerm(requestID string) {
	if _, ok := a.pending[requestID]; !ok {
		return
	}
	delete(a.pending, requestID)
	for i, b := range a.blocks {
		if b.key == "permission:"+requestID {
			a.blocks = slices.Delete(a.blocks, i, i+1)
			for key, idx := range a.live {
				if idx > i {
					a.live[key] = idx - 1
				}
			}
			for key, idx := range a.toolBlocks {
				if idx > i {
					a.toolBlocks[key] = idx - 1
				}
			}
			break
		}
	}
}

// dropSession takes one conversation out of the list and, when it was
// the one on show, leaves a fresh transcript behind.
func (a *app) dropSession(sid string) {
	if a.pinned[sid] {
		a.togglePin(agentSession{id: sid})
	}
	for i, s := range a.scope().sessions {
		if s.id == sid {
			a.scope().sessions = append(a.scope().sessions[:i], a.scope().sessions[i+1:]...)
			break
		}
	}
	for id, p := range a.pending {
		if p.session == sid {
			delete(a.pending, id)
		}
	}
	if a.selectedSession != sid {
		return
	}
	a.selectedSession = ""
	a.selected = ""
	a.freshTranscript()
}

// dedupeSessions adds the rows the list does not have yet, keeping what
// came first, since later pages hold the older conversations.
func dedupeSessions(all, add []agentSession) []agentSession {
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.id] = true
	}
	out := all
	for _, s := range add {
		if !seen[s.id] {
			seen[s.id] = true
			out = append(out, s)
		}
	}
	return out
}

// toolLook pairs a tool's name with the activity row's label and icon as
// the design draws them.
func toolLook(name string) (string, *ui.SVG) {
	switch name {
	case "shell":
		return "Ran command", icTerminal
	case "read":
		return "Read files", icBook
	case "grep", "glob", "list", "find":
		return "Searched", icBook
	case "":
		return "Used tool", icPlug
	default:
		return "Used " + name, icPlug
	}
}
