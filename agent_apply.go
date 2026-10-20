package main

import (
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
						out = append(out, chat.Block{Kind: chat.KindText, Text: t, Message: chat.MessageID(m.ID)})
						spent = len(out) - 1
					}
				case "tool":
					label, icon := chat.ToolLook(part.Name)
					id := part.ID
					if id == "" {
						id = fmt.Sprintf("part-%d", i)
					}

					run := toolRun{command: part.State.Input.Command,
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
func (a *app) finishCutTurns() { a.conversation.transcript.Cut() }

// streamLine appends a faint note to the transcript for engine trouble
// worth showing in place.
func (a *app) streamLine(text string) {
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindNote, Text: text})
	a.layout.feed.ScrollToEnd()
}

// apply folds one agent event into the transcript. Kinds reuse the
// design's vocabulary: assistant words stream into the answer as it
// lands, and each tool call is an activity row that gains its detail
// once something ran.
func (a *app) applyUpdate(ev opencode.Update) {
	if ev.Kind != opencode.ModelSelected && ev.Kind != opencode.SessionRenamed && !a.belongs(ev.SessionID) {
		return
	}
	switch ev.Kind {
	case opencode.TextStarted:
		a.conversation.transcript.StartText(chat.MessageID(ev.Text.MessageID), ev.Text.Ordinal)
	case opencode.TextDelta:
		a.conversation.transcript.Delta(chat.MessageID(ev.Text.MessageID), ev.Text.Ordinal, ev.Text.Delta)
	case opencode.TextEnded:
		a.conversation.transcript.EndText(chat.MessageID(ev.Text.MessageID), ev.Text.Ordinal, ev.Text.Text)
	case opencode.ToolStarted:
		a.conversation.transcript.ToolStarted(ev.Tool.ID, ev.Tool.Name)
	case opencode.ToolCalled:
		a.conversation.transcript.ToolCalled(ev.Tool.ID, ev.Tool.Command)
	case opencode.ToolSucceeded:
		d := ev.Tool
		run := toolRun{command: a.conversation.transcript.ToolCommand(d.ID), exit: d.Metadata.Exit, truncated: d.Metadata.Truncated, out: textOf(d.Content)}
		a.conversation.transcript.ToolFinished(d.ID, toolDetailLines(run))
	case opencode.PermissionAsked:
		d := ev.Permission
		a.conversation.transcript.Ask(&chat.Permission{ID: chat.PermissionID(d.ID), Session: chat.SessionID(d.SessionID), Action: d.Action, Resources: d.Resources, Save: d.Save})
	case opencode.ExecutionStarted:
		a.conversation.transcript.Start(chat.SessionID(ev.SessionID))
	case opencode.ExecutionSucceeded, opencode.ExecutionFailed, opencode.ExecutionInterrupted:
		if a.conversation.transcript.Finish() {
			a.refreshSessions()
			a.refreshUsage(ev.SessionID)
		}
	case opencode.ModelSelected:
		for i := range a.scope().sessions {
			if a.scope().sessions[i].ID == ev.SessionID {
				a.scope().sessions[i].Model = ev.Model
			}
		}
	case opencode.SessionRenamed:
		for i := range a.scope().sessions {
			if a.scope().sessions[i].ID == ev.SessionID {
				a.scope().sessions[i].Title = ev.Title
			}
		}
		if ev.SessionID == string(a.conversation.target.session) {
			a.conversation.selected = ev.Title
		}
	}
}

// belongs reports whether a session's stream belongs to the conversation
// on show. A draft accepts no stream until its server session is adopted.
func (a *app) belongs(sid string) bool {
	return !a.conversation.target.isDraft() && sid == string(a.conversation.target.session)
}

// dropPerm takes an approval card out of the transcript once its reply
// has landed; the tool row below it carries the story from there.
func (a *app) dropPerm(requestID string) {
	a.conversation.transcript.DropPermission(chat.PermissionID(requestID))
}

// dropSession takes one conversation out of the list and its tab out of
// the strip: a deleted open tab hands the view to its neighbour, or leaves
// a fresh transcript when it was the last one.
func (a *app) dropSession(sid string) {
	scope := a.scope()
	index := -1
	for i, tab := range scope.tabs {
		if tab.id == sid {
			index = i
			break
		}
	}
	if index >= 0 {
		scope.tabs = removeTab(scope.tabs, sid)
	}
	if scope.pinned[sid] {
		a.togglePin(opencode.Session{ID: sid})
	}
	for i, s := range scope.sessions {
		if s.ID == sid {
			scope.sessions = append(scope.sessions[:i], scope.sessions[i+1:]...)
			break
		}
	}
	a.conversation.transcript.DropSession(chat.SessionID(sid))
	if string(a.conversation.target.session) != sid {
		return
	}
	if len(scope.tabs) > 0 {
		next := 0
		if index >= 0 {
			next = min(index, len(scope.tabs)-1)
		}
		if a.openTab(scope.tabs[next].id) {
			return
		}
	}
	a.resetDraft("")
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
