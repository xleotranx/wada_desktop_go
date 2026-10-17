package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// prepareMarkdown tunes the transcript's shared markdown options to this
// frame's palette: the app's colors over the package's own meters of
// spacing, heading and code. The selection stays one across the message
// blocks, as the wrapping scroll gives it.
func (a *app) prepareMarkdown(p palette) {
	a.mdTheme = markdown.Theme{
		FontSize:            14,
		Text:                p.text,
		Muted:               p.muted,
		Link:                p.link,
		Strong:              p.text,
		Code:                p.text,
		CodeBackground:      p.detailBg,
		CodeBlockBackground: p.detailBg,
		CodeBlockBorder:     p.hairline,
		QuoteBar:            p.hairline,
		QuoteText:           p.muted,
		Rule:                p.hairline,
		TableBorder:         p.hairline,
	}
	a.md.Theme = &a.mdTheme
	a.md.ShareSelection = true
}

// contentPane draws the conversation title, transcript and composer.
func (a *app) contentPane(c *ui.Context, p palette, headerH float32) {
	a.prepareMarkdown(p)
	ui.Column(c).Grow(1).Background(p.bg).Children(func() {
		a.titleBar(c, p, headerH)
		ui.Divider(c)

		// The transcript with a fade at its foot, so the newest words slip
		// under the composer as they stream in.
		area := ui.Box(c).Grow(1)
		area.Children(func() {
			if a.selectedSession == "" {
				ui.Column(c).Fill().Center().Gap(8).Children(func() {
					ui.Text(c, "What should we work on?").FontSize(26).FontWeight(600).TextColor(p.text)
					ui.Text(c, "Work with your coding agent").FontSize(14).TextColor(p.muted)
				})
			} else {
				count := len(a.blocks)
				if a.turnWorking() {
					count++
				}
				ui.List(c.Key(a.selectedSession), &a.feed, count).Fill().Selectable().
					Padding(26, 24, 18, 24).Gap(13).ItemKey(a.blockIdentity).
					Rows(func(row ui.ListRow) {
						if row.Index == len(a.blocks) {
							a.blockRow(row.Context, p, block{kind: "working", text: "Working…"})
						} else {
							a.blockRow(row.Context, p, a.blocks[row.Index])
						}
					})
			}
			ui.Box(c).Absolute().Start(0).End(0).Bottom(0).Height(26).
				Gradient(p.bg.Alpha(0), p.bg, 180)
		})

		a.composer(c, p)
	})
}

// turnRunning reports the open chat's turn still going.
func (a *app) turnRunning() bool {
	return a.busySession != "" && a.busySession == a.selectedSession
}

// awaitingPerm reports an approval of the open chat's turn waiting on the
// user.
func (a *app) awaitingPerm() bool {
	for _, perm := range a.pending {
		if perm.session == a.selectedSession {
			return true
		}
	}
	return false
}

// turnWorking reports the open chat's turn still going with nothing being
// said right now: the loader stands at the transcript's foot before the
// first words, between them while a tool runs, and steps aside while an
// approval waits on the user.
func (a *app) turnWorking() bool {
	if !a.turnRunning() || a.awaitingPerm() {
		return false
	}
	// The newest stream row is the words landing: empty means there is
	// nothing to read yet, and no stream row at all means the turn sits
	// between steps.
	for i := len(a.blocks) - 1; i >= 0; i-- {
		if a.blocks[i].kind == "stream" {
			return strings.TrimSpace(a.blocks[i].text) == ""
		}
	}
	return true
}

// blockIdentity follows a row when approvals disappear or a stream
// settles. IDs are assigned lazily, so opening history visits only what
// the virtual list needs.
func (a *app) blockIdentity(i int) any {
	if i == len(a.blocks) {
		return "working"
	}
	if a.blocks[i].id == 0 {
		a.nextBlockID++
		a.blocks[i].id = a.nextBlockID
	}
	return a.blocks[i].id
}

// titleBar shows the chat's name between the window's controls.
func (a *app) titleBar(c *ui.Context, p palette, headerH float32) {
	title := a.selected
	if title == "" {
		title = "New chat"
	}
	icon := icFolder
	if title == "New chat" {
		icon = icPen
	}

	ui.Row(c).Height(headerH).DragWindow().Padding(0, 12).Children(func() {
		name := ui.ButtonBase(c).Label(title).Height(32).Radius(9).Padding(0, 10).
			Justify(ui.Start).Cursor(ui.CursorPointer).Tooltip("Switch chat").
			Menu(func(m *ui.Menu) {
				if m.Item("Rename…").Chosen() {
					if s, ok := a.selectedRow(); ok {
						a.requestRename(s)
					}
				}
				m.Separator()
				if m.Item("Delete").Chosen() {
					if s, ok := a.selectedRow(); ok {
						a.confirmDelete(s)
					}
				}
			})
		if name.Hovered() {
			name.Background(p.hover)
		}
		name.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(0).Children(func() {
				ui.Icon(c, icon).FontSize(15).TextColor(p.muted)
				ui.Text(c, title).MarginStart(8).FontSize(13).FontWeight(500).SingleLine()
			})
		})

		ui.Spacer(c)

		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			options := ui.ButtonBase(c).Label("Chat options").Size(30, 30).Radius(8).Center().
				Cursor(ui.CursorPointer).Tooltip("Chat options").
				Menu(func(m *ui.Menu) {
					if m.Item("Rename…").Chosen() {
						if s, ok := a.selectedRow(); ok {
							a.requestRename(s)
						}
					}
					m.Separator()
					if m.Item("Delete").Chosen() {
						if s, ok := a.selectedRow(); ok {
							a.confirmDelete(s)
						}
					}
				})
			if options.Hovered() {
				options.Background(p.hover)
			}
			options.Children(func() {
				ui.Icon(c, icEllipsis).FontSize(17).TextColor(p.muted)
			})
		})
	})
}

