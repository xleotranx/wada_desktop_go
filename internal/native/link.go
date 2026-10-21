package native

import "github.com/egoist/mygo/ui"

func LinkButton(c *ui.Context, p Palette, label, text string, on func()) ui.Element {
	b := ui.ButtonBase(c).Label(label).OnClick(on).Cursor(ui.CursorPointer)
	b.Children(func() { ui.Text(c, text).FontSize(12).TextColor(p.Link) })
	return b
}
