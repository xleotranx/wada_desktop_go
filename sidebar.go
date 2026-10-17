package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// sidebarPane draws the left column: the header row (the traffic lights
// sit above it when the title bar is hidden), the source list and the
// avatar pinned to its foot.
func (a *app) sidebarPane(c *ui.Context, p palette, bar ui.TitleBar, headerH float32) {
	ui.Column(c).Width(a.sidebarW).Background(p.sidebarBg).Children(func() {
		// The navigation row, draggable like a title bar.
		ui.Row(c).Height(headerH).DragWindow().Padding(0, 8, 0, bar.Left+10).Children(func() {
			a.navButton(c, p, "Back", icBack, func() {}, p.muted)
			a.navButton(c, p, "Forward", icForward, func() {}, p.muted)
			toggle := a.navButton(c, p, "Toggle sidebar", icPanel, func() {}, p.muted)
			toggle.MarginStart(10)
		})

		ui.Column(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			a.headerRow(c, p)
			a.newChatRow(c, p)
			ui.Box(c).Height(6)

			a.searchField(c, p)
			ui.Box(c).Height(6)

			a.recentsHeading(c, p)
		})

		// The user pinned to the foot of the list.
		ui.Row(c).Height(46).Padding(0, 16).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, "Shan Lin", nil).FontSize(12)
		})
	})
}

// headerRow is the app switcher: the product menu, the approvals bell
// with its badge and the way into the chats search.
func (a *app) headerRow(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(38).Children(func() {
		title := ui.ButtonBase(c).Label("ChatGPT").Height(32).Radius(9).Padding(0, 10).
			Justify(ui.Start).Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
			if m.Item("New chat").Chosen() {
				a.startNewChat()
			}
			if m.Item("Search chats…").Chosen() {
				a.focusSearch = true
			}
		})
		if title.Hovered() {
			title.Background(p.hover)
		}
		title.Children(func() {
			ui.Text(c, "ChatGPT").FontSize(15.5).FontWeight(600).SingleLine()
			ui.Icon(c, icChevDown).FontSize(11).MarginStart(5).TextColor(p.muted)
		})

		ui.Spacer(c)

		a.bellButton(c, p)
		chip := ui.ButtonBase(c).Label("Search").Size(32, 32).Radius(9).Center().
			Cursor(ui.CursorPointer).Tooltip("Search chats")
		if chip.Hovered() {
			chip.Background(p.hover)
		}
		chip.OnClick(func() { a.focusSearch = true })
		chip.Children(func() {
			ui.Icon(c, icSearch).FontSize(17).TextColor(p.text)
		})
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
		})
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