// blockRow draws one block of the transcript: a paragraph, what the user
// said, a line of what the agent ran, or the loader of the turn in
// flight.
func (a *app) blockRow(c *ui.Context, p palette, b block) {
	ui.Column(c).FillWidth().MaxWidth(760).Margin(0, ui.Auto).Gap(10).Children(func() {
		switch b.kind {
		case "user":
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				ui.Box(c).MaxWidthPercent(76).Radius(18).Padding(9, 14).
					Background(p.bubble).Children(func() {
					ui.Text(c, b.text).FontSize(14).LineHeight(1.45)
				})
			})
		case "activity":
			a.activityRow(c, p, b)
		case "permission":
			a.permissionRow(c, p, b)
		case "working":
			// The loader at the turn in flight, until words land.
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c).Size(13, 13).Label("The agent is working")
				ui.Text(c, b.text).FontSize(13).TextColor(p.muted)
			})
		case "note":
			// Engine trouble stays a faint line under the answers.
			ui.Text(c, b.text).FontSize(14).LineHeight(1.45).TextColor(p.faint).
				SingleLine().Ellipsis("…")
		case "stream":
			// Markdown still being written: the package parses the new
			// words alone, shows half-written syntax as what it will
			// become, and paces the words in.
			markdown.Stream(c, b.text, &a.md)
		default: // "text"
			markdown.View(c, b.text, &a.md)
		}
	})
}

// activityRow is one line the agent ran, opening its detail below it.
func (a *app) activityRow(c *ui.Context, p palette, b block) {
	open := a.expanded[b.key]
	chev := icChevRight
	if open {
		chev = icChevDown
	}
	row := ui.ButtonBase(c).Label(b.text).Height(28).Radius(8).Padding(0, 6).
		FillWidth().Justify(ui.Start).Cursor(ui.CursorPointer).Tooltip(b.text)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.OnClick(func() { a.expanded[b.key] = !a.expanded[b.key] })
	row.Children(func() {
		ui.Icon(c, b.icon).FontSize(15).TextColor(p.muted)
		ui.Text(c, b.text).MarginStart(9).FontSize(13).TextColor(p.muted).SingleLine()
		ui.Icon(c, chev).FontSize(12).MarginStart(6).TextColor(p.muted)
	})
	if open && b.detail != nil {
		ui.Row(c).FillWidth().Children(func() { b.detail(c, p) })
	}
}

// permissionRow is one approval the agent raised mid-turn: what it wants
// to do, with the three decisions the server takes. It stays until the
// runner's reply lands, then the card leaves the transcript.
func (a *app) permissionRow(c *ui.Context, p palette, b block) {
	r := b.req
	if r == nil {
		return
	}
	ui.Box(c).FillWidth().Radius(10).Padding(10, 12).Margin(4, 0, 0, 0).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icShield).FontSize(15).TextColor(p.orange)
				ui.Text(c, "Approval needed — "+permAsk(r.action)).
					FontSize(13).FontWeight(500).SingleLine()
			})
			for _, res := range r.resources {
				ui.Box(c).FillWidth().Radius(8).Padding(5, 10).
					Background(p.bg).Border(1, p.hairline).Children(func() {
					ui.Text(c, "$ "+res).Font("monospace").FontSize(12).LineHeight(1.5).
						TextColor(p.text).SingleLine()
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				a.permButton(c, p, true, ui.Hex("#3572e3"), ui.Hex("#ffffff"),
					"Allow once", "Let it happen this turn.", func() { a.answerPerm(r, "once") })
				a.permButton(c, p, false, p.hover, p.text,
					"Always", "Keep allowing this pattern.", func() { a.answerPerm(r, "always") })
				a.permButton(c, p, false, p.hover, p.text,
					"Deny", "Refuse and let the agent carry on.", func() { a.answerPerm(r, "reject") })
			})
			if len(r.save) > 0 {
				ui.Text(c, "Always would remember "+strings.Join(r.save, ", ")+".").
					FontSize(12).TextColor(p.muted).SingleLine()
			}
		})
	})
}

// permButton is one small pill of the approval card's decision row; a
// solid button keeps its tint, the rest pick it up while hovered.
func (a *app) permButton(c *ui.Context, p palette, solid bool, bg, tint ui.Color, label, tip string, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(26).Radius(999).PaddingX(12).
		Cursor(ui.CursorPointer).Tooltip(tip)
	if solid {
		b.Background(bg)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(tint).SingleLine()
	})
	b.OnClick(on)
}

// permAsk words what a permission action would do, as the card's header.
func permAsk(action string) string {
	switch action {
	case "bash", "shell":
		return "run a shell command"
	case "edit":
		return "edit files in the workspace"
	case "webfetch":
		return "fetch from the web"
	}
	return "use " + action
}

// commandDetail is the expanded detail of an activity: what ran, written
// as a terminal keeps it.
func commandDetail(lines ...string) func(c *ui.Context, p palette) {
	return func(c *ui.Context, p palette) {
		ui.Box(c).FillWidth().Radius(10).Padding(10, 12).Margin(4, 0, 0, 0).
			Background(p.detailBg).Border(1, p.hairline).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				for _, l := range lines {
					ui.Text(c, l).Font("monospace").FontSize(12).LineHeight(1.55).
						TextColor(p.text).SingleLine()
				}
			})
		})
	}
}
