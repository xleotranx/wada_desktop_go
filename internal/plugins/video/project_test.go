package video

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewProjectOpens checks that a new project reads back as the editor
// shows it: its root, its scene and its length.
func TestNewProjectOpens(t *testing.T) {
	dir := t.TempDir()
	p, err := newProject(filepath.Join(dir, "intro"), "Launch film")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Launch film" || p.Root.ID != "main" {
		t.Fatalf("project = %+v", p)
	}
	if got, want := p.Duration(), 8.0; got != want {
		t.Fatalf("duration = %v", got)
	}
	if w, h := p.Size(); w != 1920 || h != 1080 {
		t.Fatalf("size = %v×%v", w, h)
	}
	if len(p.Scenes) != 1 {
		t.Fatalf("scenes = %d", len(p.Scenes))
	}
	scene := p.Scenes[0]
	if scene.Comp.ID != "scene-01" || scene.Host.Start != 0 || scene.Host.Duration != 8 {
		t.Fatalf("scene = %+v", scene)
	}
	if got := scene.Comp.registered; len(got) != 1 || got[0] != "scene-01" {
		t.Fatalf("registered = %v", got)
	}
	if got := p.Root.registered; len(got) != 1 || got[0] != "main" {
		t.Fatalf("root registered = %v", got)
	}
}

// TestOpenProjectResolvesTiming reads the timing rules the timeline shows:
// clip references, missing durations and scene windows.
func TestOpenProjectResolvesTiming(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", `<!doctype html><html><head><title>Two shots</title></head><body>
<div id="root" data-composition-id="root" data-width="1280" data-height="720" data-fps="24">
  <img id="a" class="clip" src="assets/a.png" data-start="0" data-duration="2" data-track-index="0" />
  <video id="b" class="clip" src="assets/b.mp4" data-start="a + 1" data-duration="2" data-track-index="1"></video>
  <div id="late" class="clip" data-start="b" data-duration="1" data-track-index="1"></div>
  <div id="scene" data-composition-id="s1" data-composition-src="compositions/one.html" data-start="3" data-duration="4"></div>
</div></body></html>`)
	write(t, dir, "compositions/one.html", `<template><section data-composition-id="s1" data-duration="4"><p class="clip" data-start="0" data-duration="1">Hi</p></section></template>`)
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.FPS != 24 || p.Title != "Two shots" {
		t.Fatalf("project = %+v", p)
	}
	got := map[string]float64{}
	for _, c := range p.Root.Clips {
		got[c.ID] = c.Start
	}
	if got["b"] != 3 || got["late"] != 5 || got["scene"] != 3 {
		t.Fatalf("starts = %v", got)
	}
	if len(p.Scenes) != 1 || p.Scenes[0].Comp.ID != "s1" {
		t.Fatalf("scenes = %+v", p.Scenes)
	}
	// The root has no data-duration: the video ends with its last clip.
	if d := p.Duration(); d != 7 {
		t.Fatalf("duration = %v", d)
	}
}

// TestOpenProjectKeepsMissingScene opens a project whose scene is missing,
// so the check can report it instead of the editor failing.
func TestOpenProjectKeepsMissingScene(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", `<div id="root" data-composition-id="root" data-duration="4">
  <div id="s" data-composition-id="gone" data-composition-src="compositions/gone.html" data-start="0" data-duration="4"></div>
</div>`)
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scenes) != 1 || p.Scenes[0].Comp.File != "compositions/gone.html" {
		t.Fatalf("scenes = %+v", p.Scenes)
	}
}

// TestDiscoverProjects finds the folder's own project and the ones under
// videos/.
func TestDiscoverProjects(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "videos/film-a/index.html", `<title>A</title><div data-composition-id="main"></div>`)
	write(t, dir, "videos/film-b/index.html", `<title>B</title><div data-composition-id="main"></div>`)
	write(t, dir, "videos/notes/readme.md", `not a project`)
	found := discover(dir)
	if len(found) != 2 {
		t.Fatalf("projects = %+v", found)
	}
	if found[0].Title != "A" && found[0].Title != "B" {
		t.Fatalf("titles = %+v", found)
	}
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
