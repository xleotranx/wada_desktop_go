package design

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

func (p *Plugin) inspectionView(c *ui.Context, pal native.Palette, showHover bool) {
	outline := func(key, label string, hit *inspectedElement, width float32) {
		if hit == nil || hit.W <= 0 || hit.H <= 0 {
			return
		}
		a, ok := p.current().board(hit.Path)
		if !ok {
			return
		}
		ax, ay, aw, ah := p.vp.screen(a)
		k := p.vp.zoom()
		left, top := max(ax, ax+hit.X*k), max(ay, ay+hit.Y*k)
		right, bottom := min(ax+aw, ax+(hit.X+hit.W)*k), min(ay+ah, ay+(hit.Y+hit.H)*k)
		if right <= left || bottom <= top {
			return
		}
		ui.Box(c.Key(key)).Label(label).Absolute().Left(left).Top(top).Width(right-left).Height(bottom-top).
			Border(width, pal.Link).PassThrough()
	}
	if showHover {
		outline("inspect-hover", "Hovered design element", p.hover, 1)
	}
	outline("inspect-selected", "Selected design element", p.inspected, 2)
}

// selectionChip makes the outgoing target explicit and removable before sending.
func (p *Plugin) selectionChip(c *ui.Context) {
	if !p.open || p.inspected == nil {
		return
	}
	hit := p.inspected
	pal := native.PaletteFor(c)
	ui.Row(c.Key("design-selection")).FillWidth().MinWidth(0).Padding(6, 8).Gap(6).AlignItems(ui.Center).
		Label("Design selection").Children(func() {
		ui.Icon(c, lucide.Scan()).FontSize(13).TextColor(pal.Link).Shrink(0)
		ui.Text(c, filepath.Base(hit.Path)+" · "+hit.Selector).FontSize(12).TextColor(pal.Text).
			SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Tooltip(rel(p.project, hit.Path) + " · " + hit.Selector)
		p.iconButton(c, pal, "clear", "Remove design selection", lucide.X, false, false, p.clearInspection)
	})
}
