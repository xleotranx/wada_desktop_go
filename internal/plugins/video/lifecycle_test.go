package video

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kittest"
)

func TestAgentCompletionRefreshesEveryProjectInput(t *testing.T) {
	for _, file := range []string{"index.html", "compositions/scene-01.html", "assets/theme.css", "assets/motion.js", "assets/image.png"} {
		t.Run(file, func(t *testing.T) {
			f := start(t)
			dir := filepath.Join(f.dir, "videos", "video-1")
			path := filepath.Join(dir, filepath.FromSlash(file))
			if strings.HasPrefix(file, "assets/") {
				write(t, dir, file, "old")
			}
			// The latest timestamp belongs to a different file. Comparing max(mtime)
			// would miss this write, even though this file's own timestamp changed.
			future := time.Now().Add(time.Hour)
			other := filepath.Join(dir, "compositions", "scene-01.html")
			if strings.HasPrefix(file, "compositions/") {
				other = filepath.Join(dir, "index.html")
			}
			if err := os.Chtimes(other, future, future); err != nil {
				t.Fatal(err)
			}
			f.open(t, dir)
			before := f.p.web.(*kittest.WebView)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			updated := strings.ReplaceAll(string(src), "Launch film", "Updated cut")
			if updated == string(src) {
				updated = "new"
			}
			write(t, dir, file, updated)
			if file != "assets/image.png" {
				// Source revisions also detect edits with preserved timestamps and size.
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			f.agent.Respond("s", "Finished updating the video")
			f.h.Flush()
			if f.p.web == before || !before.Closed {
				t.Fatal("AI completion kept the stale native document")
			}
			if !f.p.info.Mounted || f.p.loadErr != "" {
				t.Fatalf("preview did not recover: %q", f.p.loadErr)
			}
			if file == "index.html" && f.p.project.Title != "Updated cut" {
				t.Fatal("root title stayed stale")
			}
			if strings.HasPrefix(file, "compositions/") && f.ran("Updated cut") == 0 {
				t.Fatal("new scene content never reached the runtime")
			}
		})
	}
}

func TestRefreshDetectsDeletedAndNewAssetsButIgnoresRenders(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	first := f.p.web
	write(t, dir, "renders/test.mp4", "render output")
	f.p.reload()
	f.h.Flush()
	if f.p.web != first {
		t.Fatal("render output reloaded the preview")
	}
	write(t, dir, "assets/new.css", "body{}")
	f.p.reload()
	f.h.Flush()
	second := f.p.web
	if second == first {
		t.Fatal("new input was missed")
	}
	if err := os.Remove(filepath.Join(dir, "assets/new.css")); err != nil {
		t.Fatal(err)
	}
	f.p.reload()
	f.h.Flush()
	if f.p.web == second {
		t.Fatal("deleted input was missed")
	}
}

func TestRefreshCoalescesReadsAndKeepsTheLastAgentWrite(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.reload()
	f.h.RunWork(0) // old snapshot has been read; its UI result is still queued
	write(t, dir, "compositions/scene-01.html", sceneTemplate("scene-01", "Final revision", 8))
	for range 20 {
		f.p.events([]agent.Event{{Session: "s", Kind: agent.TurnSucceeded}})
	}
	if f.h.Work() != 0 {
		t.Fatal("a notification started overlapping project readers")
	}
	f.h.Flush()
	if sceneTitle(t, dir) != "Final revision" || f.ran("Final revision") == 0 {
		t.Fatal("last write was dropped")
	}
	if f.p.loading.Running() || f.p.reloadAgain {
		t.Fatal("refresh never settled")
	}
}

func TestRefreshWaitsForAnEditorMutation(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 1, 8, -1)
	f.p.events([]agent.Event{{Session: "s", Kind: agent.TurnSucceeded}})
	if f.h.Work() != 1 {
		t.Fatal("project generation overlapped an editor write")
	}
	f.h.Flush()
	if f.p.project.Scenes[0].Host.Start != 1 || f.p.loading.Running() {
		t.Fatal("saved timing was not reloaded")
	}
}

