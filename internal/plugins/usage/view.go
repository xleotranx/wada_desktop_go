package usage

import (
	"cmp"
	"fmt"
	"math"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/native"
)

// part is one kind of token in the latest answer's context.
type part struct {
	label string
	n     int
	color ui.Color
}

// parts are the latest answer's tokens by kind: what it read, then what it
// wrote. Reads share the accent and writes the success color, so the bar
// reads as two families.
func (p *Plugin) parts(pal native.Palette) []part {
	t := p.sum.last
	return []part{
		{"Input", t.Input, pal.Orange},
		{"Cache read", t.CacheRead, pal.Orange.Mix(pal.Bg, .5)},
		{"Cache write", t.CacheWrite, pal.Orange.Mix(pal.Bg, .75)},
		{"Output", t.Output, pal.GreenBadge},
		{"Reasoning", t.Reasoning, pal.GreenBadge.Mix(pal.Bg, .5)},
	}
}

// measured reports that there is usage to show.
func (p *Plugin) measured() bool {
	return p.known && (p.sum.cost > 0 || p.sum.last.Context() > 0)
}

// view is the composer's usage button: the cost, the share of the context
// window and a ring, opening the panel. The work panel shows the same while
// it is on show, so the button steps aside.
func (p *Plugin) view(c *ui.Context) {
	used := p.sum.last.Context()
	if !p.measured() || p.chat.WorkShown() {
		return
	}
	pal := native.PaletteFor(c)
	window := p.model().Context
	fraction := -1.0
	if window > 0 && used > 0 {
		fraction = float64(used) / float64(window)
	}
	b := ui.ButtonBase(c).Label("Context window").Height(24).Radius(6).Padding(0, 7).
		AlignItems(ui.Center).Gap(6).Shrink(0).Cursor(ui.CursorPointer)
	switch {
	case p.open:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.open = !p.open })
	b.Children(func() {
		if p.sum.cost > 0 {
			ui.Text(c, dollars(p.sum.cost)).FontSize(12).TextColor(pal.Muted).SingleLine()
		}
		if fraction >= 0 {
			ui.Text(c, strconv.Itoa(int(math.Round(fraction*100)))+"%").FontSize(12).TextColor(pal.Muted).SingleLine()
		}
		if used > 0 {
			ring(c, fraction, level(pal, fraction), pal.Hairline)
		}
	})
	components.Popover(c, b, &p.open, func(panel ui.Element) {
		panel.Label("Usage details").Width(300).Gap(12).Children(func() { p.panel(c, pal, used, window) })
	})
}

// panel breaks the context down, then totals the conversation.
func (p *Plugin) panel(c *ui.Context, pal native.Palette, used, window int) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Context window").FontSize(13).FontWeight(600).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine()
			ui.Text(c, contextValue(used, window)).FontSize(12).TextColor(pal.Muted).Shrink(0)
		})
		p.breakdown(c, pal, used, window)
	})
	divider(c, pal)
	p.totals(c, pal)
}

// contextSection is the work panel's Context: how full the window is, by kind.
func (p *Plugin) contextSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	used, window := p.sum.last.Context(), p.model().Context
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, contextValue(used, window)).FontSize(12.5).TextColor(pal.Text)
		p.breakdown(c, pal, used, window)
	})
}

// costSection is the work panel's Cost: the conversation's totals.
func (p *Plugin) costSection(c *ui.Context) {
	p.totals(c, native.PaletteFor(c))
}

func contextValue(used, window int) string {
	if window > 0 {
		return tokens(used) + " / " + tokens(window)
	}
	return tokens(used) + " tokens"
}

// breakdown is the context as a bar, then a legend line per kind.
func (p *Plugin) breakdown(c *ui.Context, pal native.Palette, used, window int) {
	parts := p.parts(pal)
	// Shares are of the window when it is known, else of what is used.
	whole := cmp.Or(window, used)
	if used > 0 {
		bar(c, pal, parts, max(window-used, 0))
	}
	for i, pt := range parts {
		c.Key(i)
		legend(c, pal, pt.label, tokens(pt.n), share(pt.n, whole), pt.color)
	}
	if window > 0 {
		c.Key("free")
		legend(c, pal, "Free space", tokens(max(window-used, 0)), share(max(window-used, 0), window), pal.Hairline)
	}
}

// totals are the conversation's model, replies, costs and tokens.
func (p *Plugin) totals(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		heading(c, pal, "Conversation")
		m := p.model()
		name := cmp.Or(m.Name, p.sum.model.ID)
		if v := p.sum.model.Variant; v != "" && name != "" {
			name += " · " + v
		}
		if name != "" {
			detail(c, pal, "Model", name)
		}
		detail(c, pal, "Replies", strconv.Itoa(p.sum.replies))
		detail(c, pal, "Total cost", cost(p.sum.cost, m.Free))
		detail(c, pal, "Latest reply", cost(p.sum.lastCost, m.Free))
	})
	divider(c, pal)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		heading(c, pal, "Tokens in this conversation")
		t := p.sum.total
		detail(c, pal, "Input", tokens(t.Input))
		detail(c, pal, "Cache read", tokens(t.CacheRead))
		detail(c, pal, "Cache write", tokens(t.CacheWrite))
		detail(c, pal, "Output", tokens(t.Output))
		detail(c, pal, "Reasoning", tokens(t.Reasoning))
	})
}

