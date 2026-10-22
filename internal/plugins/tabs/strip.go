package tabs

import (
	"path/filepath"

	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

const (
	tabHeight   = 40 // to the strip's foot, so the active tab meets the panel
	tabWidth    = 220
	tabMinWidth = 96
	groupLine   = 2
)

// strip draws the tabs across the title strip: each folder's tabs after its
// label, over a line in its color; then the new-chat button, and search and
// settings at the end.
func (p *Plugin) strip(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Row(c).Fill().AlignItems(ui.End).Gap(8).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).FillHeight().AlignItems(ui.End).Gap(10).Clip().Children(func() {
			for start := 0; start < len(p.tabs); {
				end := start + 1
				for end < len(p.tabs) && p.tabs[end].Folder == p.tabs[start].Folder {
					end++
				}
				p.group(c, pal, p.tabs[start:end])
				start = end
			}
			c.Key("new")
			add := ui.ButtonBase(c).Label("New chat").Tooltip("New chat  ⌘N").Size(30, 30).Radius(8).Center().
				Shrink(0).Margin(0, 0, 5, 0).Cursor(ui.CursorPointer)
			if add.Hovered() {
				add.Background(pal.Hover)
			}
			add.OnClick(p.newChat)
			add.Children(func() { ui.Icon(c, lucide.Plus()).FontSize(15).TextColor(pal.Muted) })
		})
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(2).Margin(0, 0, 10, 0).Children(func() {
			if search, ok := p.command(shell.SearchCommand); ok {
				c.Key("search")
				p.endButton(c, pal, "All chats", "All chats  ⌘K", lucide.Search(), func() {
					// Opening a dialog is not the click handler's job.
					p.ctx.Post(func() { kernel.Invoke(p.ctx, search, func(cmd kernel.Command) { cmd.Run() }) })
				})
			}
			if p.hasPage(settings.PageID) {
				c.Key("settings")
				p.endButton(c, pal, "Settings", "Settings  ⌘,", lucide.Settings(), func() { p.host.ShowPage(settings.PageID) })
			}
		})
	})
}

// group draws one folder's run of tabs; tabs without a folder have no label
// and no line.
func (p *Plugin) group(c *ui.Context, pal native.Palette, run []tabs.Tab) {
	folder := run[0].Folder
	c.Key("group:" + folder + ":" + run[0].ID)
	color := pal.Projects[p.colorIndex(folder)]
	ui.Row(c).MinWidth(0).Shrink(1).AlignItems(ui.End).Children(func() {
		if folder != "" {
			name := filepath.Base(folder)
			ui.Column(c).Height(tabHeight).Shrink(0).Children(func() {
				ui.Row(c).Grow(1).AlignItems(ui.Center).Padding(0, 4, 0, 0).Children(func() {
					ui.Text(c, name).FontSize(11).FontWeight(500).TextColor(color.Text).Background(color.Tint).
						Padding(3, 8).Radius(6).SingleLine().Label("Project " + name)
				})
				ui.Box(c).FillWidth().Height(groupLine).Background(color.Mark)
			})
		}
		for _, t := range run {
			c.Key(t.ID)
			p.tab(c, pal, t, color.Mark, folder != "")
		}
	})
}

func (p *Plugin) tab(c *ui.Context, pal native.Palette, t tabs.Tab, mark ui.Color, grouped bool) {
	active := t.ID == p.active
	b := ui.ButtonBase(c).Label(t.Title).Width(tabWidth).Shrink(1).MinWidth(tabMinWidth).Height(tabHeight).
		Radius(10, 10, 0, 0).Cursor(ui.CursorPointer)
	if active {
		b.Background(pal.PanelBg)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.Select(t.ID) })
	b.Children(func() {
		ui.Column(c).Fill().MinWidth(0).Children(func() {
			ui.Row(c).Grow(1).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(8).Padding(0, 6, 0, 12).Children(func() {
				p.marker(c, pal, t)
				text := pal.Muted
				if active {
					text = pal.Text
				}
				ui.Text(c, t.Title).FontSize(13).TextColor(text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				c.Key("close")
				x := ui.ButtonBase(c).Label("Close "+t.Title).Size(22, 22).Radius(6).Center().Shrink(0).
					Cursor(ui.CursorPointer)
				if x.Hovered() {
					x.Background(pal.Selected)
				}
				// Keep the button mounted so Tab reaches it on any tab.
				if !active && !b.Hovered() && !x.Focused() {
					x.Opacity(0)
				}
				x.OnClick(func() { p.Close(t.ID) })
				x.Children(func() { ui.Icon(c, lucide.X()).FontSize(12).TextColor(pal.Muted) })
			})
			line := ui.Box(c).FillWidth().Height(groupLine)
			if grouped && !active {
				line.Background(mark)
			}
		})
	})
}

// marker shows what a tab's conversation is doing; a new chat shows a plus.
func (p *Plugin) marker(c *ui.Context, pal native.Palette, t tabs.Tab) {
	if t.Kind == tabs.Draft {
		ui.Icon(c, lucide.Plus()).FontSize(12).TextColor(pal.Muted).Shrink(0)
		return
	}
	switch p.state(t) {
	case tabs.Working:
		ui.Icon(c, lucide.LoaderCircle()).FontSize(12).TextColor(pal.Muted).Shrink(0).Label("Working")
	case tabs.Waiting:
		ui.Box(c).Size(8, 8).Radius(2).Background(pal.Waiting).Shrink(0).Label("Waiting for you")
	case tabs.Unread:
		ui.Box(c).Size(7, 7).Radius(4).Background(pal.Text).Shrink(0).Label("Unread")
	case tabs.Idle:
	}
}

func (p *Plugin) endButton(c *ui.Context, pal native.Palette, label, tip string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Size(30, 30).Radius(8).Center().Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(on)
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(pal.Muted) })
}

// newChat opens a new chat in the active tab's folder, as ⌘N does.
func (p *Plugin) newChat() {
	folder := ""
	if t, ok := p.Active(); ok {
		folder = t.Folder
	}
	p.chat.NewChat(folder)
	p.host.ShowPage(chat.PageID)
}

func (p *Plugin) colorIndex(folder string) int {
	return max(p.colors.Get()[folder], 0) % len(native.Palette{}.Projects)
}

// command is the contributed command with this ID, when it can run.
func (p *Plugin) command(id string) (kernel.Entry[kernel.Command], bool) {
	for _, e := range kernel.Items(p.ctx, kernel.Commands) {
		if e.ID == id && e.Value.Run != nil {
			return e, true
		}
	}
	return kernel.Entry[kernel.Command]{}, false
}
