package video

import (
	"slices"
	"strings"
	"testing"
)

// codes lists the finding codes of a project.
func codes(p *Project) []string {
	c := checkProject(p)
	out := make([]string, 0, len(c.Findings))
	for _, f := range c.Findings {
		out = append(out, f.Code)
	}
	slices.Sort(out)
	return out
}

// TestNewProjectIsClean keeps the project the app starts from valid: a new
// video renders without the check finding anything.
func TestNewProjectIsClean(t *testing.T) {
	dir := t.TempDir()
	p, err := newProject(dir, "Film")
	if err != nil {
		t.Fatal(err)
	}
	c := checkProject(p)
	if !c.clean() || len(c.Findings) != 0 {
		t.Fatalf("findings = %+v", c.Findings)
	}
}

// TestCheckFindsWhatBreaksARender reads the rules the editor reports: a root
// without an id, a video that does not say how it sounds, wall-clock code.
func TestCheckFindsWhatBreaksARender(t *testing.T) {
	p := &Project{}
	root, err := parseComposition(".", "index.html", `<div id="root" data-composition-id="main" data-width="1920" data-height="1080">
  <div id="card" data-start="0" data-duration="2">Hi</div>
  <video id="clip" src="assets/a.mp4" data-start="2"></video>
  <img src="assets/b.png">
  <audio id="sound" src="assets/theme.mp3"></audio>
  <audio src="assets/theme.mp3" data-start="0"></audio>
  <div id="bare">A row the timeline cannot draw</div>
  <div class="clip" id="later" data-start="clip + 1" data-duration="2"></div>
</div>
<script>
  const tl = gsap.timeline({ paused: true, repeat: -1 });
  tl.to("#card", { opacity: 0, duration: Date.now() / 1000 });
  window.__timelines["main"] = tl;
</script>`)
	if err != nil {
		t.Fatal(err)
	}
	p.Root = root
	got := codes(p)
	want := []string{
		"gsap_infinite_repeat",
		"media_missing_id",
		"media_missing_data_start",
		"non_deterministic_code",
		"root_composition_duration_derived",
		"timed_element_missing_clip_class",
		"timeline_element_missing_timing",
		"video_missing_muted",
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	if slices.Contains(got, "media_missing_data_start") {
		// the image is not played media: only the video reports it
		c := checkProject(p)
		for _, f := range c.Findings {
			if f.Code == "media_missing_data_start" && strings.HasPrefix(f.Message, "<img") {
				t.Errorf("the img reported missing data-start: %+v", f)
			}
		}
	}
}

// TestCheckMissingRoot reports a folder's index.html that is not a
// composition at all.
func TestCheckMissingRoot(t *testing.T) {
	p := &Project{Root: &Composition{File: "index.html", src: "<p>hello</p>"}}
	got := codes(p)
	if len(got) != 1 || got[0] != "root_missing_composition_id" {
		t.Fatalf("codes = %v", got)
	}
}

// TestCheckScene checks a scene against its host: the id must match, and a
// missing file is reported rather than opening silently.
func TestCheckScene(t *testing.T) {
	p := &Project{}
	root, _ := parseComposition(".", "index.html", `<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="4">
  <div id="s" data-composition-id="other" data-composition-src="compositions/s.html" data-start="0" data-duration="4"></div>
</div>
<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>`)
	p.Root = root
	comp, _ := parseComposition(".", "compositions/s.html", `<template><section data-composition-id="s" data-duration="4"><p class="clip" data-start="0" data-duration="1">Hi</p></section></template>`)
	p.Scenes = []*Scene{{Host: root.Clips[0], Comp: comp}}
	got := codes(p)
	if !slices.Contains(got, "timeline_id_mismatch") {
		t.Fatalf("codes = %v", got)
	}

	missing := &Project{}
	missingRoot, _ := parseComposition(".", "index.html", `<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="4">
  <div id="s" data-composition-id="gone" data-composition-src="compositions/gone.html" data-start="0" data-duration="4"></div>
</div>
<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>`)
	missing.Root = missingRoot
	missing.Scenes = []*Scene{{Host: missingRoot.Clips[0], Comp: &Composition{File: "compositions/gone.html"}}}
	if got := codes(missing); !slices.Contains(got, "missing_composition_file") {
		t.Fatalf("codes = %v", got)
	}
}

// TestCheckAssetPaths keeps an absolute path and a path that leaves the
// project reported with the linter's own fixes.
func TestCheckAssetPaths(t *testing.T) {
	p := &Project{}
	root, _ := parseComposition(".", "index.html", `<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="2">
  <img class="clip" id="a" src="/assets/logo.png" data-start="0" data-duration="1">
  <img class="clip" id="b" src="../../outside.png" data-start="1" data-duration="1">
</div>
<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>`)
	p.Root = root
	got := codes(p)
	if !slices.Contains(got, "root_path_missing_in_preview") {
		t.Fatalf("codes = %v", got)
	}
	if !slices.Contains(got, "invalid_parent_traversal_in_asset_path") {
		t.Fatalf("codes = %v", got)
	}
}

// TestCheckTimingReadsReferences reports a start that names a clip the
// composition does not have.
func TestCheckTimingReadsReferences(t *testing.T) {
	p := &Project{}
	root, _ := parseComposition(".", "index.html", `<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="4">
  <p id="a" class="clip" data-start="ghost + 1" data-duration="1">Hi</p>
</div>
<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>`)
	p.Root = root
	if !slices.Contains(codes(p), "unresolved_start_reference") {
		t.Fatalf("codes = %v", codes(p))
	}
}
