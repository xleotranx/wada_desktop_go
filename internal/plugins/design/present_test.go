package design

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Use the shell's contribution wrapper: a presentation rendered directly at the
// Tester root misses the zero-sized slot that hosts shell.Overlays.
func TestPresentationFillsTheWindowFromItsShellOverlaySlot(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	path := f.p.shown()[0].Path
	original := f.web(t, "a.html")
	page := &pointerPage{WebView: f.p.boards[path].web}
	f.p.boards[path].web = page
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() { f.p.drawer(c) })
		for _, entry := range kernel.Items(f.h, shell.Overlays) {
			ui.Box(c.Key(entry.ID)).Children(func() {
				kernel.Invoke(f.h, entry, func(v shell.View) { v.Render(c) })
			})
		}
	}, 1200, 800)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	before := f.p.vp
	if err := tt.Click("Present a"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	r, ok := tt.Find("Presenting")
	if !ok || r.X != 0 || r.Y != 0 || r.W != 1200 || r.H != 800 {
		t.Fatalf("presentation did not cover the window: found %v, bounds %+v", ok, r)
	}
	web, ok := tt.Find("Interactive artboard")
	if !ok || web.W != 1200 || web.H != 756 {
		t.Fatalf("presented page has no space: %+v", web)
	}
	tt.ClickAt(web.X+web.W/2, web.Y+web.H/2)
	if page.presses != 1 {
		t.Fatal("presented page does not receive input")
	}
	if err := tt.Click("Next artboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("2 of 2") {
		t.Fatal("presentation did not advance")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if _, ok := tt.Find("Presenting"); ok || f.p.presenting != "" {
		t.Fatal("Escape did not close the presentation")
	}
	if original.Zoom != float64(max(1, before.zoom())) || f.p.vp != before {
		t.Fatal("canvas viewport was not restored")
	}
}
