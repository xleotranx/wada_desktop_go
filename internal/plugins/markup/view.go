package markup

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// sideWidth is the notes column beside the canvas.
const sideWidth = 300

func (p *Plugin) editor(c *ui.Context) {
	pal := native.PaletteFor(c)
	w, h := c.Size()
	width := max(600, w-80)
	cw, ch := width-sideWidth-16, max(240, h-270)
	var v viewport
	if p.preview != nil {
		pw, ph := p.preview.Size()
		v = p.viewport(cw, ch, pw, ph)
	}
	// A focused note selects its mark, so its corners show while it is typed.
	selected := p.selected
	if f := ui.FocusedValue(c, &p.focus); f != 0 && f != canvasFocus && p.index(f) >= 0 {
		selected = f
	}
	native.Dialog(c, &p.open, width+40, "Mark up screenshot", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, "Mark up screenshot").FontSize(18).FontWeight(600).TextColor(pal.Text)
			detail := p.source.File.Name
			if v.W > 0 {
				detail += fmt.Sprintf(" · %d × %d", v.W, v.H)
			}
			switch {
			case p.sentW == 0:
			case p.sentW < v.W:
				detail += fmt.Sprintf(" · sent at %d × %d", p.sentW, p.sentH)
			default:
				detail += " · sent at full size"
			}
			ui.Text(c, detail).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
		p.toolbar(c, pal, v)
		ui.Row(c).FillWidth().Gap(16).Children(func() {
			p.canvas(c, pal, v, cw, ch, selected)
			p.side(c, pal, ch, selected)
		})
		if p.problem != "" {
			ui.Text(c, p.problem).FontSize(12).TextColor(pal.Danger)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Drag to draw · 1–5 ink · ⌘Z undo · ⌘-scroll zooms · Hide blacks out pixels before sending · your file stays unchanged").
				FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			components.Button(c.Key("cancel"), "Cancel", components.ButtonSecondary).OnClick(p.close)
			label := "Use marked-up image"
			switch {
			case p.saving.Running():
				label = "Preparing image…"
			case len(p.marks) == 0 && p.restoring() && p.about == "":
				label = "Use original image"
			}
			components.Button(c.Key("apply"), label, components.ButtonPrimary).
				Disabled(!p.canApply()).OnClick(func() { p.apply(pal) })
		})
	})
	// Escape and the backdrop close the dialog without Cancel; the decoded
	// image and its workers are released once the frame ends.
	if !p.open && p.scope != nil {
		p.ctx.Post(p.close)
	}
}

func (p *Plugin) toolbar(c *ui.Context, pal native.Palette, v viewport) {
	busy := p.busy()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
		for _, t := range []struct {
			tool       tool
			name, keys string
			icon       func() *ui.SVG
		}{
			{pick, "Select", "V", lucide.MousePointer2}, {drawArrow, "Arrow", "A", lucide.ArrowUpRight},
			{drawBox, "Box", "B", lucide.Square}, {drawPin, "Pin", "P", lucide.MapPin},
			{drawPen, "Draw", "D", lucide.Pencil}, {drawHighlight, "Highlight", "H", lucide.Highlighter},
			{drawRedact, "Hide", "R", lucide.EyeOff},
		} {
			iconButton(c, pal, "tool/"+t.name, t.name, t.name+" ("+t.keys+")", t.icon(), p.tool == t.tool, busy,
				func() { p.setTool(t.tool); p.focus = canvasFocus })
		}
		ui.Box(c).Width(1).Height(20).Background(pal.Hairline)
		for i, ink := range pal.Inks {
			ring := pal.Hairline
			if p.ink == i {
				ring = pal.Text
			}
			ui.ButtonBase(c.Key("ink/"+ink.Name)).Label(ink.Name+" ink").Tooltip(ink.Name+" ("+strconv.Itoa(i+1)+")").
				Size(24, 24).Radius(12).Border(2, ring).Center().Cursor(ui.CursorPointer).Disabled(busy).
				OnClick(func() { p.setInk(i); p.focus = canvasFocus }).
				Children(func() { ui.Box(c).Size(16, 16).Radius(8).Background(ink.Mark) })
		}
		ui.Box(c).Width(1).Height(20).Background(pal.Hairline)
		iconButton(c, pal, "model-view", "Show what the model sees", "What the model sees, at the size sent (M)", lucide.Eye(),
			p.modelView, busy || p.sentPreview == nil, func() { p.toggleModelView(v); p.focus = canvasFocus })
		ui.Box(c).Width(1).Height(20).Background(pal.Hairline)
		iconButton(c, pal, "undo", "Undo mark", "Undo (⌘Z)", lucide.Undo2(), false, busy || len(p.undos) == 0, func() { p.undo(); p.focus = canvasFocus })
		iconButton(c, pal, "redo", "Redo mark", "Redo (⇧⌘Z)", lucide.Redo2(), false, busy || len(p.redos) == 0, func() { p.redo(); p.focus = canvasFocus })
		ui.Box(c).Grow(1)
		if v.W == 0 {
			return
		}
		cx, cy := v.CW/2, v.CH/2
		components.Button(c.Key("zoom-out"), "−", components.ButtonGhost).Label("Zoom out").
			Disabled(p.zoom <= 0).OnClick(func() { p.zoomAt(v, v.K/1.25, cx, cy) })
		ui.Text(c, fmt.Sprintf("%d%%", int(v.K*100+.5))).FontSize(12).TextColor(pal.Muted).Width(40).TextAlign(ui.Center)
		components.Button(c.Key("zoom-in"), "+", components.ButtonGhost).Label("Zoom in").
			Disabled(v.K >= 8).OnClick(func() { p.zoomAt(v, v.K*1.25, cx, cy) })
		components.Button(c.Key("fit"), "Fit", components.ButtonGhost).Label("Fit the screenshot").
			Disabled(p.zoom <= 0).OnClick(func() { p.zoom, p.scrollX, p.scrollY = 0, 0, 0 })
		components.Button(c.Key("actual"), "1:1", components.ButtonGhost).Label("Actual size").
			OnClick(func() { p.zoomAt(v, 1, cx, cy) })
	})
}

