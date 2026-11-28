package browser

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/tabs"
)

// paneView is a browser tab: its toolbar over its page.
func (p *Plugin) paneView(c *ui.Context, t tabs.Tab) {
	pal := native.PaletteFor(c)
	pg := p.pages[t.ID]
	ui.Column(c).Fill().Background(pal.Bg).Children(func() {
		if pg == nil {
			return
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Padding(6, 10).Children(func() {
			c.Key("back")
			tool(c, pal, lucide.ArrowLeft, "Back", !pg.back, func() { pg.view.GoBack() })
			c.Key("forward")
			tool(c, pal, lucide.ArrowRight, "Forward", !pg.forward, func() { pg.view.GoForward() })
			c.Key("reload")
			tool(c, pal, lucide.RotateCw, "Reload", pg.view == nil, func() { pg.view.Reload() })
			c.Key("address")
			components.TextBox(c, &pg.address, components.TextBoxOptions{}).Grow(1).FontSize(12.5).
				Placeholder("Address").OnSubmit(func() { p.navigate(pg, pg.address) })
			c.Key("external")
			tool(c, pal, lucide.ExternalLink, "Open in your browser", false, func() { p.host.OpenURL(Normalize(pg.address)) })
		})
		if pg.problem != "" {
			ui.Text(c, pg.problem).FontSize(12).TextColor(pal.DangerText).Padding(0, 12)
		}
		if pg.view != nil {
			c.Key("page")
			pg.view.Show(c).Grow(1)
		}
	})
}

func tool(c *ui.Context, pal native.Palette, icon func() *ui.SVG, label string, disabled bool, on func()) {
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(26, 26).Radius(6).Center()
	col := pal.Faint
	if !disabled {
		col = pal.Muted
		b.Cursor(ui.CursorPointer).OnClick(on)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
	}
	b.Children(func() { ui.Icon(c, icon()).FontSize(13).TextColor(col) })
}

func nativeDialog(c *ui.Context, open *bool, title string, fn func()) {
	native.Dialog(c, open, 440, title, func() {
		pal := native.PaletteFor(c)
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.Text)
		fn()
	})
}
