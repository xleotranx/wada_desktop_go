package native

import "github.com/egoist/mygo/ui"

// Choice draws one option of a small exclusive group, such as an appearance
// or connection mode.
func Choice(c *ui.Context, label string, chosen bool, on func()) {
	p := PaletteFor(c)
	b := ui.ButtonBase(c).Label(label).Height(30).Radius(8).Padding(0, 12).Center().
		Border(1, p.Hairline).Cursor(ui.CursorPointer)
	switch {
	case chosen:
		b.Background(p.GreenBadge.Alpha(.12)).BorderColor(p.GreenBadge.Alpha(.3))
	case b.Hovered():
		b.Background(p.Hover)
	}
	b.OnClick(on)
	b.Children(func() {
		text := ui.Text(c, label).FontSize(13).TextColor(p.Text)
		if chosen {
			text.FontWeight(500).TextColor(p.GreenBadge)
		}
	})
}
