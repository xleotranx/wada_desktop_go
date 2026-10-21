package desktop

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

const navRailWidth float32 = 52

// navRail is the slim primary navigation beside the workspace sidebar: a
// column of icons on the window's material.
func (a *app) navRail(c *ui.Context, p palette) {
	rail := ui.Column(c).Width(navRailWidth).FillHeight()
	if !c.Vibrancy() {
		rail.Background(p.RailBg)
	}
	rail.Children(func() {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Gap(4).Padding(10, 7).Children(func() {
			a.railButton(c, p, "home", "Home", icHome, a.startNewChat)
			if a.hasWorkspaceFeatures() {
				a.railButton(c, p, "projects", "Projects", icStack, a.openProjectDialog)
			}
			a.railButton(c, p, "history", "History", icHistory, a.openHistory)
			c.Key("rail:more")
			more := ui.ButtonBase(c).Label("More").Size(36, 36).Radius(9).Center().
				Cursor(ui.CursorPointer).Tooltip("More navigation")
			if more.Hovered() {
				more.Background(p.Hover)
			}
			more.Menu(func(m *ui.Menu) {
				if m.Item("New chat").Chosen() {
					a.startNewChat()
				}
				if m.Item("Search chats…").Chosen() {
					a.openChatSearch()
				}
				if a.hasWorkspaceFeatures() && m.Item("Open folder…").Chosen() {
					a.openProjectDialog()
				}
				if m.Item("Command palette…").Chosen() {
					a.openCommandPalette()
				}
				if m.Item("Activity view").Chosen() {
					a.toggleActivity()
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
					a.toggleSidebar()
				}
			})
			more.Children(func() { ui.Icon(c, icEllipsis).FontSize(18).TextColor(p.Muted) })
			ui.Spacer(c)
			ui.Box(c).FillWidth().Height(1).Background(p.Hairline).Margin(0, 6)
			if a.hasFeatureAction("changes") {
				a.railButton(c, p, "changes", "Changes", icGitBranch, func() { a.runFeatureAction("changes") })
			}
			a.railButton(c, p, "tools", "Tools", icGrid, func() { a.openTool("") })
			a.settingsButton(c, p)
		})

	})
}

func (a *app) railButton(c *ui.Context, p palette, key, label string, icon *ui.SVG, on func()) {
	c.Key("rail:" + key)
	chosen := a.layout.selectedRail == key
	b := ui.ButtonBase(c).Label(label).Size(36, 36).Radius(9).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.Hover)
	} else if chosen {
		b.Background(p.Selected)
	}
	b.OnClick(func() {
		// Anything the rail navigates to leaves the settings page.
		a.layout.settingsOpen = false
		a.layout.selectedRail = key
		on()
	})
	b.Children(func() {
		// Onevra tints navigation icons with the accent; the chosen one
		// keeps it while the rest sit back in the muted text color.
		tint := p.Muted
		if chosen {
			tint = p.Orange
		}
		ui.Icon(c, icon).FontSize(18).TextColor(tint)
	})
}

// settingsButton is the rail's gear: the settings page is not one of the
// rail's places, so opening it keeps the selection the user lands back on.
func (a *app) settingsButton(c *ui.Context, p palette) {
	c.Key("rail:settings")
	b := ui.ButtonBase(c).Label("Settings").Size(36, 36).Radius(9).Center().
		Cursor(ui.CursorPointer).Tooltip("Settings")
	if b.Hovered() {
		b.Background(p.Hover)
	} else if a.layout.settingsOpen {
		b.Background(p.Selected)
	}
	b.OnClick(func() { a.openSettings("") })
	b.Children(func() {
		tint := p.Muted
		if a.layout.settingsOpen {
			tint = p.Orange
		}
		ui.Icon(c, icGear).FontSize(18).TextColor(tint)
	})
}

// openChatSearch aims the sidebar's search field and leaves the settings
// page behind.
func (a *app) openChatSearch() {
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.layout.sidebarCollapsed = false
	a.search.searchOpen = true
	a.search.focusSearch = true
}

