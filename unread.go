package main

import (
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// markUnread records that a chat moved while it was not on show. A
// background approval also needs attention, which the bell and the rows
// color differently.
func (a *app) markUnread(id string, attention bool) {
	if id == "" {
		return
	}
	scope := a.scope()
	if scope.unread == nil {
		scope.unread = map[string]bool{}
	}
	if scope.attention == nil {
		scope.attention = map[string]bool{}
	}
	scope.unread[id] = true
	if attention {
		scope.attention[id] = true
	}
}

// clearUnread marks one chat read, both its unread dot and its approval.
func (a *app) clearUnread(id string) {
	scope := a.scope()
	delete(scope.unread, id)
	delete(scope.attention, id)
}

// toggleUnread marks the open chat unread, or read again.
func (a *app) toggleUnread() {
	id := string(a.conversation.target.session)
	if id == "" {
		return
	}
	if a.scope().unread[id] {
		a.clearUnread(id)
		return
	}
	a.markUnread(id, false)
}

// toggleSessionUnread is the row menu's version for a chat that is not
// necessarily the one on show.
func (a *app) toggleSessionUnread(s opencode.Session) {
	if a.scope().unread[s.ID] {
		a.clearUnread(s.ID)
		return
	}
	a.markUnread(s.ID, false)
}

// unreadCounts totals the scope's marks: attention is the subset waiting
// on an approval, unread is every chat with something new.
func (a *app) unreadCounts() (attention, unread int) {
	scope := a.scope()
	return len(scope.attention), len(scope.unread)
}

// openNextMarked visits the next chat needing attention, then any unread
// chat, skipping the one already on show.
func (a *app) openNextMarked() {
	scope := a.scope()
	current := string(a.conversation.target.session)
	for _, onlyAttention := range []bool{true, false} {
		for _, s := range scope.sessions {
			if s.ID == current {
				continue
			}
			if onlyAttention && scope.attention[s.ID] {
				a.openConversation(s)
				return
			}
			if !onlyAttention && scope.unread[s.ID] {
				a.openConversation(s)
				return
			}
		}
	}
}

// unreadDot is the small mark a row or tab wears; its label says which
// kind of news it stands for.
func unreadDot(c *ui.Context, p palette, attention bool) {
	color := p.muted
	label := "Unread"
	if attention {
		color = p.orange
		label = "Needs attention"
	}
	ui.Box(c).Size(6, 6).Radius(999).Background(color).MarginStart(6).Label(label)
}
