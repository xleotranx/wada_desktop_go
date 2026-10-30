package design

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestReducedPreviewsReuseTheirFullSizeRenderUntilContentChanges(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	a := f.p.shown()[0]
	b := f.p.boards[a.Path]
	if b.raster == nil {
		t.Fatal("loaded artboard has no reduced preview")
	}
	bitmap := b.raster
	for _, k := range []float32{.1, .25, .5, .75} {
		f.p.zoomTo(k)
		f.p.pan(-30, 20)
		f.h.Flush()
		if b.raster != bitmap {
			t.Fatal("zoom or pan recaptured an unchanged page")
		}
	}
	f.p.applyTweaks(a.Path)
	f.h.Flush()
	if b.raster == nil || b.raster == bitmap {
		t.Fatal("tweaks did not refresh the reduced preview")
	}
	bitmap = b.raster
	f.p.setPreview(a.Path, previewSize{390, 844})
	f.h.Flush()
	if b.raster == nil || b.raster == bitmap || b.rasterSize != (previewSize{390, 844}) {
		t.Fatal("responsive viewport did not replace the reduced preview")
	}
	bitmap = b.raster
	f.p.reload()
	f.h.Flush()
	if b.raster == nil || b.raster == bitmap {
		t.Fatal("reload did not refresh the reduced preview")
	}
}

func TestReducedPreviewDropsCaptureAfterChangingPages(t *testing.T) {
	f := start(t, "first/a.html", "second/b.html")
	f.open(t)
	a := f.p.shown()[0]
	b := f.p.boards[a.Path]
	before := b.raster
	f.p.renderRaster(a.Path, true)
	next := "first"
	if f.p.page == next {
		next = "second"
	}
	f.p.choosePage(next)
	f.h.Flush()
	if b.raster != before || b.rastering.Running() {
		t.Fatal("capture updated an artboard after it was removed")
	}
}

func TestReducedPreviewRefreshesAfterLiveInteractionAndPresentation(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	a := f.p.shown()[0]
	b := f.p.boards[a.Path]
	for _, present := range []bool{false, true} {
		before := b.raster
		if present {
			f.p.present(a.Path)
			f.p.stopPresenting()
		} else {
			f.p.actualBoard(a)
			f.p.zoomTo(.25)
		}
		f.h.Flush()
		if b.raster == nil || b.raster == before {
			t.Fatal("returning from a live page retained the old preview")
		}
	}
}

func TestFailedReducedPreviewStillOpensTheLivePage(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	a := f.p.shown()[0]
	f.web(t, "a.html").PNG = nil
	f.p.renderRaster(a.Path, true)
	f.h.Flush()
	tt := ui.NewTester(f.p.drawer, 1000, 700)
	tt.Frame()
	if err := tt.Click("Open a at 100%"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.vp.zoom() != 1 || f.p.selected != a.Path {
		t.Fatal("failed preview could not open the live artboard")
	}
}