// sidebarPane holds the workspace switcher, pinned chats and projects.
func (a *app) sidebarPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Width(a.layout.sidebarW).FillHeight().Background(p.SidebarBg).Children(func() {
		a.headerRow(c, p, headerH)

		ui.Column(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			if a.preferences.metadataError != "" {
				ui.Text(c, a.preferences.metadataError).FontSize(12).TextColor(p.Orange)
				if a.preferences.metadata != nil {
					if components.Button(c, "Retry saving", components.ButtonSecondary).Clicked() {
						a.saveState()
					}
				}
			}
			a.newChatRow(c, p)
			if a.search.searchOpen {
				a.searchField(c)
			}

			if a.search.searchText == "" {
				// The lists scroll once they outgrow the pane, under the
				// New chat and search rows, which stay put.
				ui.Scroll(c).Grow(1).FillWidth().Gap(2).Children(func() {
					if a.layout.sidebarFilter != "" {
						a.filteredChats(c, p)
						return
					}
					a.sectionHeading(c, p, "Pinned", nil)
					pins := a.pinnedSessions()
					if len(pins) == 0 {
						ui.Row(c).Height(27).Padding(0, 8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "No pinned chats").FontSize(12.5).TextColor(p.Faint).SingleLine()
						})
					} else {
						for _, s := range pins {
							a.pinnedSessionRow(c, p, s)
						}
					}

					addProjects := a.openProjectDialog
					if !a.localActive() {
						// A remote server's folders are its own disk's; the
						// system panel is this machine's to hand over.
						addProjects = func() { a.browseServerFolders(false) }
					}
					if !a.hasWorkspaceFeatures() {
						a.sectionHeading(c, p, "Chats", nil)
						for _, session := range a.listedSessions() {
							if !a.scope().pinned[session.ID] {
								a.sessionRow(c, p, session)
							}
						}
						if a.scope().nextCursor != "" {
							if components.Button(c, "Load older chats", components.ButtonSecondary).Clicked() {
								a.loadOlderChats()
							}
						}
						return
					}
					a.sectionHeading(c, p, "Projects", addProjects)
					projects := a.projectDirectories()
					if len(projects) == 0 {
						hint := "Add a project to get started"
						if !a.localActive() {
							hint = "The server lists its projects here"
						}
						ui.Text(c, hint).FontSize(12).TextColor(p.Faint).MarginStart(8)
					}
					visible := len(projects)
					if !a.layout.projectsExpanded && visible > 5 {
						visible = 5
					}
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for _, dir := range projects[:visible] {
							a.projectDragRow(c, p, dir)
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
			if a.hasWorkspaceFeatures() && m.Item("Open folder…").Chosen() {
				a.openProjectDialog()
			}
			m.Separator()
			if m.Item("Show unread chats").Chosen() {
				a.setSidebarFilter(sidebarFilterUnread)
			}
			if m.Item("Show chats with drafts").Chosen() {
				a.setSidebarFilter(sidebarFilterDrafts)
			}
			if m.Item("Show chats needing attention").Chosen() {
				a.setSidebarFilter(sidebarFilterAttention)
			}
			if m.Item("Show archived chats").Chosen() {
				a.setSidebarFilter(sidebarFilterArchived)
			}
			if m.Item("Show all chats").Chosen() {
				a.setSidebarFilter("")
			}
		})
		if title.Hovered() {
			title.Background(p.Hover)
		}
		title.Children(func() {
			ui.Box(c).Size(22, 22).Radius(7).Center().Shrink(0).
				Background(c.Theme().Selection.Alpha(.7)).Children(func() {
				ui.Icon(c, icSparkle).FontSize(12).TextColor(p.Orange)
			})
			ui.Text(c, "Codex").MarginStart(7).FontSize(15.5).FontWeight(600).SingleLine()
			ui.Icon(c, icChevDown).FontSize(11).MarginStart(5).TextColor(p.Muted)
		})
		a.serverPill(c, p)

		ui.Spacer(c)

		a.bellButton(c, p)
		a.navButton(c, p, "Search", icSearch, a.openChatSearch, p.Text)
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
		pill.Background(p.Hover)
	}
	pill.Children(func() {
		ui.Box(c).Width(7).Height(7).Radius(999).Background(healthTint(p, h.status)).Shrink(0)
		ui.Text(c, s.name).MarginStart(6).FontSize(12).TextColor(p.Muted).
			SingleLine().Shrink(1).MinWidth(0)
		ui.Icon(c, icChevDown).FontSize(10).MarginStart(5).TextColor(p.Faint).Shrink(0)
	})
}

// bellButton shows the approvals waiting: its badge counts them, and a
// click opens the notification center under it.
func (a *app) bellButton(c *ui.Context, p palette) {
	asks := len(a.conversation.transcript.Pending())
	attention, unread := a.unreadCounts()
	total := asks + unread
	b := ui.ButtonBase(c).Label(a.bellLabel()).Size(32, 32).Radius(9).Center().
		Cursor(ui.CursorPointer).Tooltip("Notifications")
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.layout.bellOpen = !a.layout.bellOpen })
	b.Children(func() {
		ui.Icon(c, icBell).FontSize(17).TextColor(p.Text)
		if total > 0 {
			tint := p.Orange
			if asks > 0 || attention > 0 {
				tint = p.Danger
			}
			ui.Box(c).MarginStart(-4).Height(15).Radius(8).Padding(3, 0).
				Background(tint).Children(func() {
				ui.Text(c, strconv.Itoa(total)).FontSize(10.5).FontWeight(600).
					TextColor(p.DangerText)
			})
		}
	})
	a.bellPanel(c, p, b)
}

