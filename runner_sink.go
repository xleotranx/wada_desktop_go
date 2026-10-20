package main

import (
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"
)

// runnerSink is the UI-thread adapter; workers only publish typed results.
type runnerSink struct{ a *app }

func (s runnerSink) Status(status runner.Status) {
	h := serverHealth{status: status.State, addr: status.Address, detail: status.Facts.Version}
	if status.Err != nil {
		h.detail = status.Err.Error()
	}
	s.a.setHealth(s.a.connections.activeServer, h)
	if status.State == healthReady {
		s.a.setFacts(s.a.connections.activeServer, status.Facts)
		s.a.ensureVcs()
	}
	if status.State == healthDown {
		s.a.finishCutTurns()
	}
}
func (s runnerSink) Sessions(page runner.Page) {
	a := s.a
	if page.Pinned {
		for _, session := range page.Sessions {
			if a.scope().pinned[session.ID] {
				a.scope().sessions = dedupeSessions(a.scope().sessions, []opencode.Session{session})
			}
		}
		return
	}
	if !a.connections.booted {
		a.connections.booted = true
		a.settleBoot(page.Sessions, page.Cursor, page.Err)
		return
	}
	if page.More {
		a.scope().moreLoading = false
	}
	if page.Err != nil {
		a.streamLine("The conversations did not load: " + page.Err.Error())
		return
	}
	if page.More {
		a.scope().sessions = dedupeSessions(a.scope().sessions, page.Sessions)
	} else {
		a.scope().sessions = freshSessions(a.scope().sessions, page.Sessions)
	}
	a.scope().nextCursor = page.Cursor
	a.scope().sessionsLoaded = true
}
func (s runnerSink) Catalog(catalog runner.Catalog) {
	if catalog.Err != nil {
		s.a.streamLine("The model list did not load: " + catalog.Err.Error())
	} else {
		s.a.scope().models = catalog.Models
	}
	if catalog.AgentsErr == nil {
		s.a.scope().agents = catalog.Agents
		s.a.settleAgents()
	}
	if catalog.DefaultErr == nil {
		s.a.scope().serverDefault = catalog.Default
	}
	s.a.scope().modelsLoaded = true
}
func (s runnerSink) Projects(projects []string) { s.a.scope().projects = projects }
func (s runnerSink) Updates(events []opencode.Update) {
	for _, event := range events {
		s.a.applyUpdate(event)
	}
}
func (s runnerSink) Opened(opened runner.Opened) {
	if string(s.a.conversation.target.session) != opened.Session || s.a.conversation.target.revision != opened.Revision {
		return
	}
	s.a.freshTranscript()
	s.a.conversation.transcript.Replace(restoreBlocks(opened.Messages))
}
func (s runnerSink) Created(created runner.Created) {
	a := s.a
	if a.conversation.target.revision == created.Draft && a.conversation.target.isDraft() {
		a.conversation.target.selectSession(chat.SessionID(created.Session.ID))
		a.conversation.selected = "New chat"
		a.scope().activeTab = created.Session.ID
		a.rememberTab(created.Session)
	}
	a.scope().sessions = dedupeSessions([]opencode.Session{created.Session}, a.scope().sessions)
	a.saveState()
}
func (s runnerSink) Answered(id string)             { s.a.dropPerm(id) }
func (s runnerSink) Deleted(id string)              { s.a.dropSession(id) }
func (s runnerSink) Usage(message opencode.Message) { s.a.acceptUsage(message) }
func (s runnerSink) Notice(message string)          { s.a.streamLine(message) }

// settleAgents keeps the persona pick real: a chat can only be born with
// what the server still offers, so a pick that left the catalog stands
// down to the server's default.
func (a *app) settleAgents() {
	if a.conversation.agent == "" {
		return
	}
	for _, agent := range a.scope().agents {
		if agent.ID == a.conversation.agent {
			return
		}
	}
	a.conversation.agent = ""
}

var _ runner.Sink = runnerSink{}

func (s runnerSink) Failed(failure runner.Failure) {
	target := s.a.conversation.target
	matches := failure.Origin.IsDraft && target.isDraft() && failure.Origin.Draft == target.revision
	if !failure.Origin.IsDraft {
		matches = failure.Origin.Session == "" || failure.Origin.Session == target.session
	}
	if matches {
		s.a.streamLine(failure.Text)
	} else {
		s.a.conversation.commandError = failure.Text
	}
}
