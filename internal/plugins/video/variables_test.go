package video

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestDeclaredVariables reads a composition's declarations and drops the
// entries the runtime cannot use.
func TestDeclaredVariables(t *testing.T) {
	src := `<html data-composition-variables='[
  {"id":"title","type":"string","label":"Title","default":"Pro"},
  {"id":"accent","type":"color","label":"Accent","default":"#6c5ce7"},
  {"id":"count","type":"number","label":"Count","default":3,"min":0,"max":10},
  {"id":"badge","type":"boolean","label":"Badge","default":false},
  {"id":"plan","type":"enum","label":"Plan","default":"pro","options":["pro","team"]},
  {"id":"broken","type":"enum","label":"Broken","default":"x"},
  {"id":"wrong","type":"number","label":"Wrong","default":"three"},
  {"id":"unknown","type":"date","label":"Unknown","default":"today"}
]'><body><div data-composition-id="card"></div></body></html>`
	doc, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	got := declaredVariables(doc)
	ids := make([]string, 0, len(got))
	for _, v := range got {
		ids = append(ids, v.ID)
	}
	want := []string{"title", "accent", "count", "badge", "plan"}
	if !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if got[2].Min == nil || *got[2].Min != 0 || got[2].Max == nil || *got[2].Max != 10 {
		t.Fatalf("count = %+v", got[2])
	}
	if !slices.Equal(got[4].Options, []string{"pro", "team"}) {
		t.Fatalf("plan = %+v", got[4])
	}
}

// TestDeclaredVariablesOnRoot reads declarations on a scene's composition
// root, inside its template.
func TestDeclaredVariablesOnRoot(t *testing.T) {
	doc, _ := scanHTML(`<template><section data-composition-id="s" data-composition-variables='[{"id":"logo","type":"image","label":"Logo","default":"assets/logo.svg"}]'></section></template>`)
	got := declaredVariables(doc)
	if len(got) != 1 || got[0].defaultString() != "assets/logo.svg" {
		t.Fatalf("variables = %+v", got)
	}
}

// TestMergedVariables layers defaults, the user's values and the scene's
// pinned values the way the platform does: the mount wins.
func TestMergedVariables(t *testing.T) {
	got := mergedVariables(
		map[string]any{"title": "Pro", "accent": "#000"},
		map[string]any{"title": "Mine"},
		map[string]any{"title": "Pinned"},
	)
	if got["title"] != "Pinned" || got["accent"] != "#000" {
		t.Fatalf("merged = %v", got)
	}
}

// TestMountScriptCarriesValues keeps the values the editor shows in the page
// it mounts.
func TestMountScriptCarriesValues(t *testing.T) {
	p, err := newProject(t.TempDir(), "Film")
	if err != nil {
		t.Fatal(err)
	}
	script, err := mountScript(p, map[string]any{"title": "Hello", "count": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `"variables":{"count":2,"title":"Hello"}`) {
		t.Fatalf("script = %.400s", script)
	}
}

// TestRenderArgs passes the values the user changed to the CLI.
func TestRenderArgs(t *testing.T) {
	args := renderArgs("/tmp/out.mp4", `{"title":"Hello"}`)
	want := []string{"--yes", "hyperframes", "render", "--output", "/tmp/out.mp4", "--variables", `{"title":"Hello"}`}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v", args)
	}
	if args := renderArgs("/tmp/out.mp4", ""); len(args) != 5 {
		t.Fatalf("args without values = %v", args)
	}
}

// TestProgressLine reads the CLI's progress lines.
func TestProgressLine(t *testing.T) {
	if got := progressOf(`@hf-progress {"code":"encode","done":52,"total":240,"pct":78}`); got != 78 {
		t.Fatalf("pct = %d", got)
	}
	for _, line := range []string{"", "  ██ 75%  Encoding video", "@hf-progress not json", `@hf-progress {"pct":140}`} {
		if got := progressOf(line); got != -1 {
			t.Fatalf("progressOf(%q) = %d", line, got)
		}
	}
}

