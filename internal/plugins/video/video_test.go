package video

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

type fixture struct {
	h     *kerneltest.Harness
	agent *plugintest.Agent
	ws    *plugintest.Workspace
	chat  *plugintest.Chat
	host  *kittest.Host
	p     *Plugin
	dir   string

	mu     sync.Mutex
	evals  []string
	answer func(code string) (any, error)
}

// start opens a conversation whose folder holds a new video project.
func start(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{chat: &plugintest.Chat{}, dir: t.TempDir(), agent: plugintest.NewAgent()}
	if _, err := newProject(filepath.Join(f.dir, "videos", "video-1"), "Launch film"); err != nil {
		t.Fatal(err)
	}
	f.host = &kittest.Host{Web: func(int) *kittest.WebView {
		return &kittest.WebView{EvalFunc: f.eval}
	}}
	f.ws = &plugintest.Workspace{Convs: []workspace.Conversation{{ID: "s", Folder: f.dir}}}
	f.h = kerneltest.New(t, f.agent.New, f.ws.New, f.chat.New, f.host.New,
		kerneltest.Keep(&f.p, func() *Plugin { return &Plugin{} }))
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir})
	return f
}

// eval answers the preview page the way the browser does: ready, then the
// composition mounted.
func (f *fixture) eval(_ context.Context, code string) (any, error) {
	f.mu.Lock()
	f.evals = append(f.evals, code)
	answer := f.answer
	f.mu.Unlock()
	if answer != nil {
		if v, err := answer(code); v != nil || err != nil {
			return v, err
		}
	}
	switch {
	case strings.Contains(code, "document.readyState"):
		return true, nil
	case strings.Contains(code, "__video.mount"):
		return map[string]any{"mounted": true, "duration": 8.0, "fps": 30.0, "timelines": []any{"main", "scene-01"}}, nil
	case strings.Contains(code, "__video.info"):
		return map[string]any{"mounted": true, "duration": 8.0}, nil
	}
	return nil, nil
}

// ran counts the scripts the page ran that contain part.
func (f *fixture) ran(part string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.evals {
		if strings.Contains(e, part) {
			n++
		}
	}
	return n
}

// open makes the conversation on show a video of the project in the folder.
func (f *fixture) open(t *testing.T, dir string) {
	t.Helper()
	f.p.remember(video{Origin: f.agent.Status().Origin, Session: f.chat.Conv.Session, Dir: dir})
	f.p.update(f.chat.Current())
	f.h.Flush()
}

// TestTheEditorShowsBesideItsConversation opens a video conversation and
// checks the project, its page and its mount.
func TestTheEditorShowsBesideItsConversation(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	if !f.p.open || f.p.project == nil {
		t.Fatalf("open = %v, project = %v, err = %q", f.p.open, f.p.project, f.p.loadErr)
	}
	if !f.p.check.clean() {
		t.Fatalf("check = %+v", f.p.check.Findings)
	}
	if f.p.web == nil {
		t.Fatal("no preview page")
	}
	web := f.p.web.(*kittest.WebView)
	if len(web.Loaded) != 1 || !strings.HasSuffix(web.Loaded[0], "index.html") {
		t.Fatalf("loaded = %v", web.Loaded)
	}
	if f.ran("__video.mount") != 1 {
		t.Fatalf("evals = %v", f.evals)
	}
	if !f.p.info.Mounted || f.p.info.Duration != 8 {
		t.Fatalf("info = %+v", f.p.info)
	}
}

// TestTheEditorDraws keeps the drawer's own layout: the stage, the
// transport and the timeline beside the sidebar.
func TestTheEditorDraws(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 800)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if !tt.HasText("Video editor") {
		t.Fatalf("texts = %v", tt.Texts())
	}
	if !tt.HasText("Timeline") {
		t.Fatalf("texts = %v", tt.Texts())
	}
	tt.Find("Play")
}

// TestTheEditorClosesOnAnotherConversation puts the editor away when the
// conversation on show is not a video.
func TestTheEditorClosesOnAnotherConversation(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	if !f.p.open {
		t.Fatal("the editor did not open")
	}
	f.chat.Open(agent.Session{ID: "other", Directory: f.dir})
	f.h.Flush()
	if f.p.open || f.p.web != nil {
		t.Fatalf("open = %v, web = %v", f.p.open, f.p.web)
	}
}

