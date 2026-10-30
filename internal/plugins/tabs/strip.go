package tabs

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"

	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

const (
	// Tabs are pills as tall as the strip's buttons, centered in the strip.
	tabHeight   = controlSize
	tabMaxWidth = 220
	tabMinWidth = 80
	tabGap      = 4
	closeSize   = 18
	controlSize = 28
)

// strip draws the space switcher, then the space's tabs and the pages, the
// overflow menu when some do not fit, the new-chat button, and search and
// settings at the end. The tabs are fitted to the width their row had in
// the previous frame.
func (p *Plugin) strip(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Row(c).Fill().AlignItems(ui.Center).Gap(8).Children(func() {
		c.Key("space")
		p.switcher(c, pal)
		c.Key("tabs")
		row := ui.Row(c).Grow(1).MinWidth(0).FillHeight().AlignItems(ui.Center).Gap(tabGap).Clip()
		width := row.Bounds().W
		if width <= 0 && len(p.tabs) > 0 {
			// Not laid out yet: draw everything once, then fit.
			c.Services().Invalidate()
		}
		p.shown = p.visibleInto(p.shown)
		shown, hidden := fit(p.shown, p.active, width-newButtonWidth)
		row.Children(func() {
			for _, t := range shown {
				c.Key(t.ID)
				p.tab(c, pal, t)
			}
			if len(hidden) > 0 {
				c.Key("more")
				p.overflow(c, pal, hidden)
			}
			c.Key("new")
			add := ui.ButtonBase(c).Label("New chat").Tooltip("New chat  ⌘N").Size(controlSize, controlSize).Radius(7).
				Center().Shrink(0).Margin(0, 0, 0, 4).Cursor(ui.CursorPointer)
			if add.Hovered() {
				add.Background(pal.Hover)
			}
			add.OnClick(p.newChat)
			add.Children(func() { ui.Icon(c, lucide.Plus()).FontSize(15).TextColor(pal.Muted) })
		})
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(2).Children(func() {
			for _, e := range kernel.Items(p.ctx, tabs.EndActions) {
				c.Key(e.ID)
				kernel.Invoke(p.ctx, e, func(a tabs.EndAction) {
					p.endButton(c, pal, a.Label, a.Tooltip, a.Icon(), func() {
						// Opening a dialog is not the click handler's job.
						p.ctx.Post(func() { kernel.Invoke(p.ctx, e, func(a tabs.EndAction) { a.Run() }) })
					})
				})
			}
			if p.hasPage(settings.PageID) {
				c.Key("settings")
				p.endButton(c, pal, "Settings", "Settings  ⌘,", lucide.Settings(), func() { p.host.ShowPage(settings.PageID) })
			}
		})
	})
}

// spaceName is what the strip calls a folder.
func spaceName(folder string) string {
	if folder == "" {
		return "No folder"
	}
	return filepath.Base(folder)
}

// switcher is the space on show, in its project color, opening the list of
// spaces.
func (p *Plugin) switcher(c *ui.Context, pal native.Palette) {
	name := spaceName(p.space)
	b := ui.ButtonBase(c).Label("Space "+name).Height(controlSize).MaxWidth(220).Padding(0, 8).Radius(8).Gap(7).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer)
	if !p.switching { // the tooltip would cover the list
		b.Tooltip(native.Tilde(cmp.Or(p.space, "the agent's default folder")))
	}
	switch {
	case p.switching:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.switching = !p.switching })
	b.Children(func() {
		p.spaceDot(c, pal, p.space)
		ui.Text(c, name).FontSize(14).FontWeight(500).TextColor(pal.Text).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		if p.elsewhere() != tabs.Idle {
			ui.Box(c).Size(6, 6).Radius(3).Background(pal.Orange).Shrink(0).Label("Activity in another space")
		}
		ui.Icon(c, lucide.ChevronsUpDown()).FontSize(12).TextColor(pal.Faint).Shrink(0)
	})
	components.Popover(c, b, &p.switching, func(panel ui.Element) {
		panel.Label("Spaces").Width(280).Gap(2).Children(func() { p.spaces(c, pal) })
	})
}

