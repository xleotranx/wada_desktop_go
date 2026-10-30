package markup

import (
	"math"
	"slices"

	"github.com/egoist/mygo/ui"
)

// viewport places the image on the canvas: X, Y is where its top-left
// corner is, in canvas DIPs, and K how many DIPs one image pixel takes.
type viewport struct {
	X, Y, K float32
	W, H    int     // the image's pixels
	CW, CH  float32 // the canvas
}

func fitScale(cw, ch float32, w, h int) float32 {
	return min(1, cw/float32(w), ch/float32(h))
}

// viewport fits the image in the canvas, or shows it at the zoom chosen,
// scrolled, centered on an axis where it is smaller than the canvas.
func (p *Plugin) viewport(cw, ch float32, w, h int) viewport {
	k := fitScale(cw, ch, w, h)
	if p.zoom > 0 {
		k = p.zoom
	}
	place := func(size float32, pixels int, scroll float32) float32 {
		if span := float32(pixels) * k; span <= size {
			return (size - span) / 2
		}
		return -max(0, min(float32(pixels)-size/k, scroll)) * k
	}
	return viewport{X: place(cw, w, p.scrollX), Y: place(ch, h, p.scrollY), K: k, W: w, H: h, CW: cw, CH: ch}
}

// image is where the canvas position x, y falls on the image, kept on it.
func (v viewport) image(x, y float32) point {
	return point{max(0, min(1, (x-v.X)/(v.K*float32(v.W)))), max(0, min(1, (y-v.Y)/(v.K*float32(v.H))))}
}

// canvas is where an image position shows on the canvas.
func (v viewport) canvas(f point) (float32, float32) {
	return v.X + f.X*float32(v.W)*v.K, v.Y + f.Y*float32(v.H)*v.K
}

// zoomAt sets the zoom to k, keeping the image under x, y in place. At or
// below the fitting scale the image fits again.
func (p *Plugin) zoomAt(v viewport, k, x, y float32) {
	fit := fitScale(v.CW, v.CH, v.W, v.H)
	k = max(fit, min(8, k))
	if k <= fit*1.001 {
		p.zoom, p.scrollX, p.scrollY = 0, 0, 0
		return
	}
	ix, iy := (x-v.X)/v.K, (y-v.Y)/v.K
	p.zoom, p.scrollX, p.scrollY = k, ix-x/k, iy-y/k
	p.clampScroll(v.CW, v.CH, v.W, v.H)
}

func (p *Plugin) clampScroll(cw, ch float32, w, h int) {
	if p.zoom <= 0 {
		return
	}
	p.scrollX = max(0, min(float32(w)-cw/p.zoom, p.scrollX))
	p.scrollY = max(0, min(float32(h)-ch/p.zoom, p.scrollY))
}

// toggleModelView shows the image as the backend sends it, or as it is.
// Shown smaller than sent, the two look alike, so the view zooms in until
// one pixel sent takes one DIP.
func (p *Plugin) toggleModelView(v viewport) {
	if p.sentPreview == nil {
		return
	}
	p.modelView = !p.modelView
	if sent := float32(p.sentW) / float32(v.W); p.modelView && v.K < sent {
		p.zoomAt(v, sent, v.CW/2, v.CH/2)
	}
}

const grab = 8 // DIPs within which a press takes a mark or a corner

// cornerAt returns the corner of mark id under x, y, or -1.
func (p *Plugin) cornerAt(v viewport, id uint64, x, y float32) int {
	i := p.index(id)
	if i < 0 {
		return -1
	}
	for c, at := range p.marks[i].corners() {
		cx, cy := v.canvas(at)
		if abs(cx-x) <= grab && abs(cy-y) <= grab {
			return c
		}
	}
	return -1
}

