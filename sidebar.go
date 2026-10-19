package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

const navRailWidth float32 = 52

// navRail is the slim primary navigation beside the workspace sidebar: a
// column of icons on the window's material.
func (a *app) navRail(c *ui.Context, p palette) {
	rail := ui.Column(c).Width(navRailWidth).FillHeight()
	if !c.Vibrancy() {
		rail.Background(p.railBg)
	}
	rail.Children(func() {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Gap(5).Padding(10, 7).Children(func() {
			a.railButton(c, p, "home", "Home", icHome, a.startNewChat)
			a.railButton(c, p, "projects", "Projects", icStack, a.openProjectDialog)
			a.railButton(c, p, "history", "History", icHistory, a.openChatSearch)
			c.Key("rail:more")
			more := ui.ButtonBase(c).Label("More").Size(36, 36).Radius(10).Center().
				Cursor(ui.CursorPointer).Tooltip("More navigation")
			if more.Hovered() {
				more.Background(p.hover)
			}
			more.Menu(func(m *ui.Menu) {
				if m.Item("New chat").Chosen() {
					a.startNewChat()
				}
				if m.Item("Search chats…").Chosen() {
					a.openChatSearch()
				}
				if m.Item("Open folder…").Chosen() {
					a.openProjectDialog()
				}
				m.Separator()
				if m.Item("Settings").Chosen() {
					a.openSettings("")
				}
				label := "Hide sidebar"
				if a.layout.sidebarCollapsed {
					label = "Show sidebar"
				}
				if m.Item(label).Chosen() {
					a.layout.sidebarCollapsed = !a.layout.sidebarCollapsed
				}
			})
			more.Children(func() { ui.Icon(c, icEllipsis).FontSize(18).TextColor(p.muted) })
			ui.Spacer(c)
			ui.Box(c).FillWidth().Height(1).Background(p.hairline)
			a.railButton(c, p, "changes", "Changes", icGitBranch, func() { a.openTool("Changes") })
			a.railButton(c, p, "tools", "Tools", icGrid, func() { a.openTool("") })
			a.settingsButton(c, p)
		})
		ui.Row(c).Height(48).Center().Children(func() {
			ui.Avatar(c, "Shan Lin", nil).FontSize(12)
		})
	})
}

func (a *app) railButton(c *ui.Context, p palette, key, label string, icon *ui.SVG, on func()) {
	c.Key("rail:" + key)
	b := ui.ButtonBase(c).Label(label).Size(36, 36).Radius(10).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	} else if a.layout.selectedRail == key {
		b.Background(p.selected)
	}
	b.OnClick(func() {
		// Anything the rail navigates to leaves the settings page.
		a.layout.settingsOpen = false
		a.layout.selectedRail = key
		on()
	})
	b.Children(func() {
		tint := p.muted
		if a.layout.selectedRail == key {
			tint = p.text
		}
		ui.Icon(c, icon).FontSize(18).TextColor(tint)
	})
}

// settingsButton is the rail's gear: the settings page is not one of the
// rail's places, so opening it keeps the selection the user lands back on.
func (a *app) settingsButton(c *ui.Context, p palette) {
	c.Key("rail:settings")
	b := ui.ButtonBase(c).Label("Settings").Size(36, 36).Radius(10).Center().
		Cursor(ui.CursorPointer).Tooltip("Settings")
	if b.Hovered() {
		b.Background(p.hover)
	} else if a.layout.settingsOpen {
		b.Background(p.selected)
	}
	b.OnClick(func() { a.openSettings("") })
	b.Children(func() {
		tint := p.muted
		if a.layout.settingsOpen {
			tint = p.text
		}
		ui.Icon(c, icGear).FontSize(18).TextColor(tint)
	})
}

// openChatSearch aims the sidebar's search field and leaves the settings
// page behind.
func (a *app) openChatSearch() {
	a.layout.settingsOpen = false
	a.search.searchOpen = true
	a.search.focusSearch = true
}