func TestPreviewPayloadUsesTheParsedSceneSnapshot(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	write(t, dir, "compositions/scene-01.html", sceneTemplate("scene-01", "Not yet parsed", 8))
	data := payload(f.p.project, nil)
	if !strings.Contains(data.Scenes[0].HTML, "Launch film") || strings.Contains(data.Scenes[0].HTML, "Not yet parsed") {
		t.Fatal("payload mixed parsed timing with a newer scene file")
	}
}

func TestFailedWritesPreserveUndoRedoAndTheTextDraft(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 1, 8, -1)
	f.h.Flush()
	f.p.undoEdit()
	f.h.Flush()
	if len(f.p.undo) != 0 || len(f.p.redo) != 1 {
		t.Fatal("bad test history")
	}
	path := filepath.Join(dir, "index.html")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.p.redoEdit()
	f.h.Flush()
	if len(f.p.undo) != 0 || len(f.p.redo) != 1 {
		t.Fatal("failed redo consumed its history")
	}
	f.p.writeTiming("scene-01", 2, 8, -1)
	f.h.Flush()
	if len(f.p.undo) != 0 || len(f.p.redo) != 1 {
		t.Fatal("failed timing save changed history")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f.p.focusClip("scene-01-title")
	clip, _ := f.p.clip("scene-01-title")
	f.p.text = "Keep my draft"
	if err := os.Remove(filepath.Join(dir, "compositions/scene-01.html")); err != nil {
		t.Fatal(err)
	}
	f.p.saveText(clip)
	f.h.Flush()
	if len(f.p.undo) != 0 || len(f.p.redo) != 1 || f.p.text != "Keep my draft" {
		t.Fatal("failed text save lost draft/history")
	}
}

func TestUndoRestoresTheRootDurationAndRedoExtendsIt(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 1, 8, -1)
	f.h.Flush()
	if f.p.duration() != 9 {
		t.Fatal("move did not extend film")
	}
	f.p.undoEdit()
	f.h.Flush()
	if f.p.duration() != 8 {
		t.Fatalf("undo left film at %g", f.p.duration())
	}
	f.p.redoEdit()
	f.h.Flush()
	if f.p.duration() != 9 {
		t.Fatal("redo lost extension")
	}
}

func TestAgentChangesInvalidateHistoryButUnchangedTurnsKeepIt(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 1, 8, -1)
	f.h.Flush()
	f.agent.Respond("s", "No changes")
	f.h.Flush()
	if !f.p.canUndo() {
		t.Fatal("unchanged turn lost history")
	}
	write(t, dir, "compositions/scene-01.html", sceneTemplate("scene-01", "AI changes", 8))
	f.agent.Respond("s", "Updated")
	f.h.Flush()
	if f.p.canUndo() || f.p.canRedo() {
		t.Fatal("history can overwrite the agent's changes")
	}
}

func TestFocusingAFindingUsesTheScenesRootTime(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 4, 8, -1)
	f.h.Flush()
	f.p.focusClip("scene-01-title")
	f.h.Flush()
	if f.p.playhead != 4 || f.p.displayTime() != 0 {
		t.Fatalf("finding sought film %g, local %g", f.p.playhead, f.p.displayTime())
	}
}

func TestStoryboardPausesAndRestoresTheFocusedFrameOnFailure(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[1])
	f.h.Flush()
	f.p.seek(5)
	f.h.Flush()
	f.p.togglePlay()
	f.p.web.(*kittest.WebView).PNG = []byte("invalid PNG")
	f.p.takeStoryboard()
	if f.p.playing {
		t.Fatal("capture competed with playback")
	}
	f.h.Flush()
	if (f.p.playhead < 5 || f.p.playhead > 5.01) || f.ran(", 10)") == 0 {
		t.Fatal("failed capture left the stage at another frame")
	}
}