// markAt returns the index of the topmost mark under x, y: its badge, a
// box's edge, an arrow's or a freehand line, or anywhere on a highlight or
// a hidden area. A press inside a box falls through, so marks drawn within
// it can be taken.
func (p *Plugin) markAt(v viewport, x, y float32) int {
	reach := max(grab, strokeWidth(v.W, v.H)*v.K)
	for i := len(p.marks) - 1; i >= 0; i-- {
		m := p.marks[i]
		if m.numbered() {
			bx, by := m.badgeAt(v.W, v.H)
			if math.Hypot(float64(v.X+bx*v.K-x), float64(v.Y+by*v.K-y)) <= float64(m.radius(v.W, v.H)*v.K) {
				return i
			}
		}
		ax, ay := v.canvas(m.A)
		cx, cy := v.canvas(m.B)
		switch m.Kind {
		case arrow:
			if segmentDistance(x, y, ax, ay, cx, cy) <= reach {
				return i
			}
		case box:
			for _, s := range [][4]float32{{ax, ay, cx, ay}, {cx, ay, cx, cy}, {cx, cy, ax, cy}, {ax, cy, ax, ay}} {
				if segmentDistance(x, y, s[0], s[1], s[2], s[3]) <= reach {
					return i
				}
			}
		case highlight, redact:
			if x >= min(ax, cx) && x <= max(ax, cx) && y >= min(ay, cy) && y <= max(ay, cy) {
				return i
			}
		case pen:
			for j := 1; j < len(m.Points); j++ {
				x0, y0 := v.canvas(m.Points[j-1])
				x1, y1 := v.canvas(m.Points[j])
				if segmentDistance(x, y, x0, y0, x1, y1) <= reach {
					return i
				}
			}
		case pin:
		}
	}
	return -1
}

func segmentDistance(x, y, ax, ay, bx, by float32) float32 {
	dx, dy := bx-ax, by-ay
	t := float32(0)
	if l := dx*dx + dy*dy; l > 0 {
		t = max(0, min(1, ((x-ax)*dx+(y-ay)*dy)/l))
	}
	return float32(math.Hypot(float64(ax+t*dx-x), float64(ay+t*dy-y)))
}

// input takes the canvas's pointer, scroll and keys. selected is the mark
// shown selected, which follows a focused note.
func (p *Plugin) input(ev ui.InputEvent, v viewport, selected uint64) bool {
	if p.busy() || v.W == 0 {
		return false
	}
	switch ev.Kind {
	case ui.InputKeyDown:
		return p.key(ev, v)
	case ui.InputZoom:
		p.zoomAt(v, v.K*ev.Scale, ev.X, ev.Y)
		return true
	case ui.InputScroll:
		if ev.Mods&ui.Cmd != 0 {
			p.zoomAt(v, v.K*float32(math.Exp(float64(-ev.DY)*.01)), ev.X, ev.Y)
			return true
		}
		if p.zoom <= 0 {
			return false
		}
		p.scrollX, p.scrollY = p.scrollX+ev.DX/v.K, p.scrollY+ev.DY/v.K
		p.clampScroll(v.CW, v.CH, v.W, v.H)
		return true
	case ui.InputPointerDown:
		return p.press(ev, v, selected)
	case ui.InputPointerMove:
		return p.follow(ev, v)
	case ui.InputPointerUp:
		if p.drag.mode == idle {
			return false
		}
		p.follow(ev, v)
		p.release(v)
		return true
	case ui.InputKeyUp, ui.InputText, ui.InputCompose, ui.InputCommand, ui.InputSelect:
	}
	return false
}

func (p *Plugin) press(ev ui.InputEvent, v viewport, selected uint64) bool {
	if p.drag.mode != idle {
		return true
	}
	p.focus = canvasFocus
	p.drag = drag{start: v.image(ev.X, ev.Y), x: ev.X, y: ev.Y, before: p.marks, scrollX: p.scrollX, scrollY: p.scrollY}
	if ev.Button == 2 || ev.Button == 0 && ev.Mods&ui.Alt != 0 {
		p.drag.mode = panning
		return true
	}
	if ev.Button != 0 {
		p.drag = drag{}
		return false
	}
	if corner := p.cornerAt(v, selected, ev.X, ev.Y); corner >= 0 {
		p.selected = selected
		p.drag.mode, p.drag.index, p.drag.corner = resizing, p.index(selected), corner
		p.marks = slices.Clone(p.marks)
		return true
	}
	switch p.tool {
	case pick:
		i := p.markAt(v, ev.X, ev.Y)
		if i < 0 {
			p.selected, p.drag.mode = 0, panning
			return true
		}
		p.selected = p.marks[i].ID
		if ev.Clicks >= 2 {
			p.focus, p.drag = p.selected, drag{}
			return true
		}
		p.drag.mode, p.drag.index = moving, i
		p.marks = slices.Clone(p.marks)
	case drawArrow, drawBox, drawPin, drawPen, drawHighlight, drawRedact:
		if len(p.marks) >= maxMarks {
			p.problem = "A screenshot takes at most 99 marks."
			p.drag = drag{}
			return true
		}
		p.active = mark{ID: p.serial + 1, Kind: p.tool.kind(), A: p.drag.start, B: p.drag.start, Ink: p.ink}
		if p.active.Kind == pen {
			p.active.Points = []point{p.drag.start}
		}
		p.drag.mode = creating
	}
	return true
}

