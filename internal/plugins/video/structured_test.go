package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/chat"
	kit "wada-desktop-go/internal/contract/videokit"
)

func structuredProject(t *testing.T) (*fixture, string, *kit.Document) {
	t.Helper()
	f := start(t)
	c, err := f.p.collectionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Templates) < 4 {
		t.Fatalf("templates=%d", len(c.Templates))
	}
	dir := filepath.Join(f.dir, "videos", "components")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := newStructuredProject(dir, c, c.Templates[0]); err != nil {
		t.Fatal(err)
	}
	d, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	return f, dir, d
}

func TestComponentStartersCompileAndSnapshot(t *testing.T) {
	f := start(t)
	c, err := f.p.collectionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, starter := range c.Templates {
		t.Run(starter.ID, func(t *testing.T) {
			dir := t.TempDir()
			if err := newStructuredProject(dir, c, starter); err != nil {
				t.Fatal(err)
			}
			p, err := openProject(dir)
			if err != nil {
				t.Fatal(err)
			}
			if p.Document == nil || len(p.Scenes) != len(starter.Scenes) {
				t.Fatalf("project=%+v", p)
			}
			if got := checkProject(p); got.Errors != 0 || got.Warnings != 0 {
				t.Fatalf("check=%+v", got)
			}
			// Recompiling preserves bytes and mtimes: a read must not reload the webview.
			before := p.Revision
			p, err = openProject(dir)
			if err != nil {
				t.Fatal(err)
			}
			if before != p.Revision {
				t.Fatal("unchanged document rewrote generated files")
			}
			data, err := os.ReadFile(filepath.Join(dir, "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "assets/gsap-3.15.0.min.js") || strings.Contains(string(data), "fonts.googleapis") {
				t.Fatal("runtime not pinned or external fonts required")
			}
		})
	}
}

func TestStructuredFieldsPersistWithoutTouchingOtherInstances(t *testing.T) {
	_, dir, d := structuredProject(t)
	d.Scenes[0].Parts[0].Inputs["headline"] = "It's an Excel tip"
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	id := variableID("intro", "title", "headline")
	if err := structuredField(dir, id, `It's <b>your</b> "tip"`); err != nil {
		t.Fatal(err)
	}
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Document.Overrides[id] != `It's <b>your</b> "tip"` {
		t.Fatal("override lost")
	}
	scene, err := os.ReadFile(filepath.Join(dir, "compositions/intro.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scene), "It's &lt;b&gt;your&lt;/b&gt;") {
		t.Fatal("user text was not escaped")
	}
	if !strings.Contains(string(scene), "&#39;") {
		t.Fatal("declaration apostrophe not escaped")
	}
	if err := structuredField(dir, id, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Overrides) != 0 {
		t.Fatal("reset did not remove override")
	}
	if restored.Scenes[0].Parts[0].Inputs["headline"] != d.Scenes[0].Parts[0].Inputs["headline"] {
		t.Fatal("reset modified authored input")
	}
}

func TestStructuredTimelineAndUndoUseTheDocument(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.writeTiming("demo", 4, 8, 1)
	f.h.Flush()
	if f.p.project.Scenes[1].Host.Start != 4 || f.p.project.Scenes[1].Host.Duration != 8 {
		t.Fatal("timing not recompiled")
	}
	d, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Scenes[1].Track != 1 {
		t.Fatal("track not persisted")
	}
	f.p.undoEdit()
	f.h.Flush()
	d, err = readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Scenes[1].Start != 3 || d.Scenes[1].Duration != 7 || d.Scenes[1].Track != 0 {
		t.Fatalf("undo=%+v", d.Scenes[1])
	}
	f.p.redoEdit()
	f.h.Flush()
	if f.p.project.Scenes[1].Host.Start != 4 {
		t.Fatal("redo lost")
	}
}

func TestStructuredValidationDoesNotOverwriteLastGoodProject(t *testing.T) {
	_, dir, d := structuredProject(t)
	baseline, err := os.ReadFile(filepath.Join(dir, documentName))
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*kit.Document)
	}{
		{"schema", func(d *kit.Document) { d.Schema = 7 }},
		{"path", func(d *kit.Document) { d.Scenes[0].ID = "../escape" }},
		{"duplicate", func(d *kit.Document) { d.Scenes[1].ID = d.Scenes[0].ID }},
		{"missing component version", func(d *kit.Document) { d.Scenes[0].Parts[0].Component.Version = 90 }},
		{"layout capacity", func(d *kit.Document) { d.Scenes[0].Parts = append(d.Scenes[0].Parts, d.Scenes[1].Parts...) }},
		{"text limit", func(d *kit.Document) { d.Scenes[0].Parts[0].Inputs["headline"] = strings.Repeat("x", 200) }},
		{"unknown field", func(d *kit.Document) { d.Scenes[0].Parts[0].Inputs["oops"] = "x" }},
		{"invalid table", func(d *kit.Document) { d.Scenes[1].Parts[1].Inputs["rows"] = "one | two | three" }},
		{"unknown override", func(d *kit.Document) { d.Overrides = map[string]any{"missing": true} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var broken kit.Document
			data, _ := json.Marshal(d)
			if err := json.Unmarshal(data, &broken); err != nil {
				t.Fatal(err)
			}
			tc.change(&broken)
			if err := saveDocument(dir, &broken); err == nil {
				t.Fatal("invalid document saved")
			}
			after, err := os.ReadFile(filepath.Join(dir, documentName))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(baseline) {
				t.Fatal("source overwritten on validation failure")
			}
			after, err = os.ReadFile(filepath.Join(dir, "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(index) {
				t.Fatal("HTML overwritten on validation failure")
			}
		})
	}
}

func TestComponentsCanBeAddedAndEditedWithoutAI(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.addComponent(kit.Ref{ID: "excel.formula", Version: 1})
	f.h.Flush()
	if len(f.p.project.Scenes) != 5 {
		t.Fatal("component scene not added")
	}
	s := f.p.project.Scenes[4]
	if s.Host.Start != 20 || s.Host.Duration != 6 {
		t.Fatalf("new scene=%+v", s.Host)
	}
	f.p.focusOn(s)
	f.p.panel = varsPanel
	f.p.setVariable(variableID(s.Host.ID, "content", "formula"), "=SUM(B1:B5)")
	f.h.Flush()
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Document.Overrides[variableID(s.Host.ID, "content", "formula")] != "=SUM(B1:B5)" {
		t.Fatal("native variable edit did not persist")
	}
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if !strings.Contains(strings.Join(tt.Texts(), " "), "Formula") {
		t.Fatalf("component inputs missing: %v", tt.Texts())
	}
}

func TestStarterPickerCanOpenCloseAndReturn(t *testing.T) {
	f := start(t)
	tt := ui.NewTester(func(c *ui.Context) { f.p.templatePicker(c) }, 1000, 720)
	f.p.chooseVideoStarter(f.dir)
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), " "), "One Excel tip") {
		t.Fatalf("starter absent: %v", tt.Texts())
	}
	f.p.pickerFolder = ""
	tt.Frame()
	f.p.chooseVideoStarter(f.dir)
	tt.Frame()
	if _, ok := tt.Find("Choose a video starter"); !ok {
		t.Fatal("picker did not return")
	}
}

