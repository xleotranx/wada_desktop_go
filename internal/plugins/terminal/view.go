package terminal

import (
	"cmp"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
	contractterm "wada-desktop-go/internal/contract/terminal"
)

const (
	drawerWidth  = 460
	lineFontSize = 11.5
	// lineHeight is one row of output: the text's line height plus the gap.
	lineHeight   = lineFontSize*1.4 + 1
	viewportPadX = 12
	viewportPadY = 10
)

func (p *Plugin) drawerView(c *ui.Context) {
	if !p.open {
		return
	}
	pal := native.PaletteFor(c)
	active, hasActive := p.activeTerminal()

	ui.Row(c.Key("terminal-drawer")).Width(drawerWidth).FillHeight().Shrink(0).Label("Terminal").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(pal.Bg).Children(func() {
			p.drawerHeader(c, pal, active, hasActive)
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			if len(p.terminals) > 0 {
				p.drawerTabs(c, pal)
				ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			}
			p.drawerViewport(c, pal, hasActive)
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			p.drawerFooter(c, pal, active, hasActive)
		})
	})
}

func (p *Plugin) drawerHeader(c *ui.Context, pal native.Palette, active contractterm.Info, hasActive bool) {
	sub := "Interactive PTY shell"
	if hasActive {
		dir := cmp.Or(active.Directory, p.conv.Directory)
		sh := filepath.Base(active.Shell)
		switch {
		case sh != "" && sh != "." && dir != "":
			sub = sh + " · " + dir
		case dir != "":
			sub = dir
		case sh != "" && sh != ".":
			sub = sh
		}
	} else if p.conv.Directory != "" {
		sub = p.conv.Directory
	}

	ui.Row(c).FillWidth().Height(48).Shrink(0).Padding(0, 10, 0, 14).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, lucide.Terminal()).FontSize(14).TextColor(pal.Muted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, "Terminal").FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine()
			ui.Text(c, sub).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		nb := ui.ButtonBase(c.Key("new-term")).Label("New terminal").Tooltip("New terminal  ⇧⌘J").Size(26, 26).Radius(6).
			Center().Cursor(ui.CursorPointer).OnClick(p.createTerminal)
		if nb.Hovered() {
			nb.Background(pal.Hover)
		}
		nb.Children(func() { ui.Icon(c, lucide.Plus()).FontSize(13).TextColor(pal.Muted) })

		if hasActive {
			cp := ui.ButtonBase(c.Key("copy-term")).Label("Copy output").Tooltip("Copy output").Size(26, 26).Radius(6).
				Center().Cursor(ui.CursorPointer).OnClick(p.copyOutput)
			if cp.Hovered() {
				cp.Background(pal.Hover)
			}
			cp.Children(func() { ui.Icon(c, lucide.Copy()).FontSize(12.5).TextColor(pal.Muted) })
		}

		cb := ui.ButtonBase(c.Key("close-term")).Label("Close terminal").Tooltip("Close terminal  ⌘J").Size(26, 26).Radius(6).
			Center().Cursor(ui.CursorPointer).OnClick(p.closeDrawer)
		if cb.Hovered() {
			cb.Background(pal.Hover)
		}
		cb.Children(func() { ui.Icon(c, lucide.X()).FontSize(13).TextColor(pal.Muted) })
	})
}

func (p *Plugin) drawerTabs(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Padding(6, 10).Gap(6).AlignItems(ui.Center).Children(func() {
		for _, t := range p.terminals {
			term := t
			active := term.ID == p.activeID
			dotColor := pal.Faint
			if term.Running {
				dotColor = pal.GreenBadge
			}
			row := ui.Row(c.Key("tab:"+term.ID)).Height(26).Radius(6).Padding(0, 6, 0, 8).Gap(5).AlignItems(ui.Center)
			if active {
				row.Background(pal.Selected)
			}
			row.Children(func() {
				sel := ui.ButtonBase(c.Key("select")).Label(cmp.Or(term.Name, term.ID)).Height(24).Gap(5).
					AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() { p.selectTerminal(term.ID) })
				if !active && sel.Hovered() {
					sel.Background(pal.Hover)
				}
				sel.Children(func() {
					ui.Box(c).Size(6, 6).Radius(99).Background(dotColor).Shrink(0)
					col := pal.Muted
					if active {
						col = pal.Text
					}
					ui.Text(c, cmp.Or(term.Name, term.ID)).FontSize(12).FontWeight(500).TextColor(col).SingleLine()
				})

				kb := ui.ButtonBase(c.Key("kill")).Label("Kill "+cmp.Or(term.Name, term.ID)).Tooltip("Kill terminal").
					Size(18, 18).Radius(4).Center().Cursor(ui.CursorPointer).OnClick(func() { p.killTerminal(term.ID) })
				if kb.Hovered() {
					kb.Background(pal.Hover)
				}
				kb.Children(func() { ui.Icon(c, lucide.X()).FontSize(11).TextColor(pal.Muted) })
			})
		}
	})
}