// bar is the context window as a strip: each part in its color, then the
// free space as the bare track.
func bar(c *ui.Context, pal native.Palette, parts []part, free int) {
	ui.Row(c).FillWidth().Height(8).Radius(4).Clip().Gap(2).Background(pal.Hairline.Mix(pal.Bg, .4)).Children(func() {
		for i, pt := range parts {
			if pt.n > 0 {
				c.Key(i)
				ui.Box(c).Width(0).Grow(float32(pt.n)).FillHeight().Background(pt.color)
			}
		}
		if free > 0 {
			c.Key("free")
			ui.Box(c).Width(0).Grow(float32(free)).FillHeight()
		}
	})
}

func legend(c *ui.Context, pal native.Palette, label, value, pct string, color ui.Color) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Box(c).Size(9, 9).Radius(2).Shrink(0).Background(color)
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine()
		ui.Text(c, value).FontSize(12.5).TextColor(pal.Muted).Shrink(0)
		ui.Text(c, pct).FontSize(12.5).TextColor(pal.Muted).Width(44).Shrink(0).TextAlign(ui.End)
	})
}

func detail(c *ui.Context, pal native.Palette, label, value string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted).Shrink(0)
		ui.Text(c, value).FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0).TextAlign(ui.End).SingleLine().Ellipsis("…")
	})
}

func heading(c *ui.Context, pal native.Palette, text string) {
	ui.Text(c, text).FontSize(11.5).FontWeight(600).TextColor(pal.Faint).SingleLine()
}

func divider(c *ui.Context, pal native.Palette) {
	ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
}

// level colors the ring: the accent, a warning from 80%, danger from 95%.
func level(pal native.Palette, fraction float64) ui.Color {
	switch {
	case fraction >= .95:
		return pal.Danger
	case fraction >= .8:
		return pal.Warning
	}
	return pal.Orange
}

// ring is a small gauge filled clockwise from 12 o'clock to fraction; a
// fraction at or below zero draws only the track.
func ring(c *ui.Context, fraction float64, fill, track ui.Color) {
	ui.Box(c).Size(13, 13).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		const stroke = 2.5
		radius := (r.W - stroke) / 2
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		var circle ui.Path
		circle.Circle(cx, cy, radius)
		p.StrokePath(&circle, stroke, track)
		if fraction <= 0 {
			return
		}
		if fraction >= 1 {
			p.StrokePath(&circle, stroke, fill)
			return
		}
		var arc ui.Path
		start, sweep := -math.Pi/2, fraction*2*math.Pi
		point := func(a float64) (float32, float32) {
			return cx + radius*float32(math.Cos(a)), cy + radius*float32(math.Sin(a))
		}
		arc.MoveTo(point(start))
		steps := int(math.Ceil(sweep / (math.Pi / 2)))
		delta := sweep / float64(steps)
		k := float32(4.0/3*math.Tan(delta/4)) * radius
		for i := range steps {
			a, b := start+float64(i)*delta, start+float64(i+1)*delta
			x0, y0 := point(a)
			x1, y1 := point(b)
			arc.CubeTo(x0-k*float32(math.Sin(a)), y0+k*float32(math.Cos(a)), x1+k*float32(math.Sin(b)), y1-k*float32(math.Cos(b)), x1, y1)
		}
		p.StrokePath(&arc, stroke, fill)
	})
}

// share spells n as a part of whole: "7.4%", "<0.1%", or "" without a whole.
func share(n, whole int) string {
	switch {
	case whole <= 0:
		return ""
	case n == 0:
		return "0%"
	}
	pct := float64(n) / float64(whole) * 100
	if pct < 0.1 {
		return "<0.1%"
	}
	return trim(fmt.Sprintf("%.1f", pct)) + "%"
}

// cost spells a cost, "Free" for nothing on a free model.
func cost(v float64, free bool) string {
	if v == 0 {
		if free {
			return "Free"
		}
		return "$0.00"
	}
	return dollars(v)
}

// dollars spells a cost: "$0.42", or "<$0.01" for less than a cent.
func dollars(v float64) string {
	if v < 0.005 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// tokens spells a count shortly: "812", "4.2k", "45k", "1.2M".
func tokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		return trim(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	case n < 999_500:
		return strconv.Itoa((n+500)/1000) + "k"
	}
	return trim(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
}

// trim drops a trailing ".0".
func trim(s string) string {
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}