// iconButton is a square toolbar button showing icon, filled while active.
func iconButton(c *ui.Context, pal native.Palette, key, label, tip string, icon *ui.SVG, active, disabled bool, run func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(tip).Size(30, 30).Radius(7).Center().
		Cursor(ui.CursorPointer).Disabled(disabled).Shrink(0)
	color := pal.Muted
	switch {
	case active:
		b.Background(pal.Selected).Border(1, pal.Hairline)
		color = pal.Text
	case b.Hovered() && !disabled:
		b.Background(pal.Hover)
	}
	if disabled {
		color = pal.Faint
	}
	b.OnClick(run).Children(func() { ui.Icon(c, icon).FontSize(16).TextColor(color) })
}

func (p *Plugin) canvas(c *ui.Context, pal native.Palette, v viewport, cw, ch float32, selected uint64) {
	canvas := ui.Box(c.Key("canvas")).Label("Screenshot canvas").Role(ui.RoleImage).Size(cw, ch).Shrink(0).
		Background(pal.DetailBg).Radius(6).Clip()
	if p.preview == nil {
		canvas.Center().Children(func() {
			if p.loading.Running() {
				ui.Text(c, "Opening screenshot…").FontSize(13).TextColor(pal.Muted)
			}
		})
		return
	}
	cursor := ui.CursorCrosshair
	switch {
	case p.drag.mode == panning:
		cursor = ui.CursorGrabbing
	case p.drag.mode == moving:
		cursor = ui.CursorMove
	case p.tool == pick:
		cursor = ui.CursorDefault
	}
	marks := p.marks
	if p.drag.mode == creating {
		marks = append(marks[:len(marks):len(marks)], p.active)
	}
	preview, sel, handles, ring := p.preview, p.index(selected), pal.Bg, pal.Link
	if p.modelView && p.sentPreview != nil {
		// Drawn over the same rectangle, the smaller copy shows as softly as
		// the model gets it.
		preview = p.sentPreview
	}
	canvas.FocusBind(&p.focus, canvasFocus).Cursor(cursor).
		HandleInput(func(ev ui.InputEvent) bool { return p.input(ev, v, selected) }).
		Draw(func(painter *ui.Painter, bounds ui.Rect) {
			// A zoomed image is larger than the canvas; nothing of it draws
			// past the canvas's edge.
			painter.Clip(bounds, 6, func() { p.paintCanvas(painter, pal, bounds, v, preview, marks, sel, handles, ring) })
		})
}

