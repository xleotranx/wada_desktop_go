package design

import (
	"os"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
)

func TestInspectHighlightsAndSelectsWithoutActivatingThePage(t *testing.T) {
	f := start(t, "a.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero", "text": "Hello", "html": "<h1 id=hero>Hello</h1>", "x": 10.0, "y": 20.0, "w": 200.0, "h": 40.0}, nil
		}
		return nil, nil
	}
	f.open(t)
	path := f.p.shown()[0].Path
	page := &pointerPage{WebView: f.p.boards[path].web}
	f.p.boards[path].web = page
	tt := ui.NewTester(f.p.drawer, 1100, 800)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if err := tt.Click("Inspect  I"); err != nil {
		t.Fatal(err)
	}
	r, _ := tt.Find("Interactive artboard")
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Move(x, y)
	f.h.Flush()
	tt.Frame()
	if f.p.hover == nil || f.p.hover.Selector != "#hero" || f.p.inspected != nil {
		t.Fatal("hover did not highlight without selecting")
	}
	if _, ok := tt.Find("Hovered design element"); !ok {
		t.Fatal("hover outline missing")
	}
	tt.ClickAt(x, y)
	f.h.Flush()
	tt.Frame()
	if page.presses != 0 || f.p.inspected == nil || f.p.inspected.Path != path {
		t.Fatal("inspect activated the page or lost its selection")
	}
	if _, ok := tt.Find("Selected design element"); !ok {
		t.Fatal("selection outline missing")
	}
}

func TestInspectDropsResultsAfterNavigation(t *testing.T) {
	f := start(t, "a.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero"}, nil
		}
		return nil, nil
	}
	f.open(t)
	a := f.p.shown()[0]
	x, y, _, _ := f.p.vp.screen(a)
	f.p.inspect(x+20, y+20, true)
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	f.h.Flush()
	if f.p.inspected != nil || f.p.hover != nil {
		t.Fatal("inspection followed navigation")
	}
}

func TestSelectedElementIsVisibleInComposerAndAccompaniesEachMessage(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	path := f.p.shown()[0].Path
	f.p.inspected = &inspectedElement{Path: path, element: element{Selector: "#hero", Text: "Hello", HTML: "<h1 id=hero>Hello</h1>"}}
	tt := ui.NewTester(f.p.selectionChip, 400, 100)
	tt.Frame()
	if !tt.HasText("a.html · #hero") {
		t.Fatal("composer does not show the target")
	}
	draft := chat.Draft{Session: "s", Directory: f.dir, Text: "Make this smaller"}
	for range 2 {
		out, err := f.send(draft)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Files) != 1 || out.Files[0].Name != "design-selection.json" || out.Files[0].MIME != "text/plain" || !strings.Contains(string(out.Files[0].Data), `"selector": "#hero"`) {
			t.Fatalf("missing selection context: %+v", out.Files)
		}
	}
	if err := tt.Click("Remove design selection"); err != nil {
		t.Fatal(err)
	}
	out, _ := f.send(draft)
	if len(out.Files) != 0 {
		t.Fatal("removed context was attached")
	}
}

func TestSelectedElementDoesNotAccompanyAnotherConversation(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.inspected = &inspectedElement{Path: f.p.shown()[0].Path, element: element{Selector: "#hero"}}
	out, _ := f.send(chat.Draft{Session: "other", Directory: f.dir, Text: "Hello"})
	if len(out.Files) != 0 {
		t.Fatal("selection leaked to another conversation")
	}
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	tt := ui.NewTester(f.p.selectionChip, 400, 100)
	tt.Frame()
	if tt.HasText("a.html · #hero") {
		t.Fatal("selection followed navigation")
	}
}

func TestDesignControlsFitMinimumCanvas(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			f := start(t, "a.html")
			f.open(t)
			tt := ui.NewTester(f.p.drawer, canvasMinWidth, 700)
			tt.SetDark(dark)
			tt.Frame()
			f.h.Flush()
			tt.Frame()
			add, ok := tt.Find("New artboard")
			if !ok {
				t.Fatal("new artboard control missing")
			}
			present, ok := tt.Find("Present")
			if !ok {
				t.Fatal("present control missing")
			}
			if add.X+add.W >= present.X {
				t.Fatal("toolbar overlaps presentation controls")
			}
			for _, label := range []string{"Inspect  I", "Zoom", "Zoom to fit  ⇧1"} {
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.X+r.W > canvasMinWidth || r.Y+r.H > 700 {
					t.Fatalf("%s is outside the canvas: %+v", label, r)
				}
			}
		})
	}
}

func TestInspectionClearsWhenItsArtboardIsRemoved(t *testing.T) {
	f := start(t, "a.html", "b.html")
	f.open(t)
	path := f.p.shown()[0].Path
	f.p.inspected = &inspectedElement{Path: path, element: element{Selector: "#hero"}}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.p.scan()
	f.h.Flush()
	if f.p.inspected != nil {
		t.Fatal("removed artboard remained the message target")
	}
}