// newChatRow starts a fresh chat.
func (a *app) newChatRow(c *ui.Context, p palette) {
	b := a.row(c, p, "New chat", false)
	b.Height(36)
	b.OnClick(a.startNewChat)
	if draft := a.draftFor(""); strings.TrimSpace(draft) != "" {
		b.Tooltip("Draft: " + draftSnippet(draft))
	}
	b.Children(func() {
		ui.Icon(c, icPen).FontSize(15).TextColor(p.Orange)
		ui.Text(c, "New chat").MarginStart(10).FontSize(13.5).FontWeight(500).SingleLine()
		if a.hasDraft("") {
			ui.Spacer(c)
			ui.Box(c).Height(18).Radius(999).Padding(2, 7).
				Background(p.DetailBg).Children(func() {
				ui.Text(c, "Draft").FontSize(10.5).TextColor(p.Muted).SingleLine()
			})
		}
	})
}

// searchField is the filter over the store's conversations: its results
// take the list over while a query stands. Onevra's text box carries the
// magnifier, the theme's focus ring and the compact clear control.
func (a *app) searchField(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
		c.Key("chat-search")
		in := components.TextBox(c, &a.search.searchText,
			components.TextBoxOptions{LeadingIcon: icSearch}).
			FillWidth().Label("Search chats").Placeholder("Search chats").FontSize(13)
		in.OnTextKey(func(k ui.TextKey) bool {
			rows := a.search.searchResults
			if k.Key == ui.KeyEnter {
				if len(rows) > 0 && !a.search.searchLoading {
					index := a.search.searchSelected
					if index < 0 || index >= len(rows) {
						index = 0
					}
					a.openConversation(rows[index])
				}
				return true
			}
			if k.Key == ui.KeyDown && len(rows) > 0 {
				a.search.searchSelected = min(a.search.searchSelected+1, len(rows)-1)
				a.search.resultsList.ScrollTo(a.search.searchSelected, ui.Center)
				return true
			}
			if k.Key == ui.KeyUp && len(rows) > 0 {
				if a.search.searchSelected < 0 {
					a.search.searchSelected = len(rows) - 1
				} else {
					a.search.searchSelected = max(a.search.searchSelected-1, 0)
				}
				a.search.resultsList.ScrollTo(a.search.searchSelected, ui.Center)
				return true
			}
			return false
		})
		if in.Changed() {
			a.reviseSearch()
		}
		if a.search.focusSearch {
			in.Focus()
			a.search.focusSearch = false
		}
		if components.IconButton(c, icClose, "Close search", components.IconButtonCompact).Clicked() {
			a.closeChatSearch()
		}
	})
}

func (a *app) closeChatSearch() {
	a.search.searchOpen = false
	a.search.focusSearch = false
	a.search.searchSelected = -1
	if a.search.searchText != "" {
		a.search.searchText = ""
		a.reviseSearch()
	}
}

func (a *app) sectionHeading(c *ui.Context, p palette, title string, add func()) {
	ui.Row(c).FillWidth().Height(28).Margin(6, 0, 1, 0).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(p.Muted).SingleLine()
		ui.Spacer(c)
		if add != nil {
			b := ui.ButtonBase(c).Label("Add "+title).Size(22, 22).Radius(6).Center().
				Tooltip("Add a project folder")
			if b.Hovered() {
				b.Background(p.Hover)
			}
			b.OnClick(add)
			b.Children(func() { ui.Icon(c, icPlus).FontSize(13).TextColor(p.Muted) })
		}
	})
}