func (p *Plugin) drawerViewport(c *ui.Context, pal native.Palette, hasActive bool) {
	ui.Column(c.Key("viewport")).Bind(&p.viewport).FillWidth().Grow(1).MinHeight(0).Background(pal.DetailBg).Children(func() {
		if p.errText != "" {
			ui.Box(c).FillWidth().Padding(6, 12).Background(pal.Danger.Alpha(.12)).Children(func() {
				ui.Text(c, p.errText).FontSize(11.5).TextColor(pal.DangerText)
			})
		}
		if !hasActive {
			ui.Column(c).Fill().Center().Gap(10).Padding(24).Children(func() {
				ui.Icon(c, lucide.Terminal()).FontSize(22).TextColor(pal.Muted)
				ui.Text(c, "No open terminal").FontSize(13.5).FontWeight(500).TextColor(pal.Text)
				ui.Text(c, "Start a detached PTY shell in the project folder. It stays running even if you close this drawer.").
					FontSize(12).LineHeight(1.45).TextColor(pal.Muted).TextAlign(ui.Center).MaxWidth(320)
				components.Button(c, "New terminal", components.ButtonPrimary).OnClick(p.createTerminal)
			})
			return
		}

		p.fitTo(c)
		listKey := "term-lines:" + p.activeID
		ui.List(c.Key(listKey), &p.listState, p.screen.LineCount()).Fill().Selectable().Padding(viewportPadY, viewportPadX).Gap(1).
			ItemKey(func(i int) any { return i }).
			Rows(func(r ui.ListRow) {
				line := p.screen.Line(r.Index)
				if line == "" {
					line = " "
				}
				ui.Text(r.Context, line).Font("monospace").FontSize(lineFontSize).LineHeight(1.4).TextColor(pal.Text).SingleLine()
			})
	})
}

func (p *Plugin) drawerFooter(c *ui.Context, pal native.Palette, active contractterm.Info, hasActive bool) {
	statusText := "idle"
	statusColor := pal.Faint
	if hasActive {
		if active.Running {
			if active.PID > 0 {
				statusText = "running · pid " + strconv.Itoa(active.PID)
			} else {
				statusText = "running"
			}
			statusColor = pal.GreenBadge
		} else {
			statusText = "exited (" + strconv.Itoa(active.ExitCode) + ")"
			statusColor = pal.Muted
		}
	}

	ui.Column(c).FillWidth().Shrink(0).Padding(8, 12, 12, 12).Gap(8).Background(pal.Bg).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			p.controlChip(c, pal, "ctrl-c", "Ctrl+C", func() { p.sendControl(0x03) }, hasActive && active.Running)
			p.controlChip(c, pal, "ctrl-d", "Ctrl+D", func() { p.sendControl(0x04) }, hasActive && active.Running)
			p.controlChip(c, pal, "clear", "Clear", p.clearScreen, hasActive)
			ui.Spacer(c)
			ui.Text(c, statusText).FontSize(11).TextColor(statusColor).SingleLine()
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Box(c.Key("term-input")).Grow(1).MinWidth(0).Children(func() {
				box := components.TextBox(c, &p.input, components.TextBoxOptions{
					ReadOnly: !hasActive || !active.Running,
				}).FillWidth().Placeholder("Type a command and press Enter…").OnSubmit(p.submitInput)
				if p.focusInput {
					box.Focus()
					p.focusInput = false
				}
			})
			sb := ui.ButtonBase(c.Key("send-cmd")).Label("Run command").Tooltip("Send Enter").Size(30, 30).Radius(7).
				Center().Cursor(ui.CursorPointer)
			if hasActive && active.Running {
				sb.Background(pal.Selected).OnClick(p.submitInput)
			}
			sb.Children(func() {
				col := pal.Faint
				if hasActive && active.Running {
					col = pal.Text
				}
				ui.Icon(c, lucide.CornerDownLeft()).FontSize(13).TextColor(col)
			})
		})
	})
}

func (p *Plugin) controlChip(c *ui.Context, pal native.Palette, key, label string, onClick func(), enabled bool) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Height(24).Padding(0, 8).Radius(6).
		Border(1, pal.Hairline).Center().Cursor(ui.CursorPointer)
	if enabled {
		b.OnClick(onClick)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
	}
	b.Children(func() {
		col := pal.Faint
		if enabled {
			col = pal.Muted
		}
		ui.Text(c, label).FontSize(11).FontWeight(500).TextColor(col).SingleLine()
	})
}
