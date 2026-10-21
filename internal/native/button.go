package native

import "github.com/egoist/mygo/ui"

func IconButton(c *ui.Context, p Palette, label string, icon *ui.SVG, on func(), disabled bool) {
	b := ui.ButtonBase(c).Label(label).Size(28, 28).Radius(8).Center().Tooltip(label).Disabled(disabled)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(on)
	tint := p.Text
	if disabled {
		tint = p.Muted
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(tint) })
}