func TestComponentSnapshotsSurviveCollectionRemoval(t *testing.T) {
	f, dir, _ := structuredProject(t)
	if err := f.h.Disable("video-components"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if len(kernel.Items(f.h, kit.Collections)) != 0 {
		t.Fatal("collection did not dispose")
	}
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Document.Snapshot.Components) < 14 {
		t.Fatal("snapshot definitions lost")
	}
	if _, err := os.Stat(filepath.Join(dir, "assets/gsap-3.15.0.min.js")); err != nil {
		t.Fatal(err)
	}
	if err := structuredField(dir, variableID("intro", "title", "headline"), "Still editable"); err != nil {
		t.Fatal(err)
	}
}

func TestSceneAssemblyDuplicatesAndCombinesIndependentParts(t *testing.T) {
	_, dir, _ := structuredProject(t)
	id := variableID("intro", "title", "headline")
	if err := structuredField(dir, id, "My tip"); err != nil {
		t.Fatal(err)
	}
	if err := changeDocument(dir, func(d *kit.Document) error {
		if err := duplicateScene(d, "intro"); err != nil {
			return err
		}
		duplicate := d.Scenes[1]
		if d.Overrides[variableID(duplicate.ID, "title", "headline")] != "My tip" {
			t.Fatal("override not copied")
		}
		duplicate.Parts[0].Inputs["headline"] = "Independent copy"
		if d.Scenes[0].Parts[0].Inputs["headline"] == "Independent copy" {
			t.Fatal("duplicate aliases its source")
		}
		return appendPart(d, duplicate.ID, kit.Ref{ID: "excel.formula", Version: 1})
	}); err != nil {
		t.Fatal(err)
	}
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scenes) != 5 || len(p.Document.Scenes[1].Parts) != 2 || p.Document.Scenes[1].Layout.ID != "wada.split" {
		t.Fatal("scene assembly lost")
	}
	if p.Scenes[1].Host.Start != 3 || p.Scenes[2].Host.Start != 6 {
		t.Fatal("duplication did not resequence")
	}
	if err := changeDocument(dir, func(d *kit.Document) error {
		return appendPart(d, d.Scenes[1].ID, kit.Ref{ID: "excel.shortcut", Version: 1})
	}); err == nil {
		t.Fatal("overfull scene accepted")
	}
}

func TestStarterSelectionCreatesAWorkingProjectAndStructuredBrief(t *testing.T) {
	f := start(t)
	f.p.chooseVideoStarter(f.dir)
	tt := ui.NewTester(func(c *ui.Context) { f.p.templatePicker(c) }, 1000, 720)
	tt.Frame()
	if err := tt.Click("One Excel tip · AutoSum"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	tt.Frame()
	if f.p.pickerFolder != "" || f.p.project == nil || f.p.project.Document == nil {
		t.Fatal("starter did not open")
	}
	if len(f.p.project.Scenes) != 4 {
		t.Fatal("starter scenes missing")
	}
	out, _, err := f.p.outgoing(chat.Draft{Directory: f.dir, DraftID: f.chat.Conv.DraftID})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 0 || out.Project == nil || !strings.Contains(out.Instructions, "Use project inspect first") {
		t.Fatal("agent did not receive the structured brief")
	}
}

func TestSavingAComponentFieldPreservesAnotherFieldsDraft(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[0])
	var draft *variableEditor
	for _, e := range f.p.editors {
		if strings.HasSuffix(e.ID, "--body") {
			draft = e
		}
	}
	if draft == nil {
		t.Fatal("body field missing")
	}
	draft.text = "Not saved yet"
	f.p.setVariable(variableID("intro", "title", "headline"), "Updated headline")
	f.h.Flush()
	found := false
	for _, e := range f.p.editors {
		if e == draft && e.text == "Not saved yet" {
			found = true
		}
	}
	if !found {
		t.Fatal("saving a field lost another field's draft or identity")
	}
}