func (a *app) pinnedSessions() []agent.Session {
	out := make([]agent.Session, 0, len(a.scope().pinned))
	order := make(map[string]int, len(a.scope().pinOrder))
	for i, id := range a.scope().pinOrder {
		order[id] = i
	}
	for _, s := range a.scope().sessions {
		if a.scope().pinned[s.ID] && !a.chatArchived(s.ID) {
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

// copyFolderPath puts one workspace folder's path on the clipboard.
func (a *app) copyFolderPath(dir string) {
	if err := a.runtime.copy(dir); err != nil {
		a.notify(ui.Toast{Title: "Could not copy the folder path", Description: err.Error()})
		return
	}
	a.notify(ui.Toast{Title: "Folder path copied"})
}

// moveProjectTo puts a dragged project where another one sits. Projects
// stay in their pinned group: crossing the line would only snap back, so
// it says why instead.
func (a *app) moveProjectTo(dir, target string) {
	if !a.localActive() {
		return
	}
	from, to := -1, -1
	for i, project := range a.preferences.projects {
		if sameDir(project, dir) {
			from = i
		}
		if sameDir(project, target) {
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	if a.preferences.projectPins[dir] != a.preferences.projectPins[target] {
		a.notify(ui.Toast{Title: "Pinned projects stay on top",
			Description: "Unpin one of them to move it below the others."})
		return
	}
	projects := slices.Clone(a.preferences.projects)
	project := projects[from]
	projects = slices.Delete(projects, from, from+1)
	projects = slices.Insert(projects, min(to, len(projects)), project)
	a.preferences.projects = projects
	a.saveState()
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
			delete(a.preferences.projectLinks, dir)
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
func (a *app) projectRow(c *ui.Context, p palette, dir string) ui.Element {
	name := dirBase(dir)
	folders := a.projectFolders(dir)
	chats := a.projectChats(dir)
	open := !a.layout.projectCollapsed[dir]
	label := "Show chats in " + name
	if open {
		label = "Hide chats in " + name
	}
	c.Key("project:" + dir)
	active := false
	for _, s := range chats {
		if s.ID == string(a.conversation.target.session) {
			active = true
			break
		}
	}
	b := a.row(c, p, label, active)
	b.Height(30).Tooltip(strings.Join(folders, "\n"))
	hovered := b.Hovered()
	b.OnClick(func() { a.toggleProjectOpen(dir) })
	b.Children(func() {
		if len(chats) > 0 {
			chev := icChevRight
			if open {
				chev = icChevDown
			}
			ui.Icon(c, chev).FontSize(12).TextColor(p.Faint)
		} else {
			ui.Box(c).Width(12).Height(12)
		}
		ui.Icon(c, icFolder).FontSize(14).MarginStart(5).TextColor(p.Muted)
		ui.Text(c, name).MarginStart(7).FontSize(13.5).Grow(1).SingleLine()
		if summary := projectFolderSummary(folders); summary != "" {
			ui.Text(c, summary).FontSize(10.5).TextColor(p.Faint).Shrink(0)
		}
		if a.preferences.unavailableProjects[dir] {
			ui.Text(c, "Unavailable").FontSize(11).TextColor(p.Muted)
		}
		if a.preferences.projectPins[dir] {
			ui.Text(c, "Pinned").FontSize(11).TextColor(p.Muted)
		}
		if hovered {
			c.Key("projectnew:" + dir)
			add := ui.ButtonBase(c).Label("New chat in "+name).Size(22, 22).Radius(6).Center().Tooltip("New chat in " + name)
			add.OnClick(func() { a.startChatIn(dir) })
			if add.Hovered() {
				add.Background(p.Hover)
			}
			add.Children(func() { ui.Icon(c, icPlus).FontSize(14).TextColor(p.Muted) })
		}
		c.Key("projectmenu:" + dir)
		options := ui.ButtonBase(c).Label("Project options, "+name).Size(22, 22).Radius(6).Center()
		options.Menu(func(m *ui.Menu) {
			if m.Item("New chat").Chosen() {
				a.startChatIn(dir)
			}
			for _, folder := range folders[1:] {
				if m.Item("New chat in " + dirBase(folder)).Chosen() {
					a.startChatIn(folder)
				}
			}
			if a.localActive() {
				m.Separator()
				if m.Item("Add folder…").Chosen() {
					a.beginLinkProjectFolder(dir)
				}
				for _, folder := range folders[1:] {
					if m.Item("Unlink " + dirBase(folder)).Chosen() {
						a.unlinkProjectFolder(dir, folder)
					}
				}
			}
			m.Separator()
			label := "Pin project"
			if a.preferences.projectPins[dir] {
				label = "Unpin project"
			}
			if m.Item(label).Chosen() {
				a.toggleProjectPin(dir)
			}
			if a.localActive() {
				m.Separator()
				if m.Item("Copy folder path").Chosen() {
					a.copyFolderPath(dir)
				}
				if m.Item("Show in Finder").Chosen() {
					a.openLocalPath(c, dir)
				}
			}
			if m.Item("Remove project").Chosen() {
				a.removeProject(dir)
			}
		})
		options.Children(func() { ui.Icon(c, icEllipsis).FontSize(13).TextColor(p.Muted) })
	})

	if !open || len(chats) == 0 {
		return b
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
	return b
}

// projectDragRow lets a local project be dragged over another to change
// the list's order. A server's projects come from its own report and have
// nothing here to reorder.
func (a *app) projectDragRow(c *ui.Context, p palette, dir string) {
	row := a.projectRow(c, p, dir)
	if !a.localActive() {
		return
	}
	row.Drag(dir)
	if dragged, over := ui.DragOver[string](row); over && !sameDir(dragged, dir) {
		row.Background(p.Hover).Border(1, p.Orange.Alpha(.55))
	}
	if dragged, ok := ui.Drop[string](row); ok {
		a.moveProjectTo(dragged, dir)
	}
	if row.Dragging() {
		row.Opacity(.55)
	}
}

// projectChats lists the conversations working in one project's folder,
// newest first, the way the tree nests them.
func (a *app) projectChats(dir string) []agent.Session {
	folders := a.projectFolders(dir)
	var chats []agent.Session
	for _, s := range a.scope().sessions {
		if a.chatArchived(s.ID) {
			continue
		}
		if slices.ContainsFunc(folders, func(folder string) bool { return sameDir(s.Directory, folder) }) {
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
		ui.Text(c, label).FontSize(12.5).TextColor(p.Muted).SingleLine()
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
		ui.Icon(c, icEllipsis).FontSize(14).TextColor(p.Muted)
		ui.Text(c, label).MarginStart(9).FontSize(12.5).TextColor(p.Muted).SingleLine()
	})
}

// searchResultsPane shows matching conversations only while a query is active.
func (a *app) searchResultsPane(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(24).Margin(8, 0, 0, 0).Padding(0, 8).Children(func() {
		ui.Text(c, "Results").FontSize(12).FontWeight(500).TextColor(p.Muted).SingleLine()
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
			a.chatRow(row.Context, p, rows[row.Index], "session:", 8, row.Index == a.search.searchSelected)
		} else {
			ui.Row(row.Context).Padding(4, 8).Children(func() {
				ui.Text(row.Context, "No chats match.").FontSize(13).TextColor(p.Muted)
			})
		}
	})
}

// sessionRow is one real conversation among the pinned rows: choosing it
// swaps the transcript while the runner brings the stored one back, and
// the kebab at its end renames, pins or deletes it.
func (a *app) sessionRow(c *ui.Context, p palette, s agent.Session) {
	a.chatRow(c, p, s, "session:", 8, false)
}

// projectChatRow nests one conversation under its project folder.
func (a *app) projectChatRow(c *ui.Context, p palette, s agent.Session) {
	a.chatRow(c, p, s, "projectchat:"+s.Directory+":", 30, false)
}

// chatRow draws one conversation row in either place, indented by the
// tree it sits in.
func (a *app) chatRow(c *ui.Context, p palette, s agent.Session, key string, indent float32, highlight bool) ui.Element {
	label := s.Title
	if label == "" {
		label = "New chat"
	}
	c.Key(key + s.ID)
	b := a.row(c, p, label, s.ID == string(a.conversation.target.session) || highlight)
	b.PaddingStart(indent)
	b.OnClick(func() {
		a.openConversation(s)
	})
	tint := p.Faint
	if b.Hovered() {
		tint = p.Muted
	}
	b.Children(func() {
		iconTint := p.Muted
		if s.ID == string(a.conversation.target.session) {
			iconTint = p.Orange
		}
		ui.Icon(c, icChat).FontSize(13).TextColor(iconTint).Shrink(0)
		ui.Text(c, label).MarginStart(8).FontSize(13.5).Grow(1).MinWidth(0).
			SingleLine().Ellipsis("…")
		if primary := a.projectPrimaryFor(s.Directory); primary != "" && !sameDir(primary, s.Directory) {
			ui.Text(c, dirBase(s.Directory)).FontSize(10.5).TextColor(p.Faint).Shrink(0)
		}
		if draft := a.draftFor(s.ID); strings.TrimSpace(draft) != "" {
			ui.Icon(c, icPen).FontSize(11).MarginStart(6).TextColor(p.Faint).
				Tooltip("Draft: " + draftSnippet(draft))
		}
		if scope := a.scope(); scope.unread[s.ID] {
			unreadDot(c, p, scope.attention[s.ID])
		}
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
			unreadLabel := "Mark as unread"
			if a.scope().unread[s.ID] {
				unreadLabel = "Mark as read"
			}
			if m.Item(unreadLabel).Chosen() {
				a.toggleSessionUnread(s)
			}
			archiveLabel := "Archive chat"
			if a.chatArchived(s.ID) {
				archiveLabel = "Unarchive chat"
			}
			if m.Item(archiveLabel).Chosen() {
				a.toggleArchiveChat(s)
			}
			if m.Item("Delete").Chosen() {
				a.confirmDelete(s)
			}
		})
		if kebab.Hovered() {
			kebab.Background(p.Hover)
		}
		kebab.Children(func() {
			ui.Icon(c, icEllipsis).FontSize(13).TextColor(tint)
		})
	})
	return b
}

// pinnedSessionRow is one row of the Pinned section: the chat's own row,
// which also drags within the section to reorder the pins.
func (a *app) pinnedSessionRow(c *ui.Context, p palette, s agent.Session) {
	row := a.chatRow(c, p, s, "session:", 8, false)
	row.Drag(s.ID)
	if dragged, over := ui.DragOver[string](row); over && dragged != s.ID {
		row.Background(p.Hover).Border(1, p.Orange.Alpha(.55))
	}
	if dragged, ok := ui.Drop[string](row); ok {
		a.movePinTo(dragged, s.ID)
	}
	if row.Dragging() {
		row.Opacity(.55)
	}
}

// movePinTo puts a dragged pinned chat where another one sits; the rest
// keep their order.
func (a *app) movePinTo(id, target string) {
	scope := a.scope()
	from, to := -1, -1
	for i, pinned := range scope.pinOrder {
		if pinned == id {
			from = i
		}
		if pinned == target {
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	order := slices.Clone(scope.pinOrder)
	pin := order[from]
	order = slices.Delete(order, from, from+1)
	order = slices.Insert(order, min(to, len(order)), pin)
	scope.setPins(order)
	a.saveState()
}

func (a *app) togglePin(s agent.Session) {
	scope := a.scope()
	pins := scope.pinOrder
	if scope.pinned[s.ID] {
		pins = slices.DeleteFunc(pins, func(id string) bool { return id == s.ID })
	} else {
		pins = append(pins, s.ID)
		scope.sessions = dedupeSessions(scope.sessions, []agent.Session{s})
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
		dialog(c, p, &st.open, 340, "Delete conversation", func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icTrash).FontSize(14).TextColor(p.Orange)
				ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine()
			})
			ui.Text(c, "The conversation and what it did are gone for good.").
				FontSize(12.5).TextColor(p.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
					a.layout.confirm = nil
				}
				if components.Button(c, "Delete", components.ButtonPrimary).Clicked() {
					a.deleteConfirmed()
				}
			})
		})
	}

	if st := a.layout.rename; st != nil && st.open {
		dialog(c, p, &st.open, 340, "Rename this chat", func() {
			ui.Text(c, "Rename this chat").FontSize(14).FontWeight(600)
			textField(c, "New title", &st.draft, "")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
					a.layout.rename = nil
				}
				if components.Button(c, "Save", components.ButtonPrimary).Clicked() {
					a.saveRename()
				}
			})
		})
	}
}

// row builds the base of a sidebar row: a full-width rounded button tinted
// while hovered or, when chosen, held that way.
func (a *app) row(c *ui.Context, p palette, label string, chosen bool) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(34).Radius(8).Padding(0, 8).
		Cursor(ui.CursorPointer).FillWidth().Justify(ui.Start)
	if b.Hovered() {
		b.Background(p.Hover)
	} else if chosen {
		b.Background(p.Selected)
	}
	return b
}

// navButton is an icon button of the title bar row, tinted.
func (a *app) navButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func(), tint ui.Color) ui.Element {
	b := ui.ButtonBase(c).Label(label).Size(30, 30).Radius(8).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	if on != nil {
		b.OnClick(on)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(tint)
	})
	return b
}
