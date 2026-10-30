package design

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPreviewSizeChangesTheViewportWithoutChangingTheFile(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	a := f.p.shown()[0]
	original, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	web := f.web(t, "a.html")
	f.p.setPreview(a.Path, previewSize{W: 390, H: 844})
	boards := f.p.shown()
	if boards[0].W != 390 || boards[0].H != 844 || boards[1].X != 390+boardGap {
		t.Fatalf("preview layout %+v", boards)
	}
	raw, _ := f.p.found.page(f.p.page)
	if raw.Boards[0].W != a.W || raw.Boards[0].H != a.H {
		t.Fatal("preview replaced the original dimensions")
	}
	tt := ui.NewTester(f.p.drawer, 1000, 700)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	r, ok := tt.Find("Select a")
	if !ok || r.W <= 0 {
		t.Fatal("preview artboard disappeared")
	}
	got, err := os.ReadFile(a.Path)
	if err != nil || string(got) != string(original) || web.Reloads != 0 {
		t.Fatal("preview rewrote or reloaded the HTML")
	}
	f.p.setPreview(a.Path, previewSize{})
	restored := f.p.shown()[0]
	if restored.W != a.W || restored.H != a.H {
		t.Fatal("original dimensions were not restored")
	}
}

func TestPreviewSurvivesReloadAndOriginalUsesUpdatedDeclaredSize(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	path := f.p.shown()[0].Path
	f.p.setPreview(path, previewSize{W: 768, H: 1024})
	f.write(t, "a.html", `<meta name="artboard" content="900x600">`, time.Now().Add(time.Minute))
	f.p.reload()
	f.h.Flush()
	a := f.p.shown()[0]
	if a.W != 768 || a.H != 1024 {
		t.Fatal("reload discarded the preview")
	}
	f.p.setPreview(path, previewSize{})
	a = f.p.shown()[0]
	if a.W != 900 || a.H != 600 {
		t.Fatalf("original is not the current declared size: %+v", a)
	}
}

func TestPreviewClearsInspectionAndDoesNotFollowAnotherDesign(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	path := f.p.shown()[0].Path
	f.p.inspected = &inspectedElement{Path: path, element: element{Selector: "#hero"}}
	f.p.setPreview(path, previewSize{W: 1440, H: 900})
	if f.p.inspected != nil {
		t.Fatal("inspection retained stale viewport coordinates")
	}
	f.p.follow(f.dir, t.TempDir())
	f.h.Flush()
	if len(f.p.previews) != 0 {
		t.Fatal("preview sizes followed another design")
	}
}

func TestPreviewMenuAppliesToSelectedArtboardAndFitsNarrowHeader(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	path := f.p.shown()[1].Path
	f.p.selectBoard(path)
	tt := ui.NewTester(f.p.drawer, canvasMinWidth, 700)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if err := tt.Click("Preview size for b"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Phone — 390 × 844"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	boards := f.p.shown()
	if boards[0].W != defaultW || boards[1].W != 390 || boards[1].H != 844 {
		t.Fatal("menu resized the wrong artboard")
	}
	for _, label := range []string{"Preview size for b", "Export", "Reload the artboards"} {
		r, ok := tt.Find(label)
		if !ok || r.X < 0 || r.X+r.W > canvasMinWidth {
			t.Fatalf("header control %s is outside the canvas: %+v", label, r)
		}
	}
	f.p.inspected = &inspectedElement{Path: path, element: element{Selector: "#hero"}}
	file := f.p.selectionFile()
	var context struct {
		Viewport previewSize `json:"viewport"`
	}
	if err := json.Unmarshal(file.Data, &context); err != nil {
		t.Fatal(err)
	}
	if context.Viewport != (previewSize{390, 844}) {
		t.Fatalf("selection context lost the viewport: %+v", context)
	}
}

func TestCommentsReanchorWhenPreviewSizeChanges(t *testing.T) {
	f := start(t, "a.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "const targets =") {
			return []any{map[string]any{"id": 1, "x": 24.0, "y": 180.0}}, nil
		}
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero"}, nil
		}
		return nil, nil
	}
	f.open(t)
	a := f.p.shown()[0]
	x, y, _, _ := f.p.vp.screen(a)
	f.p.comment(x+20, y+20)
	f.h.Flush()
	f.p.text = "Make this smaller"
	f.p.saveCard()
	f.p.setPreview(a.Path, previewSize{390, 844})
	f.h.Flush()
	if len(f.p.marks) != 1 || f.p.marks[0].X != 24 || f.p.marks[0].Y != 180 {
		t.Fatal("comment remained at stale coordinates after reflow")
	}
}

func TestHiddenCommentTargetsRetainFeedbackAndReturnWithOriginalViewport(t *testing.T) {
	f := start(t, "a.html")
	hidden := true
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "const targets =") {
			if hidden {
				return []any{}, nil
			}
			return []any{map[string]any{"id": 1, "x": 24.0, "y": 80.0}}, nil
		}
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero"}, nil
		}
		return nil, nil
	}
	f.open(t)
	a := f.p.shown()[0]
	x, y, _, _ := f.p.vp.screen(a)
	f.p.comment(x+20, y+20)
	f.h.Flush()
	f.p.text = "More space"
	f.p.saveCard()
	f.p.setPreview(a.Path, previewSize{390, 844})
	f.h.Flush()
	tt := ui.NewTester(f.p.drawer, 1000, 700)
	tt.Frame()
	if _, ok := tt.Find("Comment 1"); ok {
		t.Fatal("hidden target retained an inaccurate pin")
	}
	if len(f.p.pending()) != 1 || f.p.pending()[0].Words != "More space" {
		t.Fatal("hidden target lost its feedback")
	}
	hidden = false
	f.p.setPreview(a.Path, previewSize{})
	f.h.Flush()
	tt.Frame()
	if _, ok := tt.Find("Comment 1"); !ok {
		t.Fatal("pin did not return with its target")
	}
}
