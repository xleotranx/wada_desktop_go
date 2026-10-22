package tabs

import (
	"path/filepath"
	"strconv"

	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// section is the Tabs settings: reopening, and each folder's color.
func (p *Plugin) section(c *ui.Context) {
	pal := native.PaletteFor(c)
	toggle(c, pal, p.reopen.Get(), "Reopen tabs when Wada starts",
		"The tabs open when Wada quit come back, without conversations no longer listed.",
		func(on bool) { p.reopen.Set(on) })
	folders := p.ws.Folders()
	if len(folders) == 0 {
		return
	}
	ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, "Project colors").FontSize(14).FontWeight(500).TextColor(pal.Text)
		ui.Text(c, "Each folder’s space is marked in its color in the switcher.").FontSize(12).TextColor(pal.Muted)
	})
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		for _, f := range folders {
			c.Key(f.Path)
			name, chosen := filepath.Base(f.Path), p.colorIndex(f.Path)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, name).FontSize(13.5).FontWeight(500).TextColor(pal.Text).SingleLine().Ellipsis("…")
					ui.Text(c, native.Tilde(f.Path)).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
				})
				for i, color := range pal.Projects {
					c.Key(i)
					swatch := ui.ButtonBase(c).Label("Color "+strconv.Itoa(i+1)+" for "+name).Size(22, 22).Radius(11).
						Center().Shrink(0).Cursor(ui.CursorPointer)
					if i == chosen {
						swatch.Border(2, pal.Text)
					}
					swatch.OnClick(func() { p.setColor(f.Path, i) })
					swatch.Children(func() { ui.Box(c).Size(14, 14).Radius(7).Background(color.Mark) })
				}
			})
		}
	})
}

func toggle(c *ui.Context, pal native.Palette, on bool, label, detail string, set func(bool)) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, label).FontSize(13.5).FontWeight(500).TextColor(pal.Text)
			ui.Text(c, detail).FontSize(12).TextColor(pal.Muted)
		})
		value := on
		components.Switch(c, &value, label, components.SwitchOptions{HideLabel: true})
		if value != on {
			set(value)
		}
	})
}

// setColor gives a folder one of the project colors.
func (p *Plugin) setColor(folder string, i int) {
	colors := p.colors.Get()
	next := make(map[string]int, len(colors)+1)
	for k, v := range colors {
		next[k] = v
	}
	next[folder] = i
	p.colors.Set(next)
}