// sidebarPane holds the workspace switcher, pinned chats and projects.
func (a *app) sidebarPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Width(a.layout.sidebarW).Background(p.sidebarBg).Children(func() {
		a.headerRow(c, p, headerH)

		ui.Column(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			if a.preferences.metadataError != "" {
				ui.Text(c, a.preferences.metadataError).FontSize(12).TextColor(p.orange)
				if a.preferences.metadata != nil {
					ui.Button(c, "Retry saving").OnClick(a.saveState)
				}
			} else if a.preferences.metadataSaving {
				ui.Text(c, "Saving…").FontSize(12).TextColor(p.muted)
			}
			a.newChatRow(c, p)
			if a.search.searchOpen {
				a.searchField(c, p)
			}

			if a.search.searchText == "" {
				// The lists scroll once they outgrow the pane, under the
				// New chat and search rows, which stay put.
				ui.Scroll(c).Grow(1).FillWidth().Gap(2).Children(func() {
					a.sectionHeading(c, p, "Pinned", nil)
					pins := a.pinnedSessions()
					if len(pins) == 0 {
						ui.Row(c).Height(27).Padding(0, 8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "No pinned chats").FontSize(12.5).TextColor(p.faint).SingleLine()
						})
					} else {
						for _, s := range pins {
							a.sessionRow(c, p, s)
						}
					}

					addProjects := a.openProjectDialog
					if !a.localActive() {
						// A remote server's folders are its own disk's; the
						// system panel is this machine's to hand over.
						addProjects = func() { a.browseServerFolders(false) }
					}
					a.sectionHeading(c, p, "Projects", addProjects)
					projects := a.projectDirectories()
					if len(projects) == 0 {
						hint := "Add a project to get started"
						if !a.localActive() {
							hint = "The server lists its projects here"
						}
						ui.Text(c, hint).FontSize(12).TextColor(p.faint).MarginStart(8)
					}
					visible := len(projects)
					if !a.layout.projectsExpanded && visible > 5 {
						visible = 5
					}
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for _, dir := range projects[:visible] {
							a.projectRow(c, p, dir)
						}
						if visible < len(projects) || a.layout.projectsExpanded {
							a.moreProjectsRow(c, p, len(projects)-visible)
						}
					})
				})
			}
			if a.search.searchText != "" {
				a.searchResultsPane(c, p)
			}
		})

	})
}

// headerRow is the Codex switcher, approval bell and chats search.
func (a *app) headerRow(c *ui.Context, p palette, headerH float32) {
	ui.Row(c).FillWidth().Height(headerH).DragWindow().Padding(0, 12).AlignItems(ui.Center).Children(func() {
		title := ui.ButtonBase(c).Label("Codex").Height(34).Radius(9).Padding(0, 9).
			Justify(ui.Start).Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
			if m.Item("New chat").Chosen() {
				a.startNewChat()
			}
			if m.Item("Search chats…").Chosen() {
				a.openChatSearch()
			}
			m.Separator()
			if m.Item("Open folder…").Chosen() {
				a.openProjectDialog()
			}
		})
		if title.Hovered() {
			title.Background(p.hover)
		}
		title.Children(func() {
			ui.Text(c, "Codex").FontSize(15.5).FontWeight(600).SingleLine()
			ui.Icon(c, icChevDown).FontSize(11).MarginStart(5).TextColor(p.muted)
		})
		a.serverPill(c, p)

		ui.Spacer(c)

		a.bellButton(c, p)
		a.navButton(c, p, "Search", icSearch, a.openChatSearch, p.text)
	})
}

// serverPill is the server the app is talking to, its health dot and the
// menu that switches to any other one without a trip through Settings.
func (a *app) serverPill(c *ui.Context, p palette) {
	s := a.activeServerConn()
	h := a.connections.serverHealth[s.id]
	c.Key("server-pill")
	pill := ui.ButtonBase(c).Label("Server menu, "+s.name).Height(26).Radius(8).Padding(0, 8).
		Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
		for _, other := range a.connections.servers {
			item := m.Item(other.name)
			if other.id == a.connections.activeServer {
				item.Disabled(true)
			} else if item.Chosen() {
				a.useServer(other.id)
			}
		}
		m.Separator()
		if m.Item("Server settings…").Chosen() {
			a.openSettings(tabServers)
		}
	})
	if pill.Hovered() {
		pill.Background(p.hover)
	}
	pill.Children(func() {
		ui.Box(c).Width(7).Height(7).Radius(999).Background(healthTint(p, h.status)).Shrink(0)
		ui.Text(c, s.name).MarginStart(6).FontSize(12).TextColor(p.muted).
			SingleLine().Shrink(1).MinWidth(0)
		ui.Icon(c, icChevDown).FontSize(10).MarginStart(5).TextColor(p.faint).Shrink(0)
	})
}