// TestEditingAVariableKeepsAndClearsIt sets a value, keeps it, and puts it
// back to the default.
func TestEditingAVariableKeepsAndClearsIt(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "compositions/scene-01.html", `<template>
  <section id="scene-01" data-composition-id="scene-01" data-duration="8"
    data-composition-variables='[{"id":"title","type":"string","label":"Title","default":"Launch day"}]'>
    <h1 id="scene-01-title" class="clip" data-start="0" data-duration="8" data-var-text="title">Launch day</h1>
  </section>
</template>`)
	f.open(t, dir)
	if len(f.p.editors) != 0 {
		t.Fatalf("the root has editors: %+v", f.p.editors)
	}
	f.p.focusOn(f.p.project.Scenes[0])
	if len(f.p.editors) != 1 || f.p.editors[0].ID != "title" {
		t.Fatalf("editors = %+v", f.p.editors)
	}
	f.p.setVariable("title", "Hello")
	f.h.Flush()
	if f.p.values["title"] != "Hello" {
		t.Fatalf("values = %v", f.p.values)
	}
	if f.p.saved[dir]["title"] != "Hello" {
		t.Fatalf("saved = %v", f.p.saved)
	}
	if f.ran("setVariable") == 0 {
		t.Fatal("the page was not told")
	}
	e := f.p.editors[0]
	f.p.resetVariable(e)
	f.h.Flush()
	if _, ok := f.p.values["title"]; ok {
		t.Fatalf("the value was kept: %v", f.p.values)
	}
	if e.text != "Launch day" {
		t.Fatalf("editor = %q", e.text)
	}
	if _, ok := f.p.saved[dir]; ok {
		t.Fatalf("saved = %v", f.p.saved)
	}
}

// TestVariableValuesSurviveRelaunch reads the values back from the settings.
func TestVariableValuesSurviveRelaunch(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.setVariable("title", "Remembered")
	f.h.Flush()
	raw, err := json.Marshal(f.p.saved)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved[dir]["title"] != "Remembered" {
		t.Fatalf("saved = %v", saved)
	}
}

// TestTheVariablesPanelShowsTheDeclarations draws the sidebar's Variables
// tab with the focused composition's declarations.
func TestTheVariablesPanelShowsTheDeclarations(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "compositions/scene-01.html", `<template>
  <section id="scene-01" data-composition-id="scene-01" data-duration="8"
    data-composition-variables='[{"id":"title","type":"string","label":"Headline","default":"Launch day"},{"id":"accent","type":"color","label":"Accent","default":"#8a7fd6"}]'>
    <h1 id="scene-01-title" class="clip" data-start="0" data-duration="8" data-var-text="title">Launch day</h1>
  </section>
</template>`)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[0])
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if err := tt.Click("Vars"); err != nil {
		t.Fatalf("no Variables tab: %v", err)
	}
	f.h.Flush()
	tt.Frame()
	if !tt.HasText("Headline") || !tt.HasText("Accent") {
		t.Fatalf("texts = %v", tt.Texts())
	}
	if !tt.HasText("Back to the main composition") {
		t.Logf("breadcrumb texts = %v", tt.Texts())
	}
}

// TestCheckVariables reports the declarations the runtime cannot use and the
// bindings that name nothing.
func TestCheckVariables(t *testing.T) {
	p := &Project{}
	root, err := parseComposition(".", "index.html", `<html data-composition-variables='[
	  {"id":"title","type":"string","label":"Title","default":"Hello"},
	  {"id":"logo","type":"image","label":"Logo","default":"javascript:alert(1)"},
	  {"id":"nope","type":"number","label":"Nope","default":"three"}
	]'><body>
	<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="4">
	  <h1 id="h" class="clip" data-start="0" data-duration="4" data-var-text="ghost">Hi</h1>
	</div>
	<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>
	</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	p.Root = root
	c := checkProject(p)
	var codes []string
	for _, f := range c.Findings {
		codes = append(codes, f.Code)
	}
	for _, want := range []string{"unloadable_media_variable_default", "invalid_composition_variables_declaration", "unknown_variable_binding"} {
		if !slices.Contains(codes, want) {
			t.Errorf("missing %s in %v", want, codes)
		}
	}
}

// TestCheckVariablesInvalidJSON catches an attribute that is not JSON at all.
func TestCheckVariablesInvalidJSON(t *testing.T) {
	p := &Project{}
	root, _ := parseComposition(".", "index.html", `<html data-composition-variables='not json'><body>
	<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="1"></div>
	<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>
	</body></html>`)
	p.Root = root
	if !slices.Contains(codes(p), "invalid_composition_variables_declaration") {
		t.Fatalf("codes = %v", codes(p))
	}
}