// paintCanvas draws the image, its marks and the selected mark's handles.
func (p *Plugin) paintCanvas(painter *ui.Painter, pal native.Palette, bounds ui.Rect, v viewport, preview *ui.Bitmap, marks []mark, sel int, handles, ring ui.Color) {
	r := ui.Rect{X: bounds.X + v.X, Y: bounds.Y + v.Y, W: float32(v.W) * v.K, H: float32(v.H) * v.K}
	painter.Image(preview, r, ui.Contain)
	paintMarks(painter, pal, marks, r, v.W, v.H)
	if sel < 0 || sel >= len(marks) {
		return
	}
	m := marks[sel]
	corners := m.corners()
	for _, at := range corners {
		x, y := v.canvas(at)
		handle := ui.Rect{X: bounds.X + x - 5, Y: bounds.Y + y - 5, W: 10, H: 10}
		painter.Fill(handle, handles, 2)
		painter.Stroke(handle, ring, 2, 2)
	}
	if len(corners) == 0 {
		// A pin or a line is only moved: a dashed frame shows it is
		// selected, around its badge or its bounds.
		pad := strokeWidth(v.W, v.H)*v.K + 4
		x0, y0 := v.canvas(m.A)
		x1, y1 := v.canvas(m.B)
		if m.Kind == pin {
			bx, by := m.badgeAt(v.W, v.H)
			x0, y0 = v.X+bx*v.K, v.Y+by*v.K
			x1, y1, pad = x0, y0, m.radius(v.W, v.H)*v.K+4
		}
		painter.StrokeDashed(ui.Rect{X: bounds.X + x0 - pad, Y: bounds.Y + y0 - pad, W: x1 - x0 + 2*pad, H: y1 - y0 + 2*pad}, ring, 4, 1.5)
	}
}

// side lists the marks by number, each with its note, under a note about
// the whole image.
func (p *Plugin) side(c *ui.Context, pal native.Palette, height float32, selected uint64) {
	busy := p.busy()
	ui.Column(c.Key("side")).Width(sideWidth).Height(height).Shrink(0).Gap(8).Children(func() {
		ui.Text(c, "About this screenshot").FontSize(12).FontWeight(600).TextColor(pal.Muted)
		components.TextBox(c.Key("about"), &p.about, components.TextBoxOptions{Multiline: true}).
			Label("About this screenshot").Placeholder("What should change? · optional").Lines(2, 5).FillWidth().Disabled(busy)
		ui.Text(c, fmt.Sprintf("Marks · %d", len(p.marks))).FontSize(12).FontWeight(600).TextColor(pal.Muted)
		if len(p.marks) == 0 {
			ui.Text(c, "Draw a box, an arrow, a pin or a line, then say what it means. The agent reads each note with its number.").
				FontSize(12).TextColor(pal.Faint)
			return
		}
		ui.Scroll(c.Key("marks")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for i, n := range numbers(p.marks) {
					p.row(c, pal, n, p.marks[i], p.marks[i].ID == selected, busy)
				}
			})
		})
	})
}

// row is mark number n with its note, or, for a hidden area (n is 0), a
// line saying so.
func (p *Plugin) row(c *ui.Context, pal native.Palette, n int, m mark, selected, busy bool) {
	ink := inkOf(pal.Inks, m)
	row := ui.Row(c.Key(strconv.FormatUint(m.ID, 10))).FillWidth().AlignItems(ui.Center).Gap(8).Padding(4, 6).Radius(6)
	if selected {
		row.Background(pal.Selected)
	}
	row.OnClick(func() { p.selected = m.ID })
	row.Children(func() {
		remove := fmt.Sprintf("Remove mark %d", n)
		if n == 0 {
			remove = "Remove hidden area"
			ui.Box(c).Size(22, 22).Radius(4).Background(pal.Redaction).Center().Shrink(0).Children(func() {
				ui.Icon(c, lucide.EyeOff()).FontSize(12).TextColor(pal.Inks[0].On)
			})
			ui.Text(c, "Hidden area · blacked out before sending").FontSize(12).TextColor(pal.Muted).Grow(1).MinWidth(0)
		} else {
			ui.Box(c).Size(22, 22).Radius(11).Background(ink.Mark).Center().Shrink(0).Children(func() {
				ui.Text(c, strconv.Itoa(n)).FontSize(11).FontWeight(700).TextColor(ink.On)
			})
			components.TextBox(c.Key("note"), p.note(m.ID), components.TextBoxOptions{}).
				Label(fmt.Sprintf("Note for mark %d", n)).Placeholder(m.Kind.String()+" · what about it?").
				Grow(1).MinWidth(0).Disabled(busy).FocusBind(&p.focus, m.ID)
			if m.detailable() {
				tip := "Also send this part at full resolution"
				if m.Detail {
					tip = "Sending this part at full resolution too"
				}
				iconButton(c, pal, "detail", fmt.Sprintf("Send mark %d at full resolution", n), tip, lucide.ZoomIn(),
					m.Detail, busy, func() { p.setDetail(m.ID, !m.Detail); p.focus = canvasFocus })
			}
		}
		b := ui.ButtonBase(c.Key("remove")).Label(remove).Size(24, 24).Radius(6).
			Center().Cursor(ui.CursorPointer).Disabled(busy).Shrink(0)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
		b.OnClick(func() { p.remove(m.ID) }).
			Children(func() { ui.Icon(c, lucide.X()).FontSize(14).TextColor(pal.Muted) })
	})
}