// TestDraggingAClipWritesItsTiming writes a move into the file, and extends
// the root's duration when the clip now ends past it.
func TestDraggingAClipWritesItsTiming(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01-title", 1, 9, -1)
	f.h.Flush()
	data, err := os.ReadFile(filepath.Join(dir, "compositions", "scene-01.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, `data-start="1"`) || !strings.Contains(html, `data-duration="9"`) {
		t.Fatalf("scene = %s", html)
	}
	root, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	// The scene's own clip grew; the root's length follows the host window.
	if !strings.Contains(string(root), `data-duration="8"`) {
		t.Fatalf("root = %s", root)
	}
}

// TestNewVideoMakesAProject starts a video and opens its chat.
func TestNewVideoMakesAProject(t *testing.T) {
	f := start(t)
	f.chat.Open(agent.Session{ID: "", Directory: f.dir})
	f.h.Flush()
	f.p.newVideoIn(f.dir)
	f.h.Flush()
	// The fixture already made video-1, so the new project is video-2.
	dir := filepath.Join(f.dir, "videos", "video-2")
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Fatalf("the new project is not there: %v", err)
	}
	if f.chat.Conv.Directory != f.dir || !f.p.draft || f.p.draftDir != dir {
		t.Fatalf("conv = %+v, draft = %v, dir = %q", f.chat.Conv, f.p.draft, f.p.draftDir)
	}
	if !f.p.open || f.p.project == nil {
		t.Fatalf("the new video did not open: %q", f.p.loadErr)
	}
}

// TestTheBriefGoesWithTheFirstMessage tells the agent how the editor reads
// the project.
func TestTheBriefGoesWithTheFirstMessage(t *testing.T) {
	f := start(t)
	f.chat.Open(agent.Session{ID: "", Directory: f.dir})
	f.p.newVideoIn(f.dir)
	f.h.Flush()
	if !f.p.open || f.p.draftDir == "" {
		t.Fatalf("the new video did not open: %q", f.p.loadErr)
	}
	draft := chat.Draft{Directory: f.dir, Session: "", DraftID: f.chat.Conv.DraftID}
	out, commit, err := f.p.outgoing(draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 || out.Files[0].Name != briefName {
		t.Fatalf("files = %+v", out.Files)
	}
	if !strings.Contains(string(out.Files[0].Data), "data-composition-id") {
		t.Fatalf("brief = %s", out.Files[0].Data)
	}
	if commit == nil {
		t.Fatal("the brief's commit is missing")
	}
	commit()
	if len(f.p.starts) != 1 {
		t.Fatalf("starts = %v", f.p.starts)
	}
}

// TestNoPagesKeepsTheEditor opens the editor in a window without pages: it
// says why instead of failing.
func TestNoPagesKeepsTheEditor(t *testing.T) {
	f := start(t)
	f.host.Web = nil
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	if f.p.open != true {
		t.Fatal("the editor closed")
	}
	if f.p.webErr == "" {
		t.Fatal("the editor does not say why it shows nothing")
	}
	if len(f.host.Toasts) == 0 {
		t.Fatal("no toast")
	}
}

var _ = kernel.Plugin(nil)

// TestClickingTheTimelineSeeks reads the ruler as time: a click at the middle
// of an eight second video puts the playhead at four.
func TestClickingTheTimelineSeeks(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatalf("timeline missing; texts = %v", tt.Texts())
	}
	// The ruler is the top 22 DIPs of the timeline.
	tt.ClickAt(rect.X+timelinePad+(rect.W-2*timelinePad)/2, rect.Y+8)
	f.h.Flush()
	if f.p.playhead < 3.9 || f.p.playhead > 4.1 {
		t.Fatalf("playhead = %v", f.p.playhead)
	}
}

// TestDraggingAClipOnTheTimeline writes the move into the file: the scene
// moves a second later, and the film grows to hold it.
func TestDraggingAClipOnTheTimeline(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatal("timeline missing")
	}
	y := rect.Y + rulerHeight + rowHeight/2
	tt.Press(rect.X+timelinePad+30, y)
	tt.Move(rect.X+timelinePad+30+91, y)
	tt.Release(rect.X+timelinePad+30+91, y)
	f.h.Flush()
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	if !strings.Contains(html, `data-start="1"`) {
		t.Fatalf("index = %s", html)
	}
	if !strings.Contains(html, `data-duration="9"`) {
		t.Fatalf("the root did not grow: %s", html)
	}
}

