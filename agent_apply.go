package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
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
func restoreBlocks(messages []opencode.Message) []chat.Block {
	var out []chat.Block
	for _, m := range messages {
		switch m.Type {
		case "user":
			t := m.Payload.Text
			if t == "" {
				t = m.Text
			}
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, chat.Block{Kind: chat.KindUser, Text: t})
			}
		case "assistant":
			spent := -1 // the message's last text part, which words its spending
			for i, part := range m.Content {
				switch part.Type {
				case "text":
					if t := strings.TrimSpace(part.Text); t != "" {
						out = append(out, chat.Block{Kind: chat.KindText, Text: t, Message: m.ID})
						spent = len(out) - 1
					}
				case "tool":
					label, icon := chat.ToolLook(part.Name)
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
					out = append(out, chat.Block{Kind: chat.KindActivity, Text: label, Icon: icon,
						Key: "activity:" + id, Detail: toolDetailLines(run)})
				}
			}
			// One answer can hold several text parts; the spending stands
			// under the last of them, where the message ends.
			if spent >= 0 {
				out[spent].Cost = m.Cost
				out[spent].Tokens = m.Tokens.Output + m.Tokens.Reasoning
			}
		}
	}
	return out
}

// textOf joins the text pieces of tool output into lines.
func textOf(content []opencode.ToolOutput) []string {
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
func (a *app) finishCutTurns() { a.transcript.Cut() }

// streamLine appends a faint note to the transcript for engine trouble
// worth showing in place.
func (a *app) streamLine(text string) {
	a.transcript.Append(chat.Block{Kind: chat.KindNote, Text: text})
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
	SessionID string                `json:"sessionID"`
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Input     json.RawMessage       `json:"input"`
	Content   []opencode.ToolOutput `json:"content"`
	Metadata  struct {
		Status    string `json:"status"`
		Truncated bool   `json:"truncated"`
		Exit      int    `json:"exit"`
	} `json:"metadata"`
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

// apply folds one agent event into the transcript. Kinds reuse the
// design's vocabulary: assistant words stream into the answer as it
// lands, and each tool call is an activity row that gains its detail
// once something ran.
func (a *app) apply(ev opencode.Event) {
	switch ev.Type {
	case "session.text.started":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.StartText(d.MessageID, d.Ordinal)
	case "session.text.delta":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.Delta(d.MessageID, d.Ordinal, d.Delta)
	case "session.text.ended":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.EndText(d.MessageID, d.Ordinal, d.Text)
	case "session.tool.input.started":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.ToolStarted(d.ID, d.Name)
	case "session.tool.called":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		var in struct {
			Command string `json:"command"`
		}
		json.Unmarshal(d.Input, &in)
		a.transcript.ToolCalled(d.ID, in.Command)
	case "session.tool.success":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		run := toolRun{command: a.transcript.ToolCommand(d.ID), exit: d.Metadata.Exit, truncated: d.Metadata.Truncated, out: textOf(d.Content)}
		a.transcript.ToolFinished(d.ID, toolDetailLines(run))
	case "permission.asked":
		var d evPermissionData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.Ask(&chat.Permission{ID: d.ID, Session: d.SessionID, Action: d.Action, Resources: d.Resources, Save: d.Save})
	case "session.execution.started":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.transcript.Start(d.SessionID)
	case "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if !a.transcript.Finish() {
			return
		}
		// The turn moved the chat's place in the list and what it has
		// spent: read the newest page again, and ask the store what the
		// answer that just ended cost.
		a.refreshSessions()
		a.refreshUsage(d.SessionID)
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
			if a.scope().sessions[i].ID == d.SessionID {
				a.scope().sessions[i].Model = opencode.ModelRef{ProviderID: d.Model.ProviderID,
					ID: d.Model.ID, Variant: d.Model.Variant}
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
			if a.scope().sessions[i].ID == d.SessionID {
				a.scope().sessions[i].Title = d.Title
			}
		}
		// The conversation on show takes its new name at once.
		if d.SessionID == a.selectedSession {
			a.selected = d.Title
		}
	}
}

// belongs reports whether a session's stream belongs to the conversation
// on show. A draft accepts no stream until its server session is adopted.
func (a *app) belongs(sid string) bool {
	return a.selectedSession != "" && sid == a.selectedSession
}

// dropPerm takes an approval card out of the transcript once its reply
// has landed; the tool row below it carries the story from there.
func (a *app) dropPerm(requestID string) { a.transcript.DropPermission(requestID) }

// dropSession takes one conversation out of the list and, when it was
// the one on show, leaves a fresh transcript behind.
func (a *app) dropSession(sid string) {
	if a.pinned[sid] {
		a.togglePin(opencode.Session{ID: sid})
	}
	for i, s := range a.scope().sessions {
		if s.ID == sid {
			a.scope().sessions = append(a.scope().sessions[:i], a.scope().sessions[i+1:]...)
			break
		}
	}
	a.transcript.DropSession(sid)
	if a.selectedSession != sid {
		return
	}
	a.selectedSession = ""
	a.selected = ""
	a.freshTranscript()
	a.workspaceChanged()
}

// dedupeSessions adds the rows the list does not have yet, keeping what
// came first, since later pages hold the older conversations.
func dedupeSessions(all, add []opencode.Session) []opencode.Session {
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.ID] = true
	}
	out := all
	for _, s := range add {
		if !seen[s.ID] {
			seen[s.ID] = true
			out = append(out, s)
		}
	}
	return out
}

// freshSessions lays one page the server just gave over a list that
// already holds older pages: the fresh rows stand in their own order at
// the head, and the kept ones they do not name follow in theirs.
func freshSessions(kept, fresh []opencode.Session) []opencode.Session {
	seen := map[string]bool{}
	for _, s := range fresh {
		seen[s.ID] = true
	}
	out := fresh
	for _, s := range kept {
		if !seen[s.ID] {
			out = append(out, s)
		}
	}
	return out
}
