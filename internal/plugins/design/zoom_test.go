package design

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
)

type measuredPage struct {
	shell.WebView
	handle ui.Handle
}

func (p *measuredPage) Show(c *ui.Context) ui.Element {
	return p.WebView.Show(c).Bind(&p.handle)
}

func TestZoomedOutArtboardRetainsItsFullLayoutViewport(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.zoomTo(.25)
	a := f.p.shown()[0]
	web := f.web(t, "a.html")
	page := &measuredPage{WebView: web}
	f.p.boards[a.Path].web = page
	var r ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		f.p.drawer(c)
		r = page.handle.Bounds(c)
	}, 1000, 700)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if math.Abs(float64(r.W-a.W)) > 1 || math.Abs(float64(r.H-a.H)) > 1 {
		t.Fatalf("page viewport %+v, want %g × %g", r, a.W, a.H)
	}
	if got := web.Zoom; got != 1 {
		t.Fatalf("page zoom %v: reduced browser zoom distorts small fonts", got)
	}
}

func TestFitScreenRestoresPreviousViewport(t *testing.T) {
	for _, autoFit := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual view", true: "fit all"}[autoFit], func(t *testing.T) {
			f := start(t, "a.html", "b.html")
			f.open(t)
			if !autoFit {
				f.p.vp = viewport{K: 1.5, X: -120, Y: 40}
				f.p.autoFit = false
				f.p.setZoom()
			}
			before := f.p.vp
			path := f.p.shown()[0].Path
			f.p.focusBoard(path)
			if f.p.vp == before {
				t.Fatal("screen did not fit")
			}
			f.p.focusBoard(path)
			if f.p.vp != before || f.p.autoFit != autoFit {
				t.Fatalf("previous view not restored: %+v, want %+v", f.p.vp, before)
			}
			if got := f.web(t, "a.html").Zoom; got != float64(max(1, before.zoom())) {
				t.Fatalf("page zoom %v, want %v", got, max(1, before.zoom()))
			}
		})
	}
}

func TestCanvasHasVisibleZoomButtonsAndRestoreControl(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	tt := ui.NewTester(f.p.drawer, 1000, 700)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	before := f.p.vp
	if err := tt.Click("Fit screen a"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("Restore view for a"); !ok {
		t.Fatal("screen control did not offer restore")
	}
	if err := tt.Click("Restore previous view"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.vp != before {
		t.Fatal("restore control changed the previous view")
	}
	k := f.p.vp.K
	if err := tt.Click("Zoom in"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.vp.K <= k {
		t.Fatal("plus did not zoom in")
	}
	zoomedIn := f.p.vp.K
	if err := tt.Click("Zoom out"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.vp.K >= zoomedIn {
		t.Fatal("minus did not zoom out")
	}
}

func TestFitScreenRestoreDoesNotFollowNavigation(t *testing.T) {
	f := start(t, "first/a.html", "second/b.html")
	f.open(t)
	f.p.focusBoard(f.p.shown()[0].Path)
	other := "first"
	if f.p.page == other {
		other = "second"
	}
	f.p.choosePage(other)
	if f.p.focusView != nil {
		t.Fatal("previous view followed another page")
	}
	f.p.focusBoard(f.p.shown()[0].Path)
	f.p.fit()
	if f.p.focusView != nil {
		t.Fatal("fit all retained an obsolete restore target")
	}
}

func TestFitScreenRestoreRefitsAnAutomaticViewAfterResize(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	f.p.focusBoard(f.p.shown()[0].Path)
	f.p.size = [2]float32{1400, 900}
	f.p.restoreView()
	got := f.p.vp
	f.p.fit()
	if got != f.p.vp || !f.p.autoFit {
		t.Fatal("restored automatic view did not fit the resized canvas")
	}
}

func TestZoomControlsAndPendingCommentsFitMinimumCanvas(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 20)
	f.p.text = "More space"
	f.p.saveCard()
	tt := ui.NewTester(f.p.drawer, canvasMinWidth, 700)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	comments, ok := tt.Find("Add 1 comment to the message")
	if !ok {
		t.Fatal("pending comment control missing")
	}
	for _, label := range []string{"Zoom in", "Zoom out", "Zoom"} {
		r, ok := tt.Find(label)
		if !ok || r.X < 0 || r.X+r.W > canvasMinWidth || r.Y+r.H > 700 {
			t.Fatalf("%s is outside the canvas: %+v", label, r)
		}
		if comments.Y+comments.H > r.Y {
			t.Fatal("comments overlap zoom controls")
		}
	}
	for _, k := range []float32{minZoom, maxZoom} {
		f.p.zoomTo(k)
		tt.Frame()
		before := f.p.vp
		label := "Zoom out"
		if k == maxZoom {
			label = "Zoom in"
		}
		tt.Click(label)
		tt.Frame()
		if f.p.vp != before {
			t.Fatalf("%s changed the view beyond its limit", label)
		}
	}
}
