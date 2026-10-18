package main

import (
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"
)

type targetKind uint8

const (
	targetDraft targetKind = iota
	targetSession
)

// conversationTarget binds navigation and its request revision together.
type conversationTarget struct {
	kind     targetKind
	session  string
	revision uint64
}

func (t conversationTarget) isDraft() bool { return t.kind == targetDraft }
func (t *conversationTarget) selectSession(id string) {
	t.revision++
	t.session = id
	if id == "" {
		t.kind = targetDraft
	} else {
		t.kind = targetSession
	}
}

// openConversation admits the read before committing the complete navigation.
func (a *app) openConversation(session opencode.Session) bool {
	revision := a.conversation.target.revision + 1
	if a.queueCommand(runner.Open{Session: session.ID, Revision: revision}) != runner.Accepted {
		return false
	}
	a.adoptSession(session)
	return true
}