// bellButton shows the approvals waiting: its badge counts them, and a
// click takes the user to the chat that asked.
func (a *app) bellButton(c *ui.Context, p palette) {
	asks := len(a.conversation.transcript.Pending())
	label := "Notifications"
	if asks > 0 {
		label = fmt.Sprintf("Notifications, %d approvals waiting", asks)
	}
	b := ui.ButtonBase(c).Label(label).Size(32, 32).Radius(9).Center().
		Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(p.hover)
	}
	if asks > 0 {
		b.OnClick(a.openAnAsk)
	}
	b.Children(func() {
		ui.Icon(c, icBell).FontSize(17).TextColor(p.text)
		if asks > 0 {
			ui.Box(c).MarginStart(-4).Height(15).Radius(8).Padding(3, 0).
				Background(p.danger).Children(func() {
				ui.Text(c, strconv.Itoa(asks)).FontSize(10.5).FontWeight(600).
					TextColor(p.dangerText)
			})
		}
	})
}

// openAnAsk takes the user to a chat with an approval waiting, the one
// the store lists first.
func (a *app) openAnAsk() {
	a.layout.settingsOpen = false
	keys := make([]chat.PermissionID, 0, len(a.conversation.transcript.Pending()))
	for k := range a.conversation.transcript.Pending() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	ask := a.conversation.transcript.Pending()[keys[0]]
	for _, session := range a.scope().sessions {
		if session.ID == string(ask.Session) {
			a.openConversation(session)
			return
		}
	}
	a.openConversation(opencode.Session{ID: string(ask.Session)})
}

// newChatRow starts a fresh chat.
func (a *app) newChatRow(c *ui.Context, p palette) {
	b := a.row(c, p, "New chat", false)
	b.Height(34)
	b.OnClick(a.startNewChat)
	b.Children(func() {
		ui.Icon(c, icPen).FontSize(16).TextColor(p.text)
		ui.Text(c, "New chat").MarginStart(10).FontSize(14).FontWeight(500).SingleLine()
	})
}

// searchField is the filter over the store's conversations: its results
// take the list over while a query stands.
func (a *app) searchField(c *ui.Context, p palette) {
	box := ui.Box(c).FillWidth().Height(30).Radius(9).
		Background(p.composerBg).Border(1, p.composerBorder).Padding(0, 8).
		Justify(ui.Center)
	if box.FocusWithin() {
		box.Border(1, p.composerBorderFocus)
	}
	box.Children(func() {
		ui.Row(c).Gap(5).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Icon(c, icSearch).FontSize(13).TextColor(p.muted)
			in := ui.TextInputBase(c, &a.search.searchText).FontSize(13).
				Placeholder("Search chats").FillWidth().Label("Search chats")
			if in.Changed() {
				a.reviseSearch()
			}
			if a.search.focusSearch {
				in.Focus()
				a.search.focusSearch = false
			}
			close := ui.ButtonBase(c).Label("Close search").Size(22, 22).Radius(6).Center().
				Tooltip("Close search")
			if close.Hovered() {
				close.Background(p.hover)
			}
			close.OnClick(a.closeChatSearch)
			close.Children(func() {
				ui.Icon(c, icClose).FontSize(12).TextColor(p.muted)
			})
		})
	})
}

func (a *app) closeChatSearch() {
	a.search.searchOpen = false
	a.search.focusSearch = false
	if a.search.searchText != "" {
		a.search.searchText = ""
		a.reviseSearch()
	}
}

func (a *app) sectionHeading(c *ui.Context, p palette, title string, add func()) {
	ui.Row(c).FillWidth().Height(28).Margin(5, 0, 1, 0).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(500).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		if add != nil {
			b := ui.ButtonBase(c).Label("Add "+title).Size(22, 22).Radius(6).Center().
				Tooltip("Add a project folder")
			if b.Hovered() {
				b.Background(p.hover)
			}
			b.OnClick(add)
			b.Children(func() { ui.Icon(c, icPlus).FontSize(13).TextColor(p.muted) })
		}
	})
}

func (a *app) pinnedSessions() []opencode.Session {
	out := make([]opencode.Session, 0, len(a.scope().pinned))
	order := make(map[string]int, len(a.scope().pinOrder))
	for i, id := range a.scope().pinOrder {
		order[id] = i
	}
	for _, s := range a.scope().sessions {
		if a.scope().pinned[s.ID] {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].ID] < order[out[j].ID] })
	return out
}