// TestTheTimelineEditsAScene edits a clip inside a scene: the scene's own
// time is what the timeline draws, and its file is what a drag writes.
func TestTheTimelineEditsAScene(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	if len(f.p.project.Scenes) != 1 {
		t.Fatalf("scenes = %d", len(f.p.project.Scenes))
	}
	f.p.focusOn(f.p.project.Scenes[0])
	if got := f.p.span(); got != 8 {
		t.Fatalf("span = %v", got)
	}
	if rows := f.p.rows(); len(rows) != 1 || len(rows[0].clips) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if !tt.HasText("scene-01") {
		t.Fatalf("the breadcrumb is missing; texts = %v", tt.Texts())
	}
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatal("timeline missing")
	}
	y := rect.Y + rulerHeight + rowHeight/2
	tt.Press(rect.X+timelinePad+30, y)
	tt.Move(rect.X+timelinePad+30+91, y)
	tt.Release(rect.X+timelinePad+30+91, y)
	f.h.Flush()
	scene, err := os.ReadFile(filepath.Join(dir, "compositions", "scene-01.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scene), `data-start="1"`) {
		t.Fatalf("scene = %s", scene)
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `data-start="0"`) {
		t.Fatalf("the root moved: %s", index)
	}
	f.p.unfocus()
	if f.p.span() != 8 || len(f.p.rows()) != 1 {
		t.Fatalf("root = %v span", f.p.span())
	}
}

// TestDraggingAClipToAnotherTrack writes the lane into the file.
func TestDraggingAClipToAnotherTrack(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "index.html", `<div id="main" data-composition-id="main" data-width="1920" data-height="1080" data-duration="8">
  <div id="a" class="clip" data-start="0" data-duration="2" data-track-index="0">A</div>
  <div id="b" class="clip" data-start="4" data-duration="2" data-track-index="1">B</div>
  <script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>
</div>`)
	f.open(t, dir)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatal("timeline missing")
	}
	x := rect.X + timelinePad + 60
	top := rect.Y + rulerHeight + rowHeight/2
	second := rect.Y + rulerHeight + rowHeight + rowGap + rowHeight/2
	tt.Press(x, top)
	tt.Move(x, second)
	tt.Release(x, second)
	f.h.Flush()
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scanHTML(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.byID("a").value("data-track-index"); got != "1" {
		t.Fatalf("track = %q in %s", got, data)
	}
}

// TestUndoAndRedoPutATimingBack takes a drag back and applies it again.
func TestUndoAndRedoPutATimingBack(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.writeTiming("scene-01", 1, 8, -1)
	f.h.Flush()
	start := func() string {
		data, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := scanHTML(string(data))
		if err != nil {
			t.Fatal(err)
		}
		return doc.byID("scene-01").value("data-start")
	}
	if got := start(); got != "1" {
		t.Fatalf("start = %q", got)
	}
	if !f.p.canUndo() {
		t.Fatal("nothing to undo")
	}
	f.p.undoEdit()
	f.h.Flush()
	if got := start(); got != "0" {
		t.Fatalf("after undo start = %q", got)
	}
	if !f.p.canRedo() {
		t.Fatal("nothing to redo")
	}
	f.p.redoEdit()
	f.h.Flush()
	if got := start(); got != "1" {
		t.Fatalf("after redo start = %q", got)
	}
	// A new edit drops the redo steps.
	f.p.undoEdit()
	f.h.Flush()
	f.p.writeTiming("scene-01", 2, 8, -1)
	f.h.Flush()
	if f.p.canRedo() {
		t.Fatal("a new edit kept the redo steps")
	}
}

// TestTheEditorFillsTheWindow draws the page: the stage, the timeline and the
// way back to the chat.
func TestTheEditorFillsTheWindow(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := ui.NewTester(f.p.pageView, 1200, 800)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	for _, text := range []string{"Video", "Timeline", "Back to the chat", "Play"} {
		if !tt.HasText(text) {
			t.Fatalf("%q is missing; texts = %v", text, tt.Texts())
		}
	}

	// Without a video conversation the page says so and offers the chat.
	f.chat.Open(agent.Session{ID: "other", Directory: f.dir})
	f.h.Flush()
	tt.Frame()
	if !tt.HasText("No video here yet") {
		t.Fatalf("texts = %v", tt.Texts())
	}
}

