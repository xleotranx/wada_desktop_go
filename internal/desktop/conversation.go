package desktop

import (
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"
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
	session  chat.SessionID
	revision uint64
}

func (t conversationTarget) isDraft() bool { return t.kind == targetDraft }
func (t *conversationTarget) selectSession(id chat.SessionID) {
	t.revision++
	t.session = id
	if id == "" {
		t.kind = targetDraft
	} else {
		t.kind = targetSession
	}
}

// openConversation admits the read before committing the complete navigation.
func (a *app) openConversation(session agent.Session) bool {
	revision := a.conversation.target.revision + 1
	if a.queueCommand(runner.Open{Session: chat.SessionID(session.ID), Revision: revision}) != runner.Accepted {
		return false
	}
	a.connections.booted = true
	a.adoptSession(session)
	return true
}

// openRecentChat follows the numbered command keys: the newest chat is
// one, in the order the server listed them. A key past the list or a
// conversation already open changes nothing.
func (a *app) openRecentChat(index int) {
	chats := a.listedSessions()
	if index < 0 || index >= len(chats) {
		return
	}
	session := chats[index]
	if session.ID == string(a.conversation.target.session) {
		return
	}
	a.openConversation(session)
}