// projectDirectories is the folder list the sidebar and the picker offer,
// pinned ones first, whole per server: the local one's are the folders
// the user added, a remote server's are the folders its project report
// names plus the ones its chats actually sit in — a worktree copy, a
// folder it dropped. A remote chat has no business starting in one of
// this machine's folders, and the server has nothing to say about them.
func (a *app) projectDirectories() []string {
	projects := make([]string, 0, len(a.preferences.projects)+len(a.scope().projects))
	if a.localActive() {
		projects = append(projects, a.preferences.projects...)
	} else {
		projects = append(projects, a.scope().projects...)
		seen := map[string]bool{}
		for _, dir := range projects {
			seen[filepath.Clean(dir)] = true
		}
		for _, s := range a.scope().sessions {
			if s.Directory == "" || seen[filepath.Clean(s.Directory)] {
				continue
			}
			seen[filepath.Clean(s.Directory)] = true
			projects = append(projects, s.Directory)
		}
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return a.preferences.projectPins[projects[i]] && !a.preferences.projectPins[projects[j]]
	})
	return projects
}

func (a *app) addProject(dir string) {
	dir = canonDir(dir)
	for _, project := range a.preferences.projects {
		if filepath.Clean(project) == dir {
			delete(a.preferences.unavailableProjects, dir)
			return
		}
	}
	a.preferences.projects = append(a.preferences.projects, dir)
	delete(a.preferences.unavailableProjects, dir)
	a.saveState()
}

func (a *app) removeProject(dir string) {
	dir = canonDir(dir)
	for i, project := range a.preferences.projects {
		if project == dir {
			a.preferences.projects = append(a.preferences.projects[:i], a.preferences.projects[i+1:]...)
			delete(a.preferences.projectPins, dir)
			delete(a.preferences.unavailableProjects, dir)
			delete(a.layout.projectCollapsed, dir)
			delete(a.layout.projectChatsShown, dir)
			a.saveState()
			return
		}
	}
}

func (a *app) toggleProjectPin(dir string) {
	if a.preferences.projectPins == nil {
		a.preferences.projectPins = map[string]bool{}
	}
	if a.preferences.projectPins[dir] {
		delete(a.preferences.projectPins, dir)
	} else {
		a.preferences.projectPins[dir] = true
	}
	a.saveState()
}

// projectChatPreview is how many of a folder's chats show before the
// "Show more chats" row.
const projectChatPreview = 5

// projectRow is one folder of the Projects list, with its chats nested
// under it while it is open: clicking the row folds them away or back,
// and the hover + starts a fresh chat there.
func (a *app) projectRow(c *ui.Context, p palette, dir string) {
	name := dirBase(dir)
	chats := a.projectChats(dir)
	open := !a.layout.projectCollapsed[dir]
	label := "Show chats in " + name
	if open {
		label = "Hide chats in " + name
	}
	c.Key("project:" + dir)
	b := a.row(c, p, label, false)
	b.Height(30).Tooltip(dir)
	hovered := b.Hovered()
	b.OnClick(func() { a.toggleProjectOpen(dir) })
	b.Children(func() {
		if len(chats) > 0 {
			chev := icChevRight
			if open {
				chev = icChevDown
			}
			ui.Icon(c, chev).FontSize(12).TextColor(p.faint)
		} else {
			ui.Box(c).Width(12).Height(12)
		}
		ui.Icon(c, icFolder).FontSize(14).MarginStart(5).TextColor(p.muted)
		ui.Text(c, name).MarginStart(7).FontSize(13.5).Grow(1).SingleLine()
		if a.preferences.unavailableProjects[dir] {
			ui.Text(c, "Unavailable").FontSize(11).TextColor(p.muted)
		}
		if a.preferences.projectPins[dir] {
			ui.Text(c, "Pinned").FontSize(11).TextColor(p.muted)
		}
		if hovered {
			c.Key("projectnew:" + dir)
			add := ui.ButtonBase(c).Label("New chat in "+name).Size(22, 22).Radius(6).Center().Tooltip("New chat in " + name)
			add.OnClick(func() { a.startChatIn(dir) })
			if add.Hovered() {
				add.Background(p.hover)
			}
			add.Children(func() { ui.Icon(c, icPlus).FontSize(14).TextColor(p.muted) })
		}
		c.Key("projectmenu:" + dir)
		options := ui.ButtonBase(c).Label("Project options, "+name).Size(22, 22).Radius(6).Center()
		options.Menu(func(m *ui.Menu) {
			if m.Item("New chat").Chosen() {
				a.startChatIn(dir)
			}
			m.Separator()
			label := "Pin project"
			if a.preferences.projectPins[dir] {
				label = "Unpin project"
			}
			if m.Item(label).Chosen() {
				a.toggleProjectPin(dir)
			}
			if m.Item("Remove project").Chosen() {
				a.removeProject(dir)
			}
		})
		options.Children(func() { ui.Icon(c, icEllipsis).FontSize(13).TextColor(p.muted) })
	})

	if !open || len(chats) == 0 {
		return
	}
	visible := len(chats)
	if !a.layout.projectChatsShown[dir] && visible > projectChatPreview {
		visible = projectChatPreview
	}
	ui.Column(c).FillWidth().Gap(1).Children(func() {
		for _, s := range chats[:visible] {
			a.projectChatRow(c, p, s)
		}
		if len(chats) > projectChatPreview {
			a.moreProjectChatsRow(c, p, dir, len(chats)-projectChatPreview)
		}
	})
}