// spaces lists the folders to switch between, then what acts on them.
func (p *Plugin) spaces(c *ui.Context, pal native.Palette) {
	ui.Text(c, "Spaces").FontSize(11.5).FontWeight(600).TextColor(pal.Faint).Padding(2, 8, 4, 8)
	for _, folder := range p.spaceList() {
		c.Key("space:" + folder)
		n, state := 0, tabs.Idle
		for _, t := range p.tabs {
			if t.Kind != tabs.Page && t.Folder == folder {
				n++
				state = busier(state, p.state(t))
			}
		}
		current := folder == p.space
		row := ui.ButtonBase(c).Label("Switch to "+spaceName(folder)).FillWidth().Height(30).Padding(0, 8).Radius(6).Gap(8).
			AlignItems(ui.Center).Cursor(ui.CursorPointer)
		if current {
			row.Background(pal.Selected)
		} else if row.Hovered() {
			row.Background(pal.Hover)
		}
		row.OnClick(func() {
			p.switching = false
			p.showSpace(folder)
		})
		row.Children(func() {
			p.spaceDot(c, pal, folder)
			ui.Text(c, spaceName(folder)).FontSize(13).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			stateMark(c, pal, state)
			if n > 0 {
				ui.Text(c, strconv.Itoa(n)).FontSize(12).TextColor(pal.Faint).FontFeatures("tnum").Shrink(0)
			}
			check := ui.Icon(c, lucide.Check()).FontSize(13).TextColor(pal.Text).Shrink(0)
			if !current {
				check.Opacity(0)
			}
		})
	}
	ui.Box(c).FillWidth().Height(1).Background(pal.Hairline).Margin(4, 0)
	action := func(label string, run func()) {
		c.Key(label)
		b := ui.ButtonBase(c).Label(label).FillWidth().Height(30).Padding(0, 8).Radius(6).AlignItems(ui.Center).
			Justify(ui.Start).Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
		b.OnClick(func() {
			p.switching = false
			run()
		})
		b.Children(func() { ui.Text(c, label).FontSize(13).TextColor(pal.Text).SingleLine().Ellipsis("…") })
	}
	space := p.space
	action("New Chat in "+spaceName(space), func() { p.openDraft(space) })
	action("Open Folder…", func() { p.ws.ChooseFolder(p.openDraft) })
	if space != "" {
		action("Remove “"+spaceName(space)+"”…", func() { p.removing, p.removeOn = space, true })
	}
	if p.hasPage(settings.PageID) {
		action("Tab Settings…", func() { p.host.ShowPage(settings.PageID) })
	}
}

// spaceList is every space: the workspace's folders, then the folders open
// tabs work in that it does not list, the default folder among them.
func (p *Plugin) spaceList() []string {
	var out []string
	for _, f := range p.ws.Folders() {
		out = append(out, f.Path)
	}
	for _, t := range p.tabs {
		if t.Kind != tabs.Page && !slices.Contains(out, t.Folder) {
			out = append(out, t.Folder)
		}
	}
	if !slices.Contains(out, p.space) {
		out = append(out, p.space)
	}
	return out
}

// elsewhere is what the other spaces' conversations are doing, the most
// urgent first: waiting for the user, then unread.
func (p *Plugin) elsewhere() tabs.State {
	state := tabs.Idle
	for _, t := range p.tabs {
		if t.Kind == tabs.Conversation && t.Folder != p.space {
			if s := p.state(t); s == tabs.Waiting || s == tabs.Unread {
				state = busier(state, s)
			}
		}
	}
	return state
}

