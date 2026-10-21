package desktop

import (
	"slices"
	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// toggleArchiveChat hides one chat from the pinned and project lists, or
// brings it back. Archiving drops the chat's pin; its unread marks stay
// put, out of the counts until it is restored.
func (a *app) toggleArchiveChat(s agent.Session) {
	if s.ID == "" {
		return
	}
	scope := a.scope()
	if scope.archived == nil {
		scope.archived = map[string]bool{}
	}
	if scope.archived[s.ID] {
		delete(scope.archived, s.ID)
		a.notify(ui.Toast{Title: "Chat restored"})
	} else {
		scope.archived[s.ID] = true
		if scope.pinned[s.ID] {
			scope.setPins(slices.DeleteFunc(slices.Clone(scope.pinOrder), func(id string) bool {
				return id == s.ID
			}))
		}
		a.notify(ui.Toast{Title: "Chat archived", Description: "Find it under Archived chats."})
	}
	a.saveState()
}

// listedSessions is the scope's chats without the archived ones, in the
// server's own order: the list the recent-chat keys and the boot settle on.
func (a *app) listedSessions() []agent.Session {
	scope := a.scope()
	out := make([]agent.Session, 0, len(scope.sessions))
	for _, s := range scope.sessions {
		if !scope.archived[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// chatArchived reports one chat hidden from the lists and the counts.
func (a *app) chatArchived(id string) bool { return a.scope().archived[id] }

// archiveLabel names the action the chat menus offer for one chat.
func (a *app) archiveLabel(id string) string {
	if a.chatArchived(id) {
		return "Unarchive chat"
	}
	return "Archive chat"
}
