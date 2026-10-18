package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"

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
			a.railButton(c, p, "agents", "Agents", icAgent, func() { a.openTool("Side chat") })
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
				if a.sidebarCollapsed {
					label = "Show sidebar"
				}
				if m.Item(label).Chosen() {
					a.sidebarCollapsed = !a.sidebarCollapsed
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
	} else if a.selectedRail == key {
		b.Background(p.selected)
	}
	b.OnClick(func() {
		// Anything the rail navigates to leaves the settings page.
		a.settingsOpen = false
		a.selectedRail = key
		on()
	})
	b.Children(func() {
		tint := p.muted
		if a.selectedRail == key {
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
	} else if a.settingsOpen {
		b.Background(p.selected)
	}
	b.OnClick(func() { a.openSettings("") })
	b.Children(func() {
		tint := p.muted
		if a.settingsOpen {
			tint = p.text
		}
		ui.Icon(c, icGear).FontSize(18).TextColor(tint)
	})
}

// openChatSearch aims the sidebar's search field and leaves the settings
// page behind.
func (a *app) openChatSearch() {
	a.settingsOpen = false
	a.searchOpen = true
	a.focusSearch = true
}

// sidebarPane holds the workspace switcher, pinned chats and projects.
func (a *app) sidebarPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Width(a.sidebarW).Background(p.sidebarBg).Children(func() {
		a.headerRow(c, p, headerH)

		ui.Column(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			if a.metadataError != "" {
				ui.Text(c, a.metadataError).FontSize(12).TextColor(p.orange)
				if a.metadata != nil {
					ui.Button(c, "Retry saving").OnClick(a.saveState)
				}
			} else if a.metadataSaving {
				ui.Text(c, "Saving…").FontSize(12).TextColor(p.muted)
			}
			a.newChatRow(c, p)
			if a.searchOpen {
				a.searchField(c, p)
			}

			if a.searchText == "" {
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
						// A remote server's projects come with its list;
						// there is no folder of this machine to add.
						addProjects = nil
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
					if !a.projectsExpanded && visible > 5 {
						visible = 5
					}
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for _, dir := range projects[:visible] {
							a.projectRow(c, p, dir)
						}
						if visible < len(projects) || a.projectsExpanded {
							a.moreProjectsRow(c, p, len(projects)-visible)
						}
					})
				})
			}
			if a.searchText != "" {
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
	h := a.serverHealth[s.id]
	c.Key("server-pill")
	pill := ui.ButtonBase(c).Label("Server menu, "+s.name).Height(26).Radius(8).Padding(0, 8).
		Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
		for _, other := range a.servers {
			item := m.Item(other.name)
			if other.id == a.activeServer {
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
	asks := len(a.pending)
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
	a.settingsOpen = false
	keys := make([]string, 0, len(a.pending))
	for k := range a.pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ask := a.pending[keys[0]]
	for i := range a.scope().sessions {
		if a.scope().sessions[i].id == ask.session {
			a.adoptSession(a.scope().sessions[i])
			break
		}
	}
	if a.selectedSession != ask.session {
		// The conversation sits past the list's first page; open it by
		// hand and let the restore name it.
		a.selectedSession = ask.session
		a.selected = ""
		a.freshTranscript()
	}
	select {
	case a.jobs <- agentJob{kind: jobOpen, sid: ask.session}:
	default:
	}
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
			in := ui.TextInputBase(c, &a.searchText).FontSize(13).
				Placeholder("Search chats").FillWidth().Label("Search chats")
			if in.Changed() {
				a.reviseSearch()
			}
			if a.focusSearch {
				in.Focus()
				a.focusSearch = false
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
	a.searchOpen = false
	a.focusSearch = false
	if a.searchText != "" {
		a.searchText = ""
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

func (a *app) pinnedSessions() []agentSession {
	out := make([]agentSession, 0, len(a.pinned))
	order := make(map[string]int, len(a.pinOrder))
	for i, id := range a.pinOrder {
		order[id] = i
	}
	for _, s := range a.scope().sessions {
		if a.pinned[s.id] {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].id] < order[out[j].id] })
	return out
}

// projectDirectories is the folder list the sidebar and the picker offer,
// pinned ones first, whole per server: the local one's are the folders
// the user added, a remote server's are the folders its project report
// names plus the ones its chats actually sit in — a worktree copy, a
// folder it dropped. A remote chat has no business starting in one of
// this machine's folders, and the server has nothing to say about them.
func (a *app) projectDirectories() []string {
	projects := make([]string, 0, len(a.projects)+len(a.scope().projects))
	if a.localActive() {
		projects = append(projects, a.projects...)
	} else {
		projects = append(projects, a.scope().projects...)
		seen := map[string]bool{}
		for _, dir := range projects {
			seen[filepath.Clean(dir)] = true
		}
		for _, s := range a.scope().sessions {
			if s.dir == "" || seen[filepath.Clean(s.dir)] {
				continue
			}
			seen[filepath.Clean(s.dir)] = true
			projects = append(projects, s.dir)
		}
	}
	sort.SliceStable(projects, func(i, j int) bool { return a.projectPins[projects[i]] && !a.projectPins[projects[j]] })
	return projects
}

// mergeFolderLists keeps one row per directory the two lists name: the
// ones already kept stay in place, and the fresh list adds only what is
// new, its own order.
func mergeFolderLists(kept, fresh []string) []string {
	seen := map[string]bool{}
	for _, dir := range kept {
		seen[filepath.Clean(dir)] = true
	}
	out := kept
	for _, dir := range fresh {
		if seen[filepath.Clean(dir)] {
			continue
		}
		seen[filepath.Clean(dir)] = true
		out = append(out, dir)
	}
	return out
}

func (a *app) addProject(dir string) {
	dir = canonDir(dir)
	for _, project := range a.projects {
		if filepath.Clean(project) == dir {
			delete(a.unavailableProjects, dir)
			return
		}
	}
	a.projects = append(a.projects, dir)
	delete(a.unavailableProjects, dir)
	a.saveState()
}

func (a *app) removeProject(dir string) {
	dir = canonDir(dir)
	for i, project := range a.projects {
		if project == dir {
			a.projects = append(a.projects[:i], a.projects[i+1:]...)
			delete(a.projectPins, dir)
			delete(a.unavailableProjects, dir)
			delete(a.projectCollapsed, dir)
			delete(a.projectChatsShown, dir)
			a.saveState()
			return
		}
	}
}

func (a *app) toggleProjectPin(dir string) {
	if a.projectPins == nil {
		a.projectPins = map[string]bool{}
	}
	if a.projectPins[dir] {
		delete(a.projectPins, dir)
	} else {
		a.projectPins[dir] = true
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
	open := !a.projectCollapsed[dir]
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
		if a.unavailableProjects[dir] {
			ui.Text(c, "Unavailable").FontSize(11).TextColor(p.muted)
		}
		if a.projectPins[dir] {
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
			if a.projectPins[dir] {
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
	if !a.projectChatsShown[dir] && visible > projectChatPreview {
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
func (a *app) projectChats(dir string) []agentSession {
	var chats []agentSession
	for _, s := range a.scope().sessions {
		if sameDir(s.dir, dir) {
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
	if a.projectCollapsed == nil {
		a.projectCollapsed = map[string]bool{}
	}
	a.projectCollapsed[dir] = !a.projectCollapsed[dir]
}

// moreProjectChatsRow folds the rest of a folder's chats in or out.
func (a *app) moreProjectChatsRow(c *ui.Context, p palette, dir string, remaining int) {
	c.Key("projectchats:more:" + dir)
	label := fmt.Sprintf("Show %d more chats", remaining)
	if a.projectChatsShown[dir] {
		label = "Show fewer chats"
	}
	b := a.row(c, p, label, false)
	b.Height(26).PaddingStart(30)
	b.OnClick(func() {
		if a.projectChatsShown == nil {
			a.projectChatsShown = map[string]bool{}
		}
		a.projectChatsShown[dir] = !a.projectChatsShown[dir]
	})
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(p.muted).SingleLine()
	})
}

func (a *app) moreProjectsRow(c *ui.Context, p palette, remaining int) {
	c.Key("projects:more")
	label := fmt.Sprintf("Show %d more projects", remaining)
	if a.projectsExpanded {
		label = "Show fewer projects"
	}
	b := a.row(c, p, label, false)
	b.Height(28)
	b.OnClick(func() { a.projectsExpanded = !a.projectsExpanded })
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
		if a.searchLoading {
			ui.Spinner(c).Size(11, 11).Label("Searching chats")
		}
	})
	rows := a.searchResults
	empty := len(rows) == 0 && !a.searchLoading
	count := len(rows)
	if empty {
		count++
	}
	ui.List(c, &a.resultsList, count).Grow(1).FillWidth().Gap(2).
		ItemKey(func(i int) any {
			if i == len(rows) {
				return "footer"
			}
			return "session:" + rows[i].id
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
func (a *app) sessionRow(c *ui.Context, p palette, s agentSession) {
	a.chatRow(c, p, s, "session:", 8)
}

// projectChatRow nests one conversation under its project folder.
func (a *app) projectChatRow(c *ui.Context, p palette, s agentSession) {
	a.chatRow(c, p, s, "projectchat:"+s.dir+":", 30)
}

// chatRow draws one conversation row in either place, indented by the
// tree it sits in.
func (a *app) chatRow(c *ui.Context, p palette, s agentSession, key string, indent float32) {
	label := s.title
	if label == "" {
		label = "New chat"
	}
	c.Key(key + s.id)
	b := a.row(c, p, label, s.id == a.selectedSession)
	b.PaddingStart(indent)
	b.OnClick(func() {
		a.adoptSession(s)
		select {
		case a.jobs <- agentJob{kind: jobOpen, sid: s.id}:
		default:
		}
	})
	tint := p.faint
	if b.Hovered() {
		tint = p.muted
	}
	b.Children(func() {
		a.textRow(c, p, label)
		if s.id == a.busySession {
			ui.Spinner(c).Size(13, 13).MarginStart(8).Label("Running")
		}
		c.Key(key + "menu:" + s.id)
		kebab := ui.ButtonBase(c).Label("Conversation options, "+label).Size(22, 22).
			Radius(6).Center().Tooltip("Rename or delete this chat")
		kebab.Menu(func(m *ui.Menu) {
			if m.Item("Rename…").Chosen() {
				a.requestRename(s)
			}
			pinLabel := "Pin chat"
			if a.pinned[s.id] {
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

func (a *app) togglePin(s agentSession) {
	if a.pinned == nil {
		a.pinned = map[string]bool{}
	}
	if a.pinned[s.id] {
		delete(a.pinned, s.id)
		a.pinOrder = slices.DeleteFunc(a.pinOrder, func(id string) bool { return id == s.id })
	} else {
		a.pinned[s.id] = true
		a.pinOrder = append(a.pinOrder, s.id)
		a.scope().sessions = dedupeSessions(a.scope().sessions, []agentSession{s})
	}
	// The active server's pins live in both shapes: the map the sidebar
	// draws from and the store's per-server list.
	a.pinsByServer[a.activeServer] = slices.Clone(a.pinOrder)
	a.saveState()
}

// sessionDialogs carries the dialogs opened by conversation rows.
func (a *app) sessionDialogs(c *ui.Context, p palette) {
	if st := a.confirm; st != nil && st.open {
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
				cancel.OnClick(func() { a.confirm = nil })
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

	if st := a.rename; st != nil && st.open {
		dialog(c, p, &st.open, 340, func() {
			ui.Text(c, "Rename this chat").FontSize(14).FontWeight(600)
			textField(c, p, &st.draft, "")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
				cancel.OnClick(func() { a.rename = nil })
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
