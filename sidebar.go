package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
)

const navRailWidth float32 = 52

func titlebarInset(barLeft float32) float32 {
	inset := barLeft - navRailWidth + 8
	if inset < 12 {
		return 12
	}
	return inset
}

// navRail is the slim primary navigation beside the workspace sidebar.
func (a *app) navRail(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Width(navRailWidth).FillHeight().Background(p.railBg).Children(func() {
		ui.Row(c).Height(headerH).DragWindow()
		ui.Column(c).Grow(1).AlignItems(ui.Center).Gap(5).Padding(6, 7).Children(func() {
			if a.sidebarCollapsed {
				c.Key("rail:show-sidebar")
				show := ui.ButtonBase(c).Label("Show sidebar").Size(36, 36).Radius(10).Center().
					Cursor(ui.CursorPointer).Tooltip("Show sidebar")
				if show.Hovered() {
					show.Background(p.hover)
				}
				show.OnClick(func() { a.sidebarCollapsed = false })
				show.Children(func() { ui.Icon(c, icPanel).FontSize(18).TextColor(p.muted) })
			}
			a.railButton(c, p, "home", "Home", icHome, a.startNewChat)
			a.railButton(c, p, "projects", "Projects", icStack, a.openFolderDialog)
			a.railButton(c, p, "history", "History", icHistory, func() {
				a.searchOpen = true
				a.focusSearch = true
			})
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
					a.searchOpen = true
					a.focusSearch = true
				}
				if m.Item("Open folder…").Chosen() {
					a.openFolderDialog()
				}
				m.Separator()
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

// sidebarPane holds the workspace switcher, pinned chats, projects and recents.
func (a *app) sidebarPane(c *ui.Context, p palette, headerH, barLeft float32) {
	ui.Column(c).Width(a.sidebarW).Background(p.sidebarBg).Children(func() {
		a.headerRow(c, p, headerH, titlebarInset(barLeft))

		ui.Column(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			a.newChatRow(c, p)
			if a.searchOpen {
				a.searchField(c, p)
			}

			if a.searchText == "" {
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

				a.sectionHeading(c, p, "Projects", func() { a.openFolderDialog() })
				projects := a.projectDirectories()
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
			}
			a.recentsHeading(c, p)
		})

	})
}

// headerRow is the Codex switcher, approval bell and chats search.
func (a *app) headerRow(c *ui.Context, p palette, headerH, leftInset float32) {
	ui.Row(c).FillWidth().Height(headerH).DragWindow().Padding(0, 12, 0, leftInset).AlignItems(ui.Center).Children(func() {
		title := ui.ButtonBase(c).Label("Codex").Height(34).Radius(9).Padding(0, 9).
			Justify(ui.Start).Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
			if m.Item("New chat").Chosen() {
				a.startNewChat()
			}
			if m.Item("Search chats…").Chosen() {
				a.searchOpen = true
				a.focusSearch = true
			}
			m.Separator()
			if m.Item("Open folder…").Chosen() {
				a.openFolderDialog()
			}
		})
		if title.Hovered() {
			title.Background(p.hover)
		}
		title.Children(func() {
			ui.Text(c, "Codex").FontSize(15.5).FontWeight(600).SingleLine()
			ui.Icon(c, icChevDown).FontSize(11).MarginStart(5).TextColor(p.muted)
		})

		ui.Spacer(c)

		a.bellButton(c, p)
		a.navButton(c, p, "Search chats", icSearch, func() {
			a.searchOpen = true
			a.focusSearch = true
		}, p.text)
		c.Key("sidebar:hide")
		a.navButton(c, p, "Hide sidebar", icPanel, func() {
			a.sidebarCollapsed = true
		}, p.muted)
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
	keys := make([]string, 0, len(a.pending))
	for k := range a.pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ask := a.pending[keys[0]]
	for i := range a.sessions {
		if a.sessions[i].id == ask.session {
			a.adoptSession(a.sessions[i])
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
	case a.jobs <- agentJob{kind: "open", sid: ask.session}:
	default:
	}
}

// iconChip is a small icon button with its own hover face.
func (a *app) iconChip(c *ui.Context, p palette, label string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Size(32, 32).Radius(9).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	if on != nil {
		b.OnClick(on)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(p.text)
	})
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
				Tooltip("Add a project")
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
	for _, s := range a.sessions {
		if a.pinned[s.id] {
			out = append(out, s)
		}
	}
	return out
}

func (a *app) projectDirectories() []string {
	if len(a.projects) == 0 {
		return []string{a.home}
	}
	return append([]string(nil), a.projects...)
}

func discoverProjects(home string) []string {
	home = filepath.Clean(home)
	projects := []string{home}
	entries, err := os.ReadDir(filepath.Dir(home))
	if err != nil {
		return projects
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(filepath.Dir(home), entry.Name())
		if filepath.Clean(dir) != home && hasGitMetadata(dir) {
			projects = append(projects, dir)
		}
	}
	return projects
}

func hasGitMetadata(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func (a *app) addProject(dir string) {
	dir = filepath.Clean(dir)
	for _, project := range a.projects {
		if filepath.Clean(project) == dir {
			return
		}
	}
	a.projects = append(a.projects, dir)
}

func (a *app) projectRow(c *ui.Context, p palette, dir string) {
	name := dirBase(dir)
	c.Key("project:" + dir)
	b := a.row(c, p, "Open project "+name, false)
	b.Height(30).Tooltip(dir)
	b.OnClick(func() { a.startChatIn(dir) })
	b.Children(func() {
		ui.Icon(c, icFolder).FontSize(14).TextColor(p.muted)
		ui.Text(c, name).MarginStart(9).FontSize(13.5).Grow(1).SingleLine()
		ui.Icon(c, icChevRight).FontSize(12).TextColor(p.faint)
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

// recentsHeading titles the list of real conversations; while the runner
// is still reading them a spinner stands in. A search query takes the
// list over with what the store matched.
func (a *app) recentsHeading(c *ui.Context, p palette) {
	looking := a.searchText != ""
	ui.Row(c).FillWidth().Height(24).Margin(8, 0, 0, 0).Padding(0, 8).Children(func() {
		label := "Recents"
		if looking {
			label = "Results"
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		if !a.sessionsLoaded && !looking {
			ui.Spinner(c).Size(11, 11).Label("Loading recents")
		} else if a.searchLoading {
			ui.Spinner(c).Size(11, 11).Label("Searching chats")
		}
	})
	rows := a.sessions
	if looking {
		rows = a.searchResults
	} else {
		recent := make([]agentSession, 0, len(rows))
		for _, s := range rows {
			if !a.pinned[s.id] {
				recent = append(recent, s)
			}
		}
		rows = recent
	}
	more := !looking && a.sessionsLoaded && a.nextCursor != ""
	empty := looking && len(rows) == 0 && !a.searchLoading
	count := len(rows)
	if more || empty {
		count++
	}
	ui.List(c, &a.recents, count).Grow(1).FillWidth().Gap(2).
		ItemKey(func(i int) any {
			if i == len(rows) {
				return "footer"
			}
			return "session:" + rows[i].id
		}).Rows(func(row ui.ListRow) {
		if row.Index < len(rows) {
			a.sessionRow(row.Context, p, rows[row.Index])
		} else if more {
			a.olderRow(row.Context, p)
		} else {
			ui.Row(row.Context).Padding(4, 8).Children(func() {
				ui.Text(row.Context, "No chats match.").FontSize(13).TextColor(p.muted)
			})
		}
	})
}

// sessionRow is one real conversation: choosing it swaps the transcript
// while the runner brings the stored one back, and the kebab at its end
// renames or deletes it.
func (a *app) sessionRow(c *ui.Context, p palette, s agentSession) {
	label := s.title
	if label == "" {
		label = "New chat"
	}
	c.Key("session:" + s.id)
	b := a.row(c, p, label, s.id == a.selectedSession)
	b.PaddingStart(8)
	b.OnClick(func() {
		a.adoptSession(s)
		select {
		case a.jobs <- agentJob{kind: "open", sid: s.id}:
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
		c.Key("sessmenu:" + s.id)
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
	} else {
		a.pinned[s.id] = true
	}
}

// olderRow asks the runner for the next page of conversations.
func (a *app) olderRow(c *ui.Context, p palette) {
	b := a.row(c, p, "Show older chats", false)
	b.Height(28)
	b.OnClick(func() {
		if a.moreLoading || a.nextCursor == "" {
			return
		}
		a.moreLoading = true
		select {
		case a.jobs <- agentJob{kind: "more", cursor: a.nextCursor}:
		default:
			a.moreLoading = false
		}
	})
	b.Children(func() {
		ui.Text(c, "Show older chats").FontSize(13).TextColor(p.muted).MarginStart(24)
		if a.moreLoading {
			ui.Spinner(c).Size(11, 11).Label("Loading chats")
		}
	})
}

// sessionDialogs carries the two dialogs the Recents rows open.
func (a *app) sessionDialogs(c *ui.Context, p palette) {
	title := a.confirmWhat
	if title == "" {
		title = "New chat"
	}
	dialog(c, p, &a.confirmOpen, 340, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icTrash).FontSize(14).TextColor(p.orange)
			ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine()
		})
		ui.Text(c, "The conversation and what it did are gone for good.").
			FontSize(12.5).TextColor(p.muted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
			cancel.OnClick(func() { a.confirmOpen = false })
			cancel.Children(func() {
				ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
			})
			del := ui.ButtonBase(c).Label("Delete").Height(28).Radius(999).PaddingX(12).
				Background(p.orange)
			del.OnClick(a.deleteConfirmed)
			del.Children(func() {
				ui.Text(c, "Delete").FontSize(13).FontWeight(500).TextColor(ui.Hex("#ffffff"))
			})
		})
	})

	dialog(c, p, &a.renameOpen, 340, func() {
		ui.Text(c, "Rename this chat").FontSize(14).FontWeight(600)
		textField(c, p, &a.renameDraft, "")
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
			cancel.OnClick(func() { a.renameOpen = false })
			cancel.Children(func() {
				ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
			})
			save := ui.PrimaryButton(c, "Save")
			save.OnClick(a.saveRename)
		})
	})
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
