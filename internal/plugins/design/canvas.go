package design

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// viewport places the page on the canvas: a page pixel at x, y shows at
// X + x·K, Y + y·K in canvas DIPs.
type viewport struct{ K, X, Y float32 }

func (v viewport) zoom() float32 {
	if v.K <= 0 {
		return 1
	}
	return v.K
}

// screen is where an artboard shows on the canvas.
func (v viewport) screen(a artboard) (x, y, w, h float32) {
	k := v.zoom()
	return v.X + a.X*k, v.Y + a.Y*k, a.W * k, a.H * k
}

const (
	minZoom, maxZoom = .05, 4
	fitPad           = 56 // DIPs around fitted artboards
	labelHeight      = 26 // DIPs above an artboard for its name
)

// zoomSteps are the zoom menu's levels and what zooming in and out steps through.
var zoomSteps = []float32{.1, .25, .5, .75, 1, 1.5, 2, 3, 4}

// drag is a pan in progress.
type drag struct {
	on             bool
	x, y           float32 // where it started on the canvas
	startX, startY float32 // the viewport's then
}

// focusedView remembers the canvas before fitting one screen.
type focusedView struct {
	path     string
	viewport viewport
	autoFit  bool
}

// fit shows the whole page, at most at 100%. Before the canvas has a size
// it waits: the next frame that has one posts it.
func (p *Plugin) fit() {
	p.focusView = nil
	p.autoFit = true
	pg := p.current()
	w, h := p.size[0], p.size[1]
	if len(pg.Boards) == 0 || w <= 0 || h <= 0 {
		return
	}
	shown := pg
	shown.Boards = p.shown()
	x0, y0, x1, y1 := shown.bounds()
	p.frame(x0, y0, x1, y1)
}

// focusBoard toggles between fitting one artboard and the previous view.
func (p *Plugin) focusBoard(path string) {
	a, ok := p.current().board(path)
	if !ok {
		return
	}
	if p.focusView != nil && p.focusView.path == path {
		p.restoreView()
		return
	}
	p.frameBoard(a)
}

// frameBoard fits an artboard while retaining the view to return to.
func (p *Plugin) frameBoard(a artboard) {
	if p.focusView == nil || p.focusView.path != a.Path {
		p.focusView = &focusedView{path: a.Path, viewport: p.vp, autoFit: p.autoFit}
	}
	p.selectBoard(a.Path)
	p.frame(a.X, a.Y, a.X+a.W, a.Y+a.H)
	p.autoFit = false
}

// restoreView restores a manual viewport exactly, or refits an automatic view
// to the current canvas size and artboards.
func (p *Plugin) restoreView() {
	previous := p.focusView
	if previous == nil {
		return
	}
	p.focusView = nil
	p.vp, p.autoFit = previous.viewport, previous.autoFit
	if p.autoFit {
		p.fit()
	} else {
		p.setZoom()
	}
}

// frame fits the page box x0, y0, x1, y1 in the canvas.
func (p *Plugin) frame(x0, y0, x1, y1 float32) {
	w, h := p.size[0], p.size[1]
	bw, bh := max(1, x1-x0), max(1, y1-y0)
	k := min(1, (w-2*fitPad)/bw, (h-2*fitPad-labelHeight)/bh)
	k = max(minZoom, k)
	p.vp = viewport{K: k, X: (w-bw*k)/2 - x0*k, Y: (h-bh*k+labelHeight)/2 - y0*k}
	p.setZoom()
}

// zoomAt zooms to k, keeping the page under x, y in place.
func (p *Plugin) zoomAt(k, x, y float32) {
	k = max(minZoom, min(maxZoom, k))
	old := p.vp.zoom()
	px, py := (x-p.vp.X)/old, (y-p.vp.Y)/old
	p.vp = viewport{K: k, X: x - px*k, Y: y - py*k}
	p.autoFit = false
	p.setZoom()
}

// zoomTo zooms to k around the canvas's middle.
func (p *Plugin) zoomTo(k float32) { p.zoomAt(k, p.size[0]/2, p.size[1]/2) }