// busier is the state of the two that asks more of the user.
func busier(a, b tabs.State) tabs.State {
	rank := func(s tabs.State) int {
		switch s {
		case tabs.Waiting:
			return 3
		case tabs.Unread:
			return 2
		case tabs.Working:
			return 1
		case tabs.Idle:
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func (p *Plugin) spaceDot(c *ui.Context, pal native.Palette, folder string) {
	color := pal.Faint
	if folder != "" {
		color = pal.Projects[p.colorIndex(folder)].Mark
	}
	ui.Box(c).Size(8, 8).Radius(4).Background(color).Shrink(0)
}

// tab is as wide as its title, up to tabMaxWidth. The close button sits over
// its end and shows on the active tab, on hover and on focus; only the active
// tab keeps room for it.
func (p *Plugin) tab(c *ui.Context, pal native.Palette, t tabs.Tab) {
	active := t.ID == p.active
	look, looked := p.look(t)
	if looked && t.Kind == tabs.Draft && look.Draft != "" {
		t.Title = look.Draft
	}
	b := ui.ButtonBase(c).Label(t.Title).MaxWidth(tabMaxWidth).MinWidth(tabMinWidth).Shrink(1).Height(tabHeight).
		Radius(8).Cursor(ui.CursorPointer)
	fill := pal.StripBg
	switch {
	case active:
		fill = pal.ActiveTab
		b.Shadow(0, 1, 2, 0, pal.Text.Alpha(.08))
	case b.Hovered():
		fill = pal.Hover
	}
	if fill != pal.StripBg {
		b.Background(fill)
	}
	b.OnClick(func() { p.Select(t.ID) })
	b.ContextMenu(func(m *ui.Menu) { p.tabMenu(m, t) })
	b.Children(func() {
		end := float32(10)
		if active {
			end = 4 + closeSize + 4
		}
		ui.Row(c).Fill().MinWidth(0).AlignItems(ui.Center).Gap(5).Padding(0, end, 0, 10).Children(func() {
			p.marker(c, pal, t, look, looked)
			text := pal.Muted
			if active {
				text = pal.Text
			}
			ui.Text(c, t.Title).FontSize(14).TextColor(text).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		})
		c.Key("close")
		x := ui.ButtonBase(c).Label("Close "+t.Title).Size(closeSize, closeSize).Radius(5).Center().
			Absolute().Right(4).Top((tabHeight - closeSize) / 2).Background(fill).Cursor(ui.CursorPointer)
		if x.Hovered() {
			x.Background(pal.Selected)
		}
		// Keep the button mounted so Tab reaches it on any tab.
		if !active && !b.Hovered() && !x.Focused() {
			x.Opacity(0)
		}
		x.OnClick(func() { p.Close(t.ID) })
		x.Children(func() { ui.Icon(c, lucide.X()).FontSize(11).TextColor(pal.Muted) })
	})
}

// marker shows what a tab's conversation is doing; a new chat shows a plus.
// A tab with a look shows its icon instead of the plus, and while idle.
func (p *Plugin) marker(c *ui.Context, pal native.Palette, t tabs.Tab, look tabs.Look, looked bool) {
	state := tabs.Idle
	if t.Kind != tabs.Draft {
		state = p.state(t)
	}
	switch {
	case state != tabs.Idle:
		stateMark(c, pal, state)
	case looked && look.Icon != nil:
		ui.Icon(c, look.Icon()).FontSize(12).TextColor(pal.Muted).Shrink(0).Label(look.Name)
	case t.Kind == tabs.Draft:
		ui.Icon(c, lucide.Plus()).FontSize(12).TextColor(pal.Muted).Shrink(0)
	}
}

// look is the first Look that applies to the tab.
func (p *Plugin) look(t tabs.Tab) (tabs.Look, bool) {
	for _, e := range kernel.Items(p.ctx, tabs.Looks) {
		applies := false
		kernel.Invoke(p.ctx, e, func(l tabs.Look) { applies = l.Applies != nil && l.Applies(t) })
		if applies {
			return e.Value, true
		}
	}
	return tabs.Look{}, false
}

// stateMark is a conversation's state as a small mark; idle shows none.
func stateMark(c *ui.Context, pal native.Palette, state tabs.State) {
	switch state {
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
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Size(controlSize, controlSize).Radius(7).Center().
		Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(on)
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(pal.Muted) })
}

// newButtonWidth is the new-chat button and the gap before it.
const newButtonWidth = controlSize + 6 + tabGap

// newChat opens a new chat in the space on show (⌘N, and "+").
func (p *Plugin) newChat() { p.openDraft(p.space) }

// openDraft opens a new chat in the folder; following the chat gives it a tab.
func (p *Plugin) openDraft(folder string) {
	p.chat.NewChat(folder)
	p.host.ShowPage(chat.PageID)
}

func (p *Plugin) colorIndex(folder string) int {
	return max(p.colors.Get()[folder], 0) % len(native.Palette{}.Projects)
}

// overflow is the button whose menu reaches the tabs that do not fit.
func (p *Plugin) overflow(c *ui.Context, pal native.Palette, hidden []tabs.Tab) {
	b := ui.ButtonBase(c).Label("More tabs").Tooltip(strconv.Itoa(len(hidden))+" more tabs").
		Height(controlSize).Padding(0, 8).Radius(7).Gap(4).AlignItems(ui.Center).Shrink(0).
		Margin(0, 0, 0, 4).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Menu(func(m *ui.Menu) { p.tabItems(m, hidden) })
	b.Children(func() {
		ui.Icon(c, lucide.ChevronsRight()).FontSize(14).TextColor(pal.Muted)
		ui.Text(c, strconv.Itoa(len(hidden))).FontSize(12).TextColor(pal.Muted).FontFeatures("tnum")
	})
}