func TestClosingCancelsInFlightPreviewConsumers(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	old := f.p.web.(*kittest.WebView)
	old.EvalFunc = func(ctx context.Context, _ string) (any, error) {
		if !old.Closed && ctx.Err() == nil {
			t.Fatal("unexpected live work")
		}
		if old.Closed && ctx.Err() == nil {
			t.Fatal("a closed page was used by uncancelled work")
		}
		return nil, ctx.Err()
	}
	f.p.seek(2)
	f.chat.Open(agent.Session{ID: "other", Directory: f.dir})
	f.h.Flush()
	if !old.Closed || f.p.open || f.p.web != nil {
		t.Fatal("navigation retained preview")
	}
}

func TestVariableControlsFollowAgentChangesWithoutLosingUnrelatedDrafts(t *testing.T) {
	f, dir, d := structuredProject(t)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[0])
	f.h.Flush()
	editor := f.p.editors[0]
	editor.text = "Unsaved local words"
	f.p.reload()
	f.h.Flush()
	if f.p.editors[0] != editor || editor.text != "Unsaved local words" {
		t.Fatal("unchanged reload discarded the draft")
	}
	id := editor.ID
	d.Overrides = map[string]any{id: "AI changed this field"}
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	f.agent.Respond("s", "Updated fields")
	f.h.Flush()
	if f.p.editors[0] != editor || editor.text != "AI changed this field" {
		t.Fatal("field stayed stale after the agent changed its saved value")
	}
}

func TestRapidVariableEditsSendTheWholeSavedSnapshot(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	before := f.ran("setVariables")
	f.p.setVariable("first", "First change")
	f.p.setVariable("second", "Second change")
	f.h.Flush()
	if f.ran("setVariables") <= before {
		t.Fatal("no variable update reached the page")
	}
	f.mu.Lock()
	last := ""
	for _, script := range f.evals {
		if strings.Contains(script, "setVariables") {
			last = script
		}
	}
	f.mu.Unlock()
	if !strings.Contains(last, "First change") || !strings.Contains(last, "Second change") {
		t.Fatalf("replacement job lost an earlier field: %s", last)
	}
}

func TestLoadFailureClosesStalePreviewAndNextCompletionRecovers(t *testing.T) {
	f, dir, d := structuredProject(t)
	f.open(t, dir)
	previous := f.p.web.(*kittest.WebView)
	write(t, dir, "video.json", "{ invalid JSON")
	f.agent.Respond("s", "Incomplete project")
	f.h.Flush()
	if f.p.loadErr == "" || f.p.project != nil || f.p.web != nil || !previous.Closed {
		t.Fatal("invalid project kept a stale playable preview")
	}
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	f.agent.Respond("s", "Repaired project")
	f.h.Flush()
	if f.p.loadErr != "" || f.p.web == nil || !f.p.info.Mounted {
		t.Fatal("next completion did not recover")
	}
}

func TestRapidStructuredFieldSavesAreQueuedRatherThanDropped(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	headline := variableID("intro", "title", "headline")
	body := variableID("intro", "title", "body")
	f.p.setVariable(headline, "First headline")
	f.p.setVariable(body, "A different field saved during the first write")
	f.p.setVariable(headline, "Latest headline")
	if f.h.Work() != 1 {
		t.Fatal("field writes overlap")
	}
	f.h.Flush()
	d, err := readDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Overrides[headline] != "Latest headline" || d.Overrides[body] != "A different field saved during the first write" {
		t.Fatalf("rapid saves lost content: %v", d.Overrides)
	}
	if len(f.p.pendingFields) != 0 || f.p.editing.Running() {
		t.Fatal("field queue did not drain")
	}
}

func TestFocusedPlayheadFollowsASceneMovedByTheAgent(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[0])
	f.p.seekLocal(2)
	f.h.Flush()
	if err := writeClipTiming(dir, "index.html", f.p.project, "scene-01", 4, 8, -1); err != nil {
		t.Fatal(err)
	}
	f.agent.Respond("s", "Moved the scene")
	f.h.Flush()
	if f.p.scene == nil || f.p.playhead != 6 || f.p.displayTime() != 2 {
		t.Fatalf("focus drifted to film %g / local %g", f.p.playhead, f.p.displayTime())
	}
}