func (p *Plugin) follow(ev ui.InputEvent, v viewport) bool {
	at := v.image(ev.X, ev.Y)
	switch p.drag.mode {
	case idle:
		return false
	case creating:
		switch p.active.Kind {
		case pin:
			p.active.A, p.active.B = at, at
		case pen:
			lx, ly := v.canvas(p.active.Points[len(p.active.Points)-1])
			if x, y := v.canvas(at); abs(x-lx)+abs(y-ly) >= 2 {
				p.active = p.active.extended(at)
			}
		case arrow, box, highlight, redact:
			p.active.B = at
		}
	case moving:
		before := p.drag.before[p.drag.index]
		p.marks[p.drag.index] = before.moved(point{at.X - p.drag.start.X, at.Y - p.drag.start.Y})
	case resizing:
		p.marks[p.drag.index] = p.drag.before[p.drag.index].withCorner(p.drag.corner, at)
	case panning:
		if p.zoom > 0 {
			p.scrollX = p.drag.scrollX - (ev.X-p.drag.x)/v.K
			p.scrollY = p.drag.scrollY - (ev.Y-p.drag.y)/v.K
			p.clampScroll(v.CW, v.CH, v.W, v.H)
		}
	}
	return true
}

func (p *Plugin) release(v viewport) {
	d := p.drag
	p.drag = drag{}
	switch d.mode {
	case idle, panning:
	case creating:
		ax, ay := v.canvas(p.active.A)
		bx, by := v.canvas(p.active.B)
		if p.active.Kind != pin && abs(ax-bx)+abs(ay-by) < 6 {
			return
		}
		p.record(p.marks)
		p.serial++
		p.active.Detail = p.active.wantsDetail(v.W, v.H, p.sentW) && p.details() < maxDetails
		p.marks = append(slices.Clone(p.marks), p.active)
		p.selected = p.active.ID
		if p.active.numbered() {
			p.focus = p.active.ID
		}
	case moving, resizing:
		if slices.EqualFunc(d.before, p.marks, mark.same) {
			p.marks = d.before
			return
		}
		p.record(d.before)
	}
}

// key takes the canvas's shortcuts: tools, inks, undo and redo, Delete and
// the arrow keys, which nudge the selected mark a pixel, or ten with Shift.
func (p *Plugin) key(ev ui.InputEvent, v viewport) bool {
	switch {
	case ev.Mods == ui.Cmd && ev.Key == ui.KeyZ:
		p.undo()
	case ev.Mods == ui.Cmd|ui.Shift && ev.Key == ui.KeyZ:
		p.redo()
	case ev.Mods != 0 && ev.Mods != ui.Shift:
		return false
	case ev.Key == ui.KeyBackspace || ev.Key == ui.KeyDelete:
		p.remove(p.selected)
	case ev.Key == ui.KeyV:
		p.setTool(pick)
	case ev.Key == ui.KeyA:
		p.setTool(drawArrow)
	case ev.Key == ui.KeyB:
		p.setTool(drawBox)
	case ev.Key == ui.KeyP:
		p.setTool(drawPin)
	case ev.Key == ui.KeyD:
		p.setTool(drawPen)
	case ev.Key == ui.KeyH:
		p.setTool(drawHighlight)
	case ev.Key == ui.KeyR:
		p.setTool(drawRedact)
	case ev.Key == ui.KeyM:
		p.toggleModelView(v)
	case ev.Key >= ui.Key1 && ev.Key <= ui.Key5:
		p.setInk(int(ev.Key - ui.Key1))
	case ev.Key == ui.KeyLeft, ev.Key == ui.KeyRight, ev.Key == ui.KeyUp, ev.Key == ui.KeyDown:
		i := p.index(p.selected)
		if i < 0 || p.drag.mode != idle {
			return false
		}
		step := float32(1)
		if ev.Mods == ui.Shift {
			step = 10
		}
		d := map[ui.Key]point{ui.KeyLeft: {-step, 0}, ui.KeyRight: {step, 0}, ui.KeyUp: {0, -step}, ui.KeyDown: {0, step}}[ev.Key]
		p.record(p.marks)
		p.marks = slices.Clone(p.marks)
		p.marks[i] = p.marks[i].moved(point{d.X / float32(v.W), d.Y / float32(v.H)})
	default:
		return false
	}
	return true
}