// zoomStep zooms in (up) or out to the next step of zoomSteps.
func (p *Plugin) zoomStep(up bool) {
	k := p.vp.zoom()
	var next float32
	if up {
		next = maxZoom
		for _, s := range zoomSteps {
			if s > k*1.001 {
				next = s
				break
			}
		}
	} else {
		next = minZoom
		for i := len(zoomSteps) - 1; i >= 0; i-- {
			if zoomSteps[i] < k*.999 {
				next = zoomSteps[i]
				break
			}
		}
	}
	p.zoomTo(next)
}

func (p *Plugin) pan(dx, dy float32) {
	p.vp.X += dx
	p.vp.Y += dy
	p.autoFit = false
}

// boardAt is the artboard on show under the canvas point x, y.
func (p *Plugin) boardAt(x, y float32) (artboard, bool) {
	for _, a := range p.shown() {
		ax, ay, aw, ah := p.vp.screen(a)
		if x >= ax && x < ax+aw && y >= ay && y < ay+ah {
			return a, true
		}
	}
	return artboard{}, false
}

// input takes the canvas's pointer, scroll and keys: on its background,
// and over the pages too while a tool other than Select is chosen.
func (p *Plugin) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputZoom:
		p.zoomAt(p.vp.zoom()*ev.Scale, ev.X, ev.Y)
		return true
	case ui.InputScroll:
		if ev.Mods&ui.Cmd != 0 {
			p.zoomAt(p.vp.zoom()*float32(math.Exp(float64(-ev.DY)*.01)), ev.X, ev.Y)
			return true
		}
		p.pan(-ev.DX, -ev.DY)
		return true
	case ui.InputPointerDown:
		p.focus = true
		if ev.Button == 2 || ev.Button == 0 && (p.tool == handTool || ev.Mods&ui.Alt != 0) {
			p.drag = drag{on: true, x: ev.X, y: ev.Y, startX: p.vp.X, startY: p.vp.Y}
			return true
		}
		if ev.Button != 0 {
			return false
		}
		switch p.tool {
		case commentTool:
			p.comment(ev.X, ev.Y)
		case noteTool:
			p.addNote(ev.X, ev.Y)
		case inspectTool:
			p.inspect(ev.X, ev.Y, true)
		case selectTool, handTool:
			p.closeCard()
			p.selected = ""
		}
		return true
	case ui.InputPointerMove:
		if !p.drag.on {
			if p.tool == inspectTool {
				p.inspect(ev.X, ev.Y, false)
				return true
			}
			return false
		}
		p.vp.X, p.vp.Y = p.drag.startX+ev.X-p.drag.x, p.drag.startY+ev.Y-p.drag.y
		p.autoFit = false
		return true
	case ui.InputPointerUp:
		if !p.drag.on {
			return false
		}
		p.drag = drag{}
		return true
	case ui.InputKeyDown:
		return p.key(ev)
	case ui.InputKeyUp, ui.InputText, ui.InputCompose, ui.InputCommand, ui.InputSelect:
	}
	return false
}

// toolKeys choose tools while the canvas has the keyboard.
var toolKeys = map[ui.Key]tool{ui.KeyV: selectTool, ui.KeyH: handTool, ui.KeyC: commentTool, ui.KeyN: noteTool, ui.KeyI: inspectTool}

// key chooses tools and zooms while the canvas has the keyboard.
func (p *Plugin) key(ev ui.InputEvent) bool {
	if ev.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) != 0 {
		return false
	}
	if t, ok := toolKeys[ev.Key]; ok {
		p.setTool(t)
		return true
	}
	switch {
	case ev.Key == ui.KeyEqual:
		p.zoomStep(true)
	case ev.Key == ui.KeyMinus:
		p.zoomStep(false)
	case ev.Key == ui.Key1 && ev.Mods&ui.Shift != 0:
		p.fit()
	case ev.Key == ui.KeyEscape && (p.tool != selectTool || p.selected != "" || p.inspected != nil):
		p.setTool(selectTool)
		p.clearInspection()
		p.selected = ""
	default:
		return false
	}
	return true
}

func (p *Plugin) setTool(t tool) {
	p.hovering.Cancel()
	p.inspecting.Cancel()
	p.hover = nil
	p.tool = t
	p.drag = drag{}
	if t != commentTool && t != noteTool {
		p.closeCard()
	}
}
