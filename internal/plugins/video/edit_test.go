package video

import (
	"path/filepath"
	"testing"
)

// TestSetClipTimingChangesOnlyTheValues keeps the agent's file as written
// apart from the two attribute values an edit touches.
func TestSetClipTimingChangesOnlyTheValues(t *testing.T) {
	src := `<div id="root" data-composition-id="main" data-duration="8">
  <div id="card" class="clip" data-start="1" data-duration="2" data-track-index="0">Hi</div>
</div>`
	out, err := setClipTiming(src, "card", 1.5, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div id="root" data-composition-id="main" data-duration="8">
  <div id="card" class="clip" data-start="1.5" data-duration="3" data-track-index="0">Hi</div>
</div>`
	if out != want {
		t.Fatalf("out = %q\nwant %q", out, want)
	}
}

// TestSetClipTimingAddsMissingAttributes writes timing onto a clip that had
// none, before the tag closes.
func TestSetClipTimingAddsMissingAttributes(t *testing.T) {
	src := `<div id="card" class="clip" data-start="0">Hi</div>`
	out, err := setClipTiming(src, "card", 2, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div id="card" class="clip" data-start="2" data-duration="1.25">Hi</div>`
	if out != want {
		t.Fatalf("out = %q\nwant %q", out, want)
	}
}

// TestSetClipTimingQuotesAndInserts keeps single quotes and self-closing tags
// valid.
func TestSetClipTimingQuotesAndInserts(t *testing.T) {
	src := `<img id="a" src="a.png" data-start='0'/>`
	out, err := setClipTiming(src, "a", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := `<img id="a" src="a.png" data-start='1' data-duration="2"/>`
	if out != want {
		t.Fatalf("out = %q\nwant %q", out, want)
	}
}

// TestSetRootDuration writes the film's length.
func TestSetRootDuration(t *testing.T) {
	src := `<div id="root" data-composition-id="main" data-duration="8"></div>`
	out, err := setRootDuration(src, "root", 12.5)
	if err != nil {
		t.Fatal(err)
	}
	if out != `<div id="root" data-composition-id="main" data-duration="12.5"></div>` {
		t.Fatalf("out = %q", out)
	}
}

// TestSetClipTimingRefusesUnknownIDs does not invent an element.
func TestSetClipTimingRefusesUnknownIDs(t *testing.T) {
	if _, err := setClipTiming(`<div id="a"></div>`, "b", 0, 1); err == nil {
		t.Fatal("an unknown id was edited")
	}
}

// TestAddSceneAppendsAScene writes a new scene and mounts it after the last
// one, extending the film's length.
func TestAddSceneAppendsAScene(t *testing.T) {
	dir := t.TempDir()
	p, err := newProject(filepath.Join(dir, "film"), "Film")
	if err != nil {
		t.Fatal(err)
	}
	id, err := addScene(p.Dir, p, 4)
	if err != nil {
		t.Fatal(err)
	}
	if id != "scene-02" {
		t.Fatalf("id = %q", id)
	}
	again, err := openProject(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Scenes) != 2 {
		t.Fatalf("scenes = %d", len(again.Scenes))
	}
	last := again.Scenes[1]
	if last.Host.ID != "scene-02" || last.Host.Start != 8 || last.Host.Duration != 4 {
		t.Fatalf("host = %+v", last.Host)
	}
	if got := again.Duration(); got != 12 {
		t.Fatalf("duration = %v", got)
	}
	if !checkProject(again).clean() {
		t.Fatalf("findings = %+v", checkProject(again).Findings)
	}
}
