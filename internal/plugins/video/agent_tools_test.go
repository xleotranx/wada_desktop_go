package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/chat"
)

func TestVideoToolInspectsOnlyTheBoundProjectAndUpdatesWithoutDefinitions(t *testing.T) {
	f, dir, d := structuredProject(t)
	output, err := videoProjectTool(t.Context(), dir, "inspect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "components") || !strings.Contains(output, "scenes") || strings.Contains(output, "gsap.min") || strings.Contains(output, "markup") {
		t.Fatal("inspect did not return a compact authoring catalog")
	}
	before, err := json.Marshal(d.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	output, err = videoProjectTool(t.Context(), dir, "update", json.RawMessage(`{"title":"Two useful Excel tips"}`))
	if err != nil || !strings.Contains(output, `"errors":0`) {
		t.Fatalf("update %s / %v", output, err)
	}
	changed, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(changed.Snapshot)
	if changed.Title != "Two useful Excel tips" || string(before) != string(after) {
		t.Fatal("update replaced reusable definitions")
	}
	f.open(t, dir)
	for _, input := range []string{`{"snapshot":{}}`, `{"scenes":[]}`, `{"title":null}`} {
		saved, err := os.ReadFile(filepath.Join(dir, documentName))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := videoProjectTool(t.Context(), dir, "update", json.RawMessage(input)); err == nil {
			t.Fatalf("invalid update accepted: %s", input)
		}
		current, _ := os.ReadFile(filepath.Join(dir, documentName))
		if string(current) != string(saved) {
			t.Fatal("invalid update touched the document")
		}
	}
}

func TestVideoToolRejectsTraversalSymlinksHiddenPathsAndGeneratedWrites(t *testing.T) {
	_, dir, _ := structuredProject(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte(`{"private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../video-1/index.html", "/etc/passwd", ".wada/private.json", "assets/../../private.json", "outside/secret.json", "outside/new/deep.json", "run.sh"} {
		args, _ := json.Marshal(map[string]any{"path": path, "content": "overwrite"})
		for _, action := range []string{"read", "write"} {
			if _, err := videoProjectTool(t.Context(), dir, action, args); err == nil {
				t.Fatalf("%s escaped scope with %s", action, path)
			}
		}
	}
	if _, err := videoProjectTool(t.Context(), dir, "write", json.RawMessage(`{"path":"index.html","content":"overwrite"}`)); err == nil {
		t.Fatal("agent can overwrite generated HTML")
	}
}

func TestHTMLVideoToolReadsWritesAndChecks(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	args := json.RawMessage(`{"path":"assets/style.css","content":"body { margin: 0; }"}`)
	if _, err := videoProjectTool(t.Context(), dir, "write", args); err != nil {
		t.Fatal(err)
	}
	got, err := videoProjectTool(t.Context(), dir, "read", json.RawMessage(`{"path":"assets/style.css"}`))
	if err != nil || got != "body { margin: 0; }" {
		t.Fatalf("read %q/%v", got, err)
	}
	got, err = videoProjectTool(t.Context(), dir, "check", nil)
	if err != nil || !strings.Contains(got, `"errors":0`) {
		t.Fatalf("check %s/%v", got, err)
	}
}

func TestEveryVideoTurnGetsCurrentProjectScopeIncludingProjectSwitches(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	for range 2 {
		d, _, err := f.p.outgoing(chat.Draft{Session: "s", Directory: f.dir, Text: "Create a video"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Project == nil || d.Project.Directory != dir || d.Project.Kind != "video" || !strings.Contains(d.Instructions, "session metadata") || !strings.Contains(d.Instructions, "project tool") {
			t.Fatal("video turn has no authoring scope")
		}
		if len(d.Files) != 0 {
			t.Fatal("repeat turn duplicated the brief attachment")
		}
	}
	next := filepath.Join(f.dir, "videos", "next")
	if _, err := newProject(next, "Next"); err != nil {
		t.Fatal(err)
	}
	f.open(t, next)
	d, _, err := f.p.outgoing(chat.Draft{Session: "s", Directory: f.dir})
	if err != nil || d.Project == nil || d.Project.Directory != next || strings.Contains(d.Instructions, dir+"/") {
		t.Fatal("project switch sent stale scope")
	}
	plain, _, _ := f.p.outgoing(chat.Draft{Session: "other", Directory: f.dir})
	if plain.Project != nil || plain.Instructions != "" {
		t.Fatal("video scope leaked into another chat")
	}
}

func TestProjectInspectionAndGenerationCannotReadThroughExternalSymlinks(t *testing.T) {
	for _, action := range []string{"inspect", "update", "check"} {
		t.Run(action, func(t *testing.T) {
			_, dir, _ := structuredProject(t)
			outside := filepath.Join(t.TempDir(), "outside.json")
			if err := os.WriteFile(outside, []byte(`{"private":"outside"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, documentName)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, documentName)); err != nil {
				t.Fatal(err)
			}
			if output, err := videoProjectTool(t.Context(), dir, action, json.RawMessage(`{"title":"Updated"}`)); err == nil || strings.Contains(output, "outside") {
				t.Fatalf("symlink boundary bypassed: %s/%v", output, err)
			}
		})
	}
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	outside := filepath.Join(t.TempDir(), "scene.html")
	if err := os.WriteFile(outside, []byte(sceneTemplate("scene-01", "Private", 8)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "compositions/scene-01.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "compositions/scene-01.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := videoProjectTool(t.Context(), dir, "inspect", nil); err == nil {
		t.Fatal("HTML inspect read a scene outside the video")
	}
}

func TestProjectToolCanInspectAndRepairInvalidScenes(t *testing.T) {
	_, dir, d := structuredProject(t)
	d.Scenes[0].Duration = 1
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, documentName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := videoProjectTool(t.Context(), dir, "inspect", nil); err != nil {
		t.Fatal("invalid scenes cannot be inspected for repair:", err)
	}
	d.Scenes[0].Duration = 3
	input, err := json.Marshal(map[string]any{"scenes": d.Scenes})
	if err != nil {
		t.Fatal(err)
	}
	output, err := videoProjectTool(t.Context(), dir, "update", input)
	if err != nil || !strings.Contains(output, `"errors":0`) {
		t.Fatalf("repair failed: %s/%v", output, err)
	}
}
