package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStageIndexWritesHostValues gives every scene host the user's values,
// and leaves a value the host already pins alone.
func TestStageIndexWritesHostValues(t *testing.T) {
	src := `<div data-composition-id="main">
  <div id="s" data-composition-src="compositions/s.html" data-variable-values='{"title":"Pinned"}'></div>
  <div id="t" data-composition-src="compositions/t.html"></div>
</div>`
	out, err := stageIndex(src, map[string]any{"title": "Hello", "accent": "#fff"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scanHTML(out)
	if err != nil {
		t.Fatal(err)
	}
	values := func(id string) map[string]any {
		el := doc.byID(id)
		if el == nil {
			t.Fatalf("%s is missing from %s", id, out)
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(el.value("data-variable-values")), &values); err != nil {
			t.Fatalf("%s values %q: %v", id, el.value("data-variable-values"), err)
		}
		return values
	}
	pinned := values("s")
	if pinned["title"] != "Pinned" || pinned["accent"] != "#fff" {
		t.Fatalf("pinned = %v", pinned)
	}
	free := values("t")
	if free["title"] != "Hello" || free["accent"] != "#fff" {
		t.Fatalf("free = %v", free)
	}
	// The staged file stays valid HTML: its value is a single-quoted JSON
	// attribute, not double quotes inside double quotes.
	if strings.Contains(out, `data-variable-values="`) {
		t.Fatalf("double-quoted JSON: %s", out)
	}
}

// TestRenderDirStagesTheProject links everything but the root file, writes the
// root with the values on the hosts, and cleans up.
func TestRenderDirStagesTheProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", `<div data-composition-id="main">
  <div id="s" data-composition-src="compositions/s.html" data-start="0" data-duration="8"></div>
</div>`)
	write(t, dir, "compositions/s.html", `<template><section data-composition-id="s"></section></template>`)
	write(t, dir, "assets/logo.svg", `<svg/>`)
	stage, cleanup, err := renderDir(dir, map[string]any{"title": "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if stage == dir {
		t.Fatal("the project was not staged")
	}
	index, err := os.ReadFile(filepath.Join(stage, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `"title":"Hello"`) {
		t.Fatalf("staged index = %s", index)
	}
	if _, err := os.Stat(filepath.Join(stage, "compositions", "s.html")); err != nil {
		t.Fatalf("the scene is not linked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "assets", "logo.svg")); err != nil {
		t.Fatalf("the asset is not linked: %v", err)
	}
	cleanup()
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("the staging folder is still there: %v", err)
	}

	// Without values the CLI renders the project where it is.
	same, noop, err := renderDir(dir, nil)
	if err != nil || same != dir {
		t.Fatalf("renderDir without values = %q, %v", same, err)
	}
	noop()
}
