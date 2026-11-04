package video

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kit "wada-desktop-go/internal/contract/videokit"
)

func TestEveryExcelComponentCompilesAndSeeks(t *testing.T) {
	f := start(t)
	catalog, err := f.p.collectionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Components) < 14 {
		t.Fatalf("component collection is still small: %d", len(catalog.Components))
	}
	d := &kit.Document{Schema: 1, Title: "Excel component review", Theme: kit.Ref{ID: "wada.sheet", Version: 1}, Snapshot: catalog}
	for i, c := range catalog.Components {
		parts := []kit.Part{{ID: "demo", Component: c.Ref}}
		layout, err := layoutForParts(d, parts)
		if err != nil {
			t.Fatal(err)
		}
		d.Scenes = append(d.Scenes, kit.Scene{ID: fmt.Sprintf("demo-%d", i), Title: c.Title, Layout: layout, Start: float64(i * 8), Duration: 8, Parts: parts})
	}
	dir := t.TempDir()
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if check := checkProject(p); !check.clean() {
		t.Fatalf("components failed the quick check: %+v", check.Findings)
	}
	runPreviewRuntime(t, dir)
}

func TestExcelDemoFieldValidation(t *testing.T) {
	f := start(t)
	c, err := f.p.collectionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id     string
		inputs map[string]any
	}{
		{"excel.range", map[string]any{"row-from": 2.5}},
		{"excel.range", map[string]any{"row-to": 6.0}},
		{"excel.range", map[string]any{"column-to": 4.0}},
		{"excel.range", map[string]any{"row-from": 4.0, "row-to": 2.0}},
		{"excel.conditional", map[string]any{"labels": "East | West", "values": "50"}},
		{"excel.conditional", map[string]any{"values": "36 | NaN | 64 | 95"}},
		{"excel.chart", map[string]any{"values": "35 | 50 | 101 | 85"}},
		{"excel.chart", map[string]any{"values": "35 | 50;background:red | 68 | 85"}},
		{"excel.sort", map[string]any{"transition": "spin-forever"}},
		{"excel.flashfill", map[string]any{"after": "one | two | three"}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			d := &kit.Document{Schema: 1, Title: "Invalid demo", Theme: kit.Ref{ID: "wada.sheet", Version: 1}, Snapshot: c, Scenes: []kit.Scene{{ID: "demo", Layout: kit.Ref{ID: "wada.center", Version: 1}, Duration: 8, Parts: []kit.Part{{ID: "content", Component: kit.Ref{ID: tc.id, Version: 1}, Inputs: tc.inputs}}}}}
			if _, err := compileDocument(d); err == nil {
				t.Fatal("invalid component input compiled")
			}
		})
	}
}

func TestDetailedExcelComponentsRequireReadableLayouts(t *testing.T) {
	_, _, d := structuredProject(t)
	d.Scenes[0].Parts = []kit.Part{{ID: "demo", Component: kit.Ref{ID: "excel.range", Version: 1}}}
	d.Scenes[0].Layout = kit.Ref{ID: "wada.split", Version: 1}
	if _, err := compileDocument(d); err == nil {
		t.Fatal("range demo was squeezed into half the frame")
	}
	d.Scenes[0].Layout = kit.Ref{ID: "wada.center", Version: 1}
	if err := appendPart(d, d.Scenes[0].ID, kit.Ref{ID: "excel.shortcut", Version: 1}); err == nil {
		t.Fatal("standalone demo was combined into an unreadable layout")
	}
}

func TestOlderProjectsCanImportNewExcelComponentsWithoutUpgrading(t *testing.T) {
	f, dir, d := structuredProject(t)
	catalog, err := f.p.collectionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	d.Snapshot.Components = d.Snapshot.Components[:4]
	d.Snapshot.Templates = d.Snapshot.Templates[:2]
	original, err := json.Marshal(d.Snapshot.Components)
	if err != nil {
		t.Fatal(err)
	}
	originalCSS := d.Snapshot.CSS
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	// Even if installed definitions change, import must retain the pinned ones.
	catalog.Components[0].Markup = "A different installed title"
	f.open(t, dir)
	f.p.addComponentFrom(kit.Ref{ID: "excel.filter", Version: 1}, &catalog)
	f.h.Flush()
	after, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := json.Marshal(after.Snapshot.Components[:4])
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(old) || after.Snapshot.CSS != originalCSS {
		t.Fatal("import changed an existing design")
	}
	if len(after.Snapshot.Components) != 5 || len(after.Scenes) != 5 || after.Scenes[4].Duration != 8 {
		t.Fatal("new component was not imported as a ready-to-use scene")
	}
	if !strings.Contains(after.Snapshot.Components[4].CSS, "demo-filter-menu") {
		t.Fatal("import lost the component's styles")
	}
	if _, err := os.Stat(filepath.Join(dir, "compositions", after.Scenes[4].ID+".html")); err != nil {
		t.Fatal(err)
	}
}

func TestExcelActionTransitionsKeepStableBindings(t *testing.T) {
	_, dir, d := structuredProject(t)
	d.Scenes = []kit.Scene{{ID: "demo", Title: "Sort", Duration: 8, Layout: kit.Ref{ID: "wada.center", Version: 1}, Parts: []kit.Part{{ID: "content", Component: kit.Ref{ID: "excel.sort", Version: 1}}}}}
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"wipe", "slide", "zoom"} {
		if err := structuredField(dir, variableID("demo", "content", "transition"), style); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "compositions/demo.html"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `data-effect="`+style+`"`) {
			t.Fatalf("%s did not reach the compiled scene", style)
		}
	}
}