// projectChats lists the conversations working in one project's folder,
// newest first, the way the tree nests them.
func (a *app) projectChats(dir string) []opencode.Session {
	var chats []opencode.Session
	for _, s := range a.scope().sessions {
		if sameDir(s.Directory, dir) {
			chats = append(chats, s)
		}
	}
	return chats
}

// sameDir reports whether two paths name one folder.
func sameDir(one, two string) bool {
	return one != "" && two != "" && filepath.Clean(one) == filepath.Clean(two)
}

func (a *app) toggleProjectOpen(dir string) {
	if a.layout.projectCollapsed == nil {
		a.layout.projectCollapsed = map[string]bool{}
	}
	a.layout.projectCollapsed[dir] = !a.layout.projectCollapsed[dir]
}

// moreProjectChatsRow folds the rest of a folder's chats in or out.
func (a *app) moreProjectChatsRow(c *ui.Context, p palette, dir string, remaining int) {
	c.Key("projectchats:more:" + dir)
	label := fmt.Sprintf("Show %d more chats", remaining)
	if a.layout.projectChatsShown[dir] {
		label = "Show fewer chats"
	}
	b := a.row(c, p, label, false)
	b.Height(26).PaddingStart(30)
	b.OnClick(func() {
		if a.layout.projectChatsShown == nil {
			a.layout.projectChatsShown = map[string]bool{}
		}
		a.layout.projectChatsShown[dir] = !a.layout.projectChatsShown[dir]
	})
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(p.muted).SingleLine()
	})
}

func (a *app) moreProjectsRow(c *ui.Context, p palette, remaining int) {
	c.Key("projects:more")
	label := fmt.Sprintf("Show %d more projects", remaining)
	if a.layout.projectsExpanded {
		label = "Show fewer projects"
	}
	b := a.row(c, p, label, false)
	b.Height(28)
	b.OnClick(func() { a.layout.projectsExpanded = !a.layout.projectsExpanded })
	b.Children(func() {
		ui.Icon(c, icEllipsis).FontSize(14).TextColor(p.muted)
		ui.Text(c, label).MarginStart(9).FontSize(12.5).TextColor(p.muted).SingleLine()
	})
}

// searchResultsPane shows matching conversations only while a query is active.
func (a *app) searchResultsPane(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(24).Margin(8, 0, 0, 0).Padding(0, 8).Children(func() {
		ui.Text(c, "Results").FontSize(12).FontWeight(500).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		if a.search.searchLoading {
			ui.Spinner(c).Size(11, 11).Label("Searching chats")
		}
	})
	rows := a.search.searchResults
	empty := len(rows) == 0 && !a.search.searchLoading
	count := len(rows)
	if empty {
		count++
	}
	ui.List(c, &a.search.resultsList, count).Grow(1).FillWidth().Gap(2).
		ItemKey(func(i int) any {
			if i == len(rows) {
				return "footer"
			}
			return "session:" + rows[i].ID
		}).Rows(func(row ui.ListRow) {
		if row.Index < len(rows) {
			a.sessionRow(row.Context, p, rows[row.Index])
		} else {
			ui.Row(row.Context).Padding(4, 8).Children(func() {
				ui.Text(row.Context, "No chats match.").FontSize(13).TextColor(p.muted)
			})
		}
	})
}

// sessionRow is one real conversation among the pinned rows: choosing it
// swaps the transcript while the runner brings the stored one back, and
// the kebab at its end renames, pins or deletes it.
func (a *app) sessionRow(c *ui.Context, p palette, s opencode.Session) {
	a.chatRow(c, p, s, "session:", 8)
}

// projectChatRow nests one conversation under its project folder.
func (a *app) projectChatRow(c *ui.Context, p palette, s opencode.Session) {
	a.chatRow(c, p, s, "projectchat:"+s.Directory+":", 30)
}

