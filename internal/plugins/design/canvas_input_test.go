package design

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
)

// pointerPage takes pointer input as a native WebView does.
type pointerPage struct {
	shell.WebView
	presses int
}

func (p *pointerPage) Show(c *ui.Context) ui.Element {
	return ui.Box(c).Label("Interactive artboard").HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown {
			p.presses++
		}
		return true
	})
}

func TestCanvasToolsInterceptTheArtboardsPointer(t *testing.T) {
	f := start(t, "a.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero"}, nil
		}
		return nil, nil
	}
	f.open(t)
	path := f.p.shown()[0].Path
	page := &pointerPage{WebView: f.p.boards[path].web}
	f.p.boards[path].web = page
	tt := ui.NewTester(f.p.drawer, 1000, 800)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	r, ok := tt.Find("Interactive artboard")
	if !ok {
		t.Fatal("the artboard is not shown")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.ClickAt(x, y)
	tt.Frame()
	if page.presses != 0 || f.p.vp.zoom() != 1 {
		t.Fatal("reduced preview did not open at actual size")
	}
	r, _ = tt.Find("Interactive artboard")
	x, y = r.X+r.W/2, r.Y+r.H/2
	tt.ClickAt(x, y)
	if page.presses != 1 {
		t.Fatal("Select did not give the artboard the pointer")
	}
	if err := tt.Click("Comment  C"); err != nil {
		t.Fatal(err)
	}
	tt.ClickAt(x, y)
	f.h.Flush()
	if page.presses != 1 || f.p.editing == 0 || f.p.mark(f.p.editing).Selector != "#hero" {
		t.Fatalf("presses %d, editing %d, marks %+v", page.presses, f.p.editing, f.p.marks)
	}
}