// TestZoomingTheTimelineMapsTheRuler zooms in and scrubs: the ruler's middle
// is the middle of what the view shows, not of the whole film.
func TestZoomingTheTimelineMapsTheRuler(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	f.p.viewZoom, f.p.viewStart = 2, 2
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatal("timeline missing")
	}
	tt.ClickAt(rect.X+timelinePad+(rect.W-2*timelinePad)/2, rect.Y+8)
	f.h.Flush()
	if f.p.playhead < 3.9 || f.p.playhead > 4.1 {
		t.Fatalf("playhead = %v", f.p.playhead)
	}
	// Scrolling pans forward; the view cannot leave the film.
	tt.Scroll(rect.X+rect.W/2, rect.Y+40, 0, 200)
	f.h.Flush()
	if f.p.viewStart <= 2 {
		t.Fatalf("view start = %v", f.p.viewStart)
	}
	tt.Scroll(rect.X+rect.W/2, rect.Y+40, 0, 4000)
	f.h.Flush()
	if end, span := f.p.viewEnd(), f.p.span(); end > span+1e-6 || f.p.viewStart < span-f.p.viewSpan()-1e-6 {
		t.Fatalf("view = %v..%v of %v", f.p.viewStart, end, span)
	}
	tt.Scroll(rect.X+rect.W/2, rect.Y+40, 0, -4000)
	f.h.Flush()
	if f.p.viewStart != 0 {
		t.Fatalf("view start = %v", f.p.viewStart)
	}
}

// TestAFindingOpensItsScene selects a clip in a scene from the check: the
// timeline follows it there.
func TestAFindingOpensItsScene(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	scene := f.p.project.Scenes[0]
	// The scene's title is on track 0; make it the clip a finding names.
	f.p.focusClip("scene-01-title")
	if f.p.selected != "scene-01-title" {
		t.Fatalf("selected = %q", f.p.selected)
	}
	if f.p.scene == nil || f.p.scene.Host.ID != scene.Host.ID {
		t.Fatalf("scene = %+v", f.p.scene)
	}
	if f.p.playhead != 0 {
		t.Fatalf("playhead = %v", f.p.playhead)
	}
	// A clip of the root takes the timeline back to the root.
	f.p.focusClip("scene-01")
	if f.p.scene != nil {
		t.Fatalf("scene = %+v", f.p.scene)
	}
	if f.p.selected != "scene-01" {
		t.Fatalf("selected = %q", f.p.selected)
	}
}

// TestEditingAClipsWords writes the selected clip's text, takes the edit back
// and applies it again.
func TestEditingAClipsWords(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.focusClip("scene-01-title")
	clip, ok := f.p.clip("scene-01-title")
	if !ok || !clip.TextEditable {
		t.Fatalf("clip = %+v", clip)
	}
	if clip.Text != "Launch film" {
		t.Fatalf("text = %q", clip.Text)
	}
	f.p.text = "Launch night"
	f.p.saveText(clip)
	f.h.Flush()
	if got := sceneTitle(t, dir); got != "Launch night" {
		t.Fatalf("words = %q", got)
	}
	f.p.undoEdit()
	f.h.Flush()
	if got := sceneTitle(t, dir); got != "Launch film" {
		t.Fatalf("after undo = %q", got)
	}
	if f.p.text != "Launch film" {
		t.Fatalf("the field shows %q", f.p.text)
	}
	f.p.redoEdit()
	f.h.Flush()
	if got := sceneTitle(t, dir); got != "Launch night" {
		t.Fatalf("after redo = %q", got)
	}
}

// sceneTitle reads the title element's own words from the scene file.
func sceneTitle(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "compositions", "scene-01.html"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scanHTML(string(data))
	if err != nil {
		t.Fatal(err)
	}
	el := doc.byID("scene-01-title")
	if el == nil {
		t.Fatal("the title is missing")
	}
	return ownText(string(data), el)
}