// chatRow draws one conversation row in either place, indented by the
// tree it sits in.
func (a *app) chatRow(c *ui.Context, p palette, s opencode.Session, key string, indent float32) {
	label := s.Title
	if label == "" {
		label = "New chat"
	}
	c.Key(key + s.ID)
	b := a.row(c, p, label, s.ID == string(a.conversation.target.session))
	b.PaddingStart(indent)
	b.OnClick(func() {
		a.openConversation(s)
	})
	tint := p.faint
	if b.Hovered() {
		tint = p.muted
	}
	b.Children(func() {
		a.textRow(c, p, label)
		if s.ID == string(a.conversation.transcript.Busy()) {
			ui.Spinner(c).Size(13, 13).MarginStart(8).Label("Running")
		}
		c.Key(key + "menu:" + s.ID)
		kebab := ui.ButtonBase(c).Label("Conversation options, "+label).Size(22, 22).
			Radius(6).Center().Tooltip("Rename or delete this chat")
		kebab.Menu(func(m *ui.Menu) {
			if m.Item("Rename…").Chosen() {
				a.requestRename(s)
			}
			pinLabel := "Pin chat"
			if a.scope().pinned[s.ID] {
				pinLabel = "Unpin from Pinned"
			}
			if m.Item(pinLabel).Chosen() {
				a.togglePin(s)
			}
			if m.Item("Delete").Chosen() {
				a.confirmDelete(s)
			}
		})
		if kebab.Hovered() {
			kebab.Background(p.hover)
		}
		kebab.Children(func() {
			ui.Icon(c, icEllipsis).FontSize(13).TextColor(tint)
		})
	})
}

func (a *app) togglePin(s opencode.Session) {
	scope := a.scope()
	pins := scope.pinOrder
	if scope.pinned[s.ID] {
		pins = slices.DeleteFunc(pins, func(id string) bool { return id == s.ID })
	} else {
		pins = append(pins, s.ID)
		scope.sessions = dedupeSessions(scope.sessions, []opencode.Session{s})
	}
	scope.setPins(pins)
	a.saveState()
}

// sessionDialogs carries the dialogs opened by conversation rows.
func (a *app) sessionDialogs(c *ui.Context, p palette) {
	if st := a.layout.confirm; st != nil && st.open {
		title := st.what
		if title == "" {
			title = "New chat"
		}
		dialog(c, p, &st.open, 340, func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icTrash).FontSize(14).TextColor(p.orange)
				ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine()
			})
			ui.Text(c, "The conversation and what it did are gone for good.").
				FontSize(12.5).TextColor(p.muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
				cancel.OnClick(func() { a.layout.confirm = nil })
				cancel.Children(func() {
					ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
				})
				del := ui.ButtonBase(c).Label("Delete").Height(28).Radius(999).PaddingX(12).
					Background(p.orange)
				del.OnClick(a.deleteConfirmed)
				del.Children(func() {
					ui.Text(c, "Delete").FontSize(13).FontWeight(500).TextColor(p.dangerText)
				})
			})
		})
	}

	if st := a.layout.rename; st != nil && st.open {
		dialog(c, p, &st.open, 340, func() {
			ui.Text(c, "Rename this chat").FontSize(14).FontWeight(600)
			textField(c, p, &st.draft, "")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
				cancel.OnClick(func() { a.layout.rename = nil })
				cancel.Children(func() {
					ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
				})
				save := ui.PrimaryButton(c, "Save")
				save.OnClick(a.saveRename)
			})
		})
	}
}

// textRow is the label of a row without an icon.
func (a *app) textRow(c *ui.Context, p palette, label string) {
	ui.Text(c, label).FontSize(14).Grow(1).SingleLine()
}

// row builds the base of a sidebar row: a full-width rounded button tinted
// while hovered or, when chosen, held that way.
func (a *app) row(c *ui.Context, p palette, label string, chosen bool) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(32).Radius(9).Padding(0, 8).
		Cursor(ui.CursorPointer).FillWidth().Justify(ui.Start)
	if b.Hovered() {
		b.Background(p.hover)
	} else if chosen {
		b.Background(p.selected)
	}
	return b
}

// navButton is an icon button of the title bar row, tinted.
func (a *app) navButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func(), tint ui.Color) ui.Element {
	b := ui.ButtonBase(c).Label(label).Size(30, 30).Radius(8).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	if on != nil {
		b.OnClick(on)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(tint)
	})
	return b
}
