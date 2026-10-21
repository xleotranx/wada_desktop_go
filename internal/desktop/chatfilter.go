package desktop

import (
	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

const (
	sidebarFilterUnread    = "unread"
	sidebarFilterAttention = "attention"
	sidebarFilterDrafts    = "drafts"
	sidebarFilterArchived  = "archived"
)

// setSidebarFilter narrows the sidebar to one kind of chat. The search
// field takes over the list while it has a query, so a filter starts by
// closing it.
func (a *app) setSidebarFilter(value string) {
	previous := a.layout.sidebarFilter
	switch value {
	case sidebarFilterUnread, sidebarFilterAttention, sidebarFilterDrafts, sidebarFilterArchived:
		a.layout.sidebarFilter = value
	default:
		a.layout.sidebarFilter = ""
	}
	if a.layout.sidebarFilter != previous {
		a.layout.sidebarList = ui.ListState{}
	}
	a.search.searchText = ""
	a.search.searchOpen = false
	a.layout.sidebarCollapsed = false
}

func sidebarFilterLabel(value string) string {
	switch value {
	case sidebarFilterUnread:
		return "Unread chats"
	case sidebarFilterAttention:
		return "Chats needing attention"
	case sidebarFilterDrafts:
		return "Chats with drafts"
	case sidebarFilterArchived:
		return "Archived chats"
	}
	return "Chats"
}

// filteredSessions lists what the active filter shows, once each, in the
// server's own order.
func (a *app) filteredSessions() []*agent.Session {
	scope := a.scope()
	out := make([]*agent.Session, 0, len(scope.sessions))
	for i := range scope.sessions {
		s := &scope.sessions[i]
		archived := scope.archived[s.ID]
		if a.layout.sidebarFilter == sidebarFilterArchived {
			// Only the archived filter shows them, and it shows nothing else.
			if archived {
				out = append(out, s)
			}
			continue
		}
		if archived {
			continue
		}
		match := false
		switch a.layout.sidebarFilter {
		case sidebarFilterUnread:
			match = scope.unread[s.ID]
		case sidebarFilterAttention:
			match = scope.attention[s.ID]
		case sidebarFilterDrafts:
			match = a.hasDraft(s.ID)
		}
		if match {
			out = append(out, s)
		}
	}
	return out
}

// filteredChatsHeading labels the active filter and lets the user clear it.
func (a *app) filteredChatsHeading(c *ui.Context, p palette) {
	ui.Row(c).Height(28).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, sidebarFilterLabel(a.layout.sidebarFilter)).FontSize(11.5).
			FontWeight(600).TextColor(p.Muted).SingleLine()
		ui.Spacer(c)
		clear := ui.ButtonBase(c).Label("Clear chat filter").Size(22, 22).Radius(6).Center().
			Tooltip("Clear chat filter")
		if clear.Hovered() {
			clear.Background(p.Hover)
		}
		clear.OnClick(func() { a.setSidebarFilter("") })
		clear.Children(func() {
			ui.Icon(c, icClose).FontSize(11).TextColor(p.Faint)
		})
	})
}

func filterEmptyNoun(value string) string {
	switch value {
	case sidebarFilterUnread:
		return "unread chats"
	case sidebarFilterAttention:
		return "chats needing attention"
	case sidebarFilterDrafts:
		return "chats with drafts"
	case sidebarFilterArchived:
		return "archived chats"
	}
	return "chats"
}
