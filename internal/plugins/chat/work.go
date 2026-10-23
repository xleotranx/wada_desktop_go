package chat

import (
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

const workWidth = 300

// WorkShown reports that the work panel is on show: beside a stored
// conversation, while it is turned on and a section has something to show.
func (p *Plugin) WorkShown() bool {
	return p.conv.Session != "" && p.workOn.Get() && len(p.workSections()) > 0
}

// workSections are the Work entries that have something to show now.
func (p *Plugin) workSections() []kernel.Entry[chat.WorkSection] {
	var out []kernel.Entry[chat.WorkSection]
	for _, e := range kernel.Items(p.ctx, chat.Work) {
		shown := e.Value.Render != nil
		if shown && e.Value.Shown != nil {
			kernel.Invoke(p.ctx, e, func(s chat.WorkSection) { shown = s.Shown() })
		}
		if shown {
			out = append(out, e)
		}
	}
	return out
}

// work draws the panel: each section's title over what it renders.
func (p *Plugin) work(c *ui.Context, pal native.Palette, sections []kernel.Entry[chat.WorkSection]) {
	ui.Column(c).Width(workWidth).FillHeight().Shrink(0).Background(pal.DrawerBg).Label("Work panel").Children(func() {
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).FillWidth().Padding(18, 16).Gap(22).Children(func() {
				for _, e := range sections {
					c.Key(e.ID)
					ui.Column(c).FillWidth().Gap(10).Children(func() {
						ui.Text(c, e.Value.Title).FontSize(11.5).FontWeight(600).TextColor(pal.Faint).SingleLine()
						kernel.Invoke(p.ctx, e, func(s chat.WorkSection) { s.Render(c) })
					})
				}
			})
		})
	})
}

// settingsSection is the Conversation settings: whether the work panel shows.
func (p *Plugin) settingsSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, "Show the work panel").FontSize(13.5).FontWeight(500).TextColor(pal.Text)
			ui.Text(c, "Beside a conversation: its context, changed files and cost. ⌘B turns it on and off.").
				FontSize(12).TextColor(pal.Muted)
		})
		on := p.workOn.Get()
		value := on
		components.Switch(c, &value, "Show the work panel", components.SwitchOptions{HideLabel: true})
		if value != on {
			p.workOn.Set(value)
		}
	})
}
