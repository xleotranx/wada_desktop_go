package desktop

import (
	"fmt"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

type sidebarKind uint8

const (
	sidebarHeading sidebarKind = iota
	sidebarHint
	sidebarChat
	sidebarPin
	sidebarProject
	sidebarProjectChat
	sidebarMoreChats
	sidebarMoreProjects
	sidebarOlder
	sidebarFilterHeading
)

// The tree is flattened before rendering so an expanded project or a large
// pin list cannot hide thousands of buttons inside a single virtual row.
type sidebarItem struct {
	kind    sidebarKind
	session *agent.Session
	dir     string
	text    string
	count   int
	active  bool
}

type sidebarKey struct {
	kind    sidebarKind
	id, dir string
}

func (item sidebarItem) key() sidebarKey {
	id := item.text
	if item.session != nil {
		id = item.session.ID
	}
	return sidebarKey{item.kind, id, item.dir}
}

func (a *app) sidebarItems() []sidebarItem {
	clear(a.layout.sidebarRows)
	rows := a.layout.sidebarRows[:0]
	if a.layout.sidebarFilter != "" {
		rows = append(rows, sidebarItem{kind: sidebarFilterHeading})
		sessions := a.filteredSessions()
		for i := range sessions {
			rows = append(rows, sidebarItem{kind: sidebarChat, session: sessions[i]})
		}
		if len(sessions) == 0 {
			rows = append(rows, sidebarItem{kind: sidebarHint, text: fmt.Sprintf("No %s.", filterEmptyNoun(a.layout.sidebarFilter))})
		}
	} else {
		rows = append(rows, sidebarItem{kind: sidebarHeading, text: "Pinned"})
		pins := a.pinnedSessions()
		for i := range pins {
			rows = append(rows, sidebarItem{kind: sidebarPin, session: pins[i]})
		}
		if len(pins) == 0 {
			rows = append(rows, sidebarItem{kind: sidebarHint, text: "No pinned chats"})
		}
		if !a.hasWorkspaceFeatures() {
			rows = append(rows, sidebarItem{kind: sidebarHeading, text: "Chats"})
			for i := range a.scope().sessions {
				session := &a.scope().sessions[i]
				if !a.chatArchived(session.ID) && !a.scope().pinned[session.ID] {
					rows = append(rows, sidebarItem{kind: sidebarChat, session: session})
				}
			}
			if a.scope().nextCursor != "" {
				rows = append(rows, sidebarItem{kind: sidebarOlder})
			}
		} else {
			rows = append(rows, sidebarItem{kind: sidebarHeading, text: "Projects"})
			projects := a.projectDirectories()
			if len(projects) == 0 {
				hint := "Add a project to get started"
				if !a.localActive() {
					hint = "The server lists its projects here"
				}
				rows = append(rows, sidebarItem{kind: sidebarHint, text: hint})
			}
			visible := len(projects)
			if !a.layout.projectsExpanded {
				visible = min(visible, 5)
			}
			for _, dir := range projects[:visible] {
				chats := a.projectChats(dir)
				active := false
				for _, session := range chats {
					if session.ID == string(a.conversation.target.session) {
						active = true
						break
					}
				}
				rows = append(rows, sidebarItem{kind: sidebarProject, dir: dir, count: len(chats), active: active})
				if a.layout.projectCollapsed[dir] || len(chats) == 0 {
					continue
				}
				shown := len(chats)
				if !a.layout.projectChatsShown[dir] {
					shown = min(shown, projectChatPreview)
				}
				for i := range chats[:shown] {
					rows = append(rows, sidebarItem{kind: sidebarProjectChat, dir: dir, session: chats[i]})
				}
				if len(chats) > projectChatPreview {
					rows = append(rows, sidebarItem{kind: sidebarMoreChats, dir: dir, count: len(chats) - projectChatPreview})
				}
			}
			if visible < len(projects) || a.layout.projectsExpanded {
				rows = append(rows, sidebarItem{kind: sidebarMoreProjects, count: len(projects) - visible})
			}
		}
	}
	a.layout.sidebarRows = rows
	return rows
}

func (a *app) sidebarList(c *ui.Context, p palette) {
	rows := a.sidebarItems()
	ui.List(c.Key("sidebar:list"), &a.layout.sidebarList, len(rows)).Grow(1).FillWidth().Gap(2).
		ItemKey(func(i int) any { return rows[i].key() }).Rows(func(row ui.ListRow) {
		item := rows[row.Index]
		c := row.Context
		switch item.kind {
		case sidebarHeading:
			var add func()
			if item.text == "Projects" {
				add = a.openProjectDialog
				if !a.localActive() {
					add = func() { a.browseServerFolders(false) }
				}
			}
			a.sectionHeading(c, p, item.text, add)
		case sidebarHint:
			ui.Row(c).Height(27).Padding(0, 8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, item.text).FontSize(12.5).TextColor(p.Faint).SingleLine()
			})
		case sidebarChat:
			a.sessionRow(c, p, *item.session)
		case sidebarPin:
			a.pinnedSessionRow(c, p, *item.session)
		case sidebarProject:
			a.projectDragRow(c, p, item.dir, item.count, item.active)
		case sidebarProjectChat:
			a.projectChatRow(c, p, *item.session)
		case sidebarMoreChats:
			a.moreProjectChatsRow(c, p, item.dir, item.count)
		case sidebarMoreProjects:
			a.moreProjectsRow(c, p, item.count)
		case sidebarOlder:
			if components.Button(c, "Load older chats", components.ButtonSecondary).Clicked() {
				a.loadOlderChats()
			}
		case sidebarFilterHeading:
			a.filteredChatsHeading(c, p)
		}
	})
}
