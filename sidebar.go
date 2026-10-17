package main

import "github.com/egoist/mygo/ui"

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

		ui.Scroll(c).Grow(1).Padding(4, 10, 6, 10).Gap(2).Children(func() {
			a.headerRow(c, p)
			a.newChatRow(c, p)
			ui.Box(c).Height(6)

			a.heading(c, p, "Pinned", "")
			if a.sectionOpen("pinned") {
				a.chatRow(c, p, a.pinned, 8, false, func() {
					a.textRow(c, p, a.pinned)
				})
			}

			a.heading(c, p, "Projects", "")
			if a.sectionOpen("projects") {
				for i := range a.folders {
					a.folderRow(c, p, i)
				}
				a.chatRow(c, p, "Homework", 8, false, func() {
					ui.Image(c, icBooks).Size(16, 16)
					a.textRow(c, p, "Homework")
				})
			}

			a.recentsHeading(c, p)
		})

		// The user pinned to the foot of the list.
		ui.Row(c).Height(46).Padding(0, 16).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, "Shan Lin", nil).FontSize(12)
		})
	})
}

// headerRow is the app switcher: ChatGPT and its chevron with the bell
// and the search at its right, each its own hover target.
func (a *app) headerRow(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(38).Children(func() {
		title := ui.ButtonBase(c).Label("ChatGPT").Height(32).Radius(9).Padding(0, 10).
			Justify(ui.Start).Cursor(ui.CursorPointer).Menu(func(m *ui.Menu) {
			m.Item("New chat")
			m.Item("Search chats")
			m.Separator()
			m.Item("Settings")
			m.Item("Help")
		})
		if title.Hovered() {
			title.Background(p.hover)
		}
		title.Children(func() {
			ui.Text(c, "ChatGPT").FontSize(15.5).FontWeight(600).SingleLine()
			ui.Icon(c, icChevDown).FontSize(11).MarginStart(5).TextColor(p.muted)
		})

		ui.Spacer(c)

		a.iconChip(c, p, "Notifications", icBell, nil)
		a.iconChip(c, p, "Search", icSearch, nil)
	})
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
	b.OnClick(func() {
		a.selected = ""
		a.draft = ""
		a.blocks = memoTranscript()
	})
	b.Children(func() {
		ui.Icon(c, icPen).FontSize(16).TextColor(p.text)
		ui.Text(c, "New chat").MarginStart(10).FontSize(14).FontWeight(500).SingleLine()
	})
}

// heading draws the small title of a section and closes or opens it.
func (a *app) heading(c *ui.Context, p palette, label string, key string) {
	b := ui.ButtonBase(c).Label(label).Height(24).Radius(7).Padding(0, 8).
		Cursor(ui.CursorPointer).FillWidth().Margin(8, 0, 0, 0).Justify(ui.Start)
	if key != "" {
		b.OnClick(func() { a.expanded["title:"+key] = !a.expanded["title:"+key] })
	}
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(p.muted).SingleLine()
	})
}

func (a *app) sectionOpen(key string) bool {
	return !a.expanded["title:"+key]
}

// recentsHeading shows the title that opens the recents page, with the
// spinner standing for the chats still loading.
func (a *app) recentsHeading(c *ui.Context, p palette) {
	ui.Row(c).FillWidth().Height(24).Margin(8, 0, 0, 0).Padding(0, 8).Children(func() {
		ui.Text(c, "Recents").FontSize(12).FontWeight(500).TextColor(p.muted).SingleLine()
		ui.Icon(c, icChevRight).FontSize(11).MarginStart(4).TextColor(p.muted)
		ui.Spacer(c)
		ui.Spinner(c).Size(11, 11).Label("Loading recents")
	})
}

// folderRow draws a project and, once open, its chats under it.
func (a *app) folderRow(c *ui.Context, p palette, i int) {
	f := a.folders[i]
	key := "folder:" + f.name
	b := ui.ButtonBase(c).Label(f.name).Height(32).Radius(9).Padding(0, 8).
		Cursor(ui.CursorPointer).FillWidth()
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.expanded[key] = !a.expanded[key] })
	b.Children(func() {
		ui.Icon(c, icFolder).FontSize(16).TextColor(p.text)
		ui.Text(c, f.name).MarginStart(10).FontSize(14).FontWeight(500).Grow(1).SingleLine()
	})
	if a.expanded[key] {
		for _, task := range f.tasks {
			busy := f.name == "wada_pi_webapp" && task == "Implement PD-09 outcomes and retries"
			a.chatRow(c, p, task, 34, busy, func() {
				a.textRow(c, p, task)
			})
		}
	}
}

// chatRow is one row of the list: full width, rounded, tinted while
// hovered, held tinted while chosen, with the running spinner at its end
// when busy. content draws what the row shows beside its spinner.
func (a *app) chatRow(c *ui.Context, p palette, label string, start float32, busy bool, content func()) {
	b := a.row(c, p, label, label == a.selected)
	b.PaddingStart(start)
	b.OnClick(func() { a.selected = label })
	b.Children(func() {
		content() // the label grows and pushes the spinner to the end
		if busy {
			ui.Spinner(c).Size(13, 13).MarginStart(8).Label("Running")
		}
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
