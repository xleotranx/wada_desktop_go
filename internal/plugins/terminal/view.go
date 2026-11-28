package terminal

import (
	"cmp"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/tabs"
	contractterm "wada-desktop-go/internal/contract/terminal"
)

const (
	lineFontSize = 11.5
	// lineHeight is one row of output: the text's line height plus the gap.
	lineHeight   = lineFontSize*1.4 + 1
	viewportPadX = 12
	viewportPadY = 10
)

// paneView is a terminal tab: its header, output and input.
func (p *Plugin) paneView(c *ui.Context, t tabs.Tab) {
	pal := native.PaletteFor(c)
	active, hasActive := p.activeTerminal()
	hasActive = hasActive && t.Ref == p.activeID
	ui.Column(c.Key("terminal-pane")).Fill().Background(pal.Bg).Label("Terminal").Children(func() {
		p.paneHeader(c, pal, t, active, hasActive)
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
		p.paneViewport(c, pal, hasActive)
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
		p.paneFooter(c, pal, active, hasActive)
	})
}

func (p *Plugin) paneHeader(c *ui.Context, pal native.Palette, t tabs.Tab, active contractterm.Info, hasActive bool) {
	sub := "Interactive PTY shell"
	if hasActive {
		dir := native.Tilde(active.Directory)
		sh := filepath.Base(active.Shell)
		switch {
		case sh != "" && sh != "." && dir != "":
			sub = sh + " · " + dir
		case dir != "":
			sub = dir
		case sh != "" && sh != ".":
			sub = sh
		}
	}

	ui.Row(c).FillWidth().Height(44).Shrink(0).Padding(0, 10, 0, 14).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, cmp.Or(p.title(t.Ref), t.Title, "Terminal")).FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		p.iconButton(c, pal, "new-term", "New terminal", "New terminal  ⇧⌘J", lucide.Plus(), func() { p.createTerminal(t.Folder) })
		if hasActive {
			p.iconButton(c, pal, "copy-term", "Copy output", "Copy output", lucide.Copy(), p.copyOutput)
			p.iconButton(c, pal, "kill-term", "Kill terminal", "Kill terminal and close its tab", lucide.Trash(), func() { p.killTerminal(t.Ref) })
		}
	})
}

func (p *Plugin) iconButton(c *ui.Context, pal native.Palette, key, label, tip string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(tip).Size(26, 26).Radius(6).
		Center().Cursor(ui.CursorPointer).OnClick(on)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(13).TextColor(pal.Muted) })
}

func (p *Plugin) paneViewport(c *ui.Context, pal native.Palette, hasActive bool) {
	ui.Column(c.Key("viewport")).Bind(&p.viewport).FillWidth().Grow(1).MinHeight(0).Background(pal.DetailBg).Children(func() {
		if p.errText != "" {
			ui.Box(c).FillWidth().Padding(6, 12).Background(pal.Danger.Alpha(.12)).Children(func() {
				ui.Text(c, p.errText).FontSize(11.5).TextColor(pal.DangerText)
			})
		}
		if !hasActive {
			ui.Column(c).Fill().Center().Gap(10).Padding(24).Children(func() {
				ui.Icon(c, lucide.Terminal()).FontSize(22).TextColor(pal.Muted)
				ui.Text(c, "Connecting to the terminal…").FontSize(13).TextColor(pal.Muted)
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

func (p *Plugin) paneFooter(c *ui.Context, pal native.Palette, active contractterm.Info, hasActive bool) {
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
