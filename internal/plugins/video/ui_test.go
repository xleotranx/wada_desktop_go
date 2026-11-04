package video

import (
	"image/color"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// drawerTester builds the editor beside an empty conversation pane, the way
// the drawer shows it.
func drawerTester(f *fixture, w, h int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, w, h)
}

// settle draws the editor until the plugin's loaded project and its first
// layout are on screen.
func settle(t *testing.T, f *fixture, tt *ui.Tester) {
	t.Helper()
	for range 3 {
		tt.Frame()
		f.h.Flush()
	}
}

// pixel reads one pixel of the window the tester drew.
func pixel(tt *ui.Tester, x, y int) color.RGBA {
	img := tt.Image()
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return color.RGBA{}
	}
	return img.RGBAAt(x, y)
}

// TestTheRulerStaysInsideTheTimeline paints the ruler's seconds and marks in
// the timeline's own box: nothing of it lands on the header, the stage or
// the conversation beside the editor.
func TestTheRulerStaysInsideTheTimeline(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)

	// The conversation pane beside the editor is empty: every pixel of it is
	// the same background.
	base := pixel(tt, 30, 40)
	for _, at := range [][2]int{{12, 40}, {106, 40}, {200, 40}, {12, 21}, {200, 21}} {
		if got := pixel(tt, at[0], at[1]); got != base {
			t.Fatalf("the ruler painted at %v: %v, the pane is %v", at, got, base)
		}
	}

	// The ruler does paint its ticks inside the timeline: at two seconds the
	// mark differs from the ruler around it.
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatalf("timeline missing; texts = %v", tt.Texts())
	}
	w := rect.W
	x := int(rect.X + timelinePad + (2.0/8.0)*(w-2*timelinePad))
	y := int(rect.Y + rulerHeight - 2)
	bg := pixel(tt, x+10, y)
	marked := false
	for dx := -2; dx <= 2; dx++ {
		if pixel(tt, x+dx, y) != bg {
			marked = true
			break
		}
	}
	if !marked {
		t.Fatalf("the ruler has no tick at two seconds (row y=%d)", y)
	}
}

// TestTheHeaderFitsANarrowEditor keeps every action of a narrow editor on
// screen: the check keeps its icon and drops its words, and the composition's
// size gives way to the transport.
func TestTheHeaderFitsANarrowEditor(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	f.p.focusOn(f.p.project.Scenes[0])
	tt := drawerTester(f, 620, 760)
	settle(t, f, tt)

	drawer, ok := tt.Find("Video editor")
	if !ok {
		t.Fatalf("drawer missing; texts = %v", tt.Texts())
	}
	for _, label := range []string{"Reload the project", "Open the editor in the window", "Render options",
		"Back to the main composition", "Storyboard the video", "Undo the last timing change (⌘Z)"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q is missing; texts = %v", label, tt.Texts())
		}
		if r.X < drawer.X || r.X+r.W > drawer.X+drawer.W+1 {
			t.Fatalf("%q is outside the drawer: %+v in %+v", label, r, drawer)
		}
	}
	chip, ok := tt.Find("No problems")
	if !ok {
		t.Fatalf("the check is missing; texts = %v", tt.Texts())
	}
	if chip.W > 50 {
		t.Fatalf("the check kept its words in a narrow editor: %+v", chip)
	}
	if tt.HasText("1920×1080 · 30 fps") {
		t.Fatalf("the transport kept the composition's size in a narrow editor: %v", tt.Texts())
	}

	// A wide editor keeps both.
	wide := drawerTester(f, 1440, 820)
	settle(t, f, wide)
	chip, ok = wide.Find("No problems")
	if !ok || chip.W <= 50 {
		t.Fatalf("the check lost its words in a wide editor: %+v", chip)
	}
	if !wide.HasText("1920×1080 · 30 fps") {
		t.Fatalf("the transport lost the composition's size in a wide editor: %v", wide.Texts())
	}
}

// TestThePlayButtonSaysPauseWhilePlaying names what the button does.
func TestThePlayButtonSaysPauseWhilePlaying(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	f.p.playing = true
	defer func() { f.p.playing = false }()
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if _, ok := tt.Find("Pause"); !ok {
		t.Fatalf("no Pause; texts = %v", tt.Texts())
	}
	if _, ok := tt.Find("Play"); ok {
		t.Fatalf("the button still says Play; texts = %v", tt.Texts())
	}
}

// TestSelectingAClipOpensItsDetails shows the clip's panel when the timeline
// selects one, and focuses a scene without pretending its host is selected.
func TestSelectingAClipOpensItsDetails(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)

	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatalf("timeline missing; texts = %v", tt.Texts())
	}
	x := rect.X + timelinePad + 60
	y := rect.Y + rulerHeight + rowHeight/2
	tt.Press(x, y)
	f.h.Flush()
	if f.p.selected != "scene-01" || f.p.panel != clipPanel {
		t.Fatalf("selected = %q, panel = %v", f.p.selected, f.p.panel)
	}
	tt.Release(x, y)
	f.h.Flush()

	// A scene row edits the scene's own clips; the host clip belongs to the
	// root and is not what it selects.
	if err := tt.Click("Scenes"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	tt.Frame()
	if err := tt.Click("Edit scene-01"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	f.h.Flush()
	if f.p.scene == nil {
		t.Fatal("the scene did not focus")
	}
	if f.p.selected != "" {
		t.Fatalf("the scene row selected %q", f.p.selected)
	}
}

// TestTheRenderButtonExplainsTheErrors keeps the render clickable when the
// check blocks it, so the click can say why.
func TestTheRenderButtonExplainsTheErrors(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "index.html", `<div id="main" data-composition-id="main" data-width="1920" data-height="1080" data-fps="30" data-duration="8">
  <div id="scene-01" data-composition-id="scene-01" data-composition-src="compositions/scene-01.html" data-start="0" data-duration="8" data-track-index="0"></div>
</div>`)
	f.open(t, dir)
	if f.p.check.Errors == 0 {
		t.Fatalf("the project has no errors: %+v", f.p.check.Findings)
	}
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if err := tt.Click("Render"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	f.h.Flush()
	if f.p.rendering.Running() {
		t.Fatal("the render started over the errors")
	}
	found := false
	for _, toast := range f.host.Toasts {
		if toast == "Fix the errors first" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no explanation; toasts = %v", f.host.Toasts)
	}
}

// tracksProject is a project whose composition has eight lanes, more than the
// timeline can show at once.
const tracksProject = `<div id="main" data-composition-id="main" data-width="1920" data-height="1080" data-fps="30" data-duration="8">
  <div id="a" class="clip" data-start="0" data-duration="8" data-track-index="0">A</div>
  <div id="b" class="clip" data-start="0" data-duration="8" data-track-index="1">B</div>
  <div id="c" class="clip" data-start="0" data-duration="8" data-track-index="2">C</div>
  <div id="d" class="clip" data-start="0" data-duration="8" data-track-index="3">D</div>
  <div id="e" class="clip" data-start="0" data-duration="8" data-track-index="4">E</div>
  <div id="f" class="clip" data-start="0" data-duration="8" data-track-index="5">F</div>
  <div id="g" class="clip" data-start="0" data-duration="8" data-track-index="6">G</div>
  <div id="h" class="clip" data-start="0" data-duration="8" data-track-index="7">H</div>
  <script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>
</div>`

// TestTracksScrollWhenTheyDoNotFit reaches the lanes below the timeline: a
// wheel scrolls them, and the clip there can then be selected.
func TestTracksScrollWhenTheyDoNotFit(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "index.html", tracksProject)
	f.open(t, dir)
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if p := f.p.maxScroll(); p <= 0 {
		t.Fatalf("the tracks fit; maxScroll = %v", p)
	}
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatalf("timeline missing; texts = %v", tt.Texts())
	}
	tt.Scroll(rect.X+rect.W/2, rect.Y+rulerHeight+10, 0, 40)
	f.h.Flush()
	if f.p.tlScroll <= 0 {
		t.Fatalf("the wheel did not scroll the tracks: %v", f.p.tlScroll)
	}
	tt.Scroll(rect.X+rect.W/2, rect.Y+rulerHeight+10, 0, 2000)
	f.h.Flush()
	if f.p.tlScroll != f.p.maxScroll() {
		t.Fatalf("scroll = %v, max = %v", f.p.tlScroll, f.p.maxScroll())
	}
	y := float32(rulerHeight+7*(rowHeight+rowGap)+rowHeight/2) - f.p.tlScroll
	if track, ok := f.p.trackAt(y); !ok || track != 7 {
		t.Fatalf("the last lane is at track %d, %v (y = %v)", track, ok, y)
	}
	tt.ClickAt(rect.X+timelinePad+60, rect.Y+y)
	f.h.Flush()
	if f.p.selected != "h" {
		t.Fatalf("selected = %q, not the clip on the last lane", f.p.selected)
	}
}

// TestAShortClipStillMoves gives a clip too short for its edges most of its
// bar to moving it.
func TestAShortClipStillMoves(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	write(t, dir, "index.html", `<div id="main" data-composition-id="main" data-width="1920" data-height="1080" data-fps="30" data-duration="8">
  <div id="tiny" class="clip" data-start="0" data-duration="0.05" data-track-index="0">Tiny</div>
  <div id="normal" class="clip" data-start="1" data-duration="2" data-track-index="1">Normal</div>
  <script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>
</div>`)
	f.open(t, dir)
	const w = float32(778)
	row0, _ := clipBand(0)
	tiny, kind := f.p.clipAt(f.p.xOf(0, w)+3, row0+10, w)
	if tiny == nil || tiny.ID != "tiny" || kind != dragMove {
		t.Fatalf("the short clip gives %+v, %v", tiny, kind)
	}
	row1, _ := clipBand(1)
	normal, kind := f.p.clipAt(f.p.xOf(1, w)+1, row1+10, w)
	if normal == nil || normal.ID != "normal" || kind != dragTrimStart {
		t.Fatalf("the normal clip's edge gives %+v, %v", normal, kind)
	}
}

// TestAFindingWithoutAClipKeepsTheCheck leaves the check on show when its
// finding has no clip to open, and opens the clip when it has one.
func TestAFindingWithoutAClipKeepsTheCheck(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	// No window.__timelines: an error with no clip. The loose element is a
	// warning with one.
	write(t, dir, "index.html", `<div id="main" data-composition-id="main" data-width="1920" data-height="1080" data-fps="30" data-duration="8">
  <div id="loose" data-start="0" data-duration="2" data-track-index="0">Loose</div>
</div>`)
	f.open(t, dir)
	var plain, withClip string
	for _, finding := range f.p.check.Findings {
		if finding.Clip == "" && plain == "" {
			plain = finding.Code
		}
		if finding.Clip != "" && withClip == "" {
			withClip = finding.Code
		}
	}
	if plain == "" || withClip == "" {
		t.Fatalf("findings = %+v", f.p.check.Findings)
	}
	f.p.panel = checkPanel
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if err := tt.Click(plain); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	f.h.Flush()
	if f.p.panel != checkPanel {
		t.Fatalf("a finding with no clip left the check: panel = %v", f.p.panel)
	}
	if err := tt.Click(withClip); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	f.h.Flush()
	if f.p.panel != clipPanel || f.p.selected != "loose" {
		t.Fatalf("panel = %v, selected = %q", f.p.panel, f.p.selected)
	}
}

// TestAddingASceneShowsTheRoot adds a scene while a scene is focused: the
// timeline goes back to the root, where the new host is, and selects it.
func TestAddingASceneShowsTheRoot(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[0])
	if f.p.scene == nil {
		t.Fatal("the scene did not focus")
	}
	f.p.newScene()
	f.h.Flush()
	if f.p.scene != nil {
		t.Fatal("the editor stayed in the old scene")
	}
	if len(f.p.project.Scenes) != 2 {
		t.Fatalf("scenes = %d", len(f.p.project.Scenes))
	}
	clip, ok := f.p.clip(f.p.selected)
	if !ok || clip.Mount == "" {
		t.Fatalf("the new scene is not selected: %q, %+v", f.p.selected, clip)
	}
}

// TestTheWordsFollowTheFileUnlessTheUserTyped keeps the sidebar's words in
// step with the file the agent writes, while a draft the user is typing
// survives the reload.
func TestTheWordsFollowTheFileUnlessTheUserTyped(t *testing.T) {
	f := start(t)
	dir := filepath.Join(f.dir, "videos", "video-1")
	f.open(t, dir)
	f.p.focusClip("scene-01-title")
	if f.p.text != "Launch film" {
		t.Fatalf("words = %q", f.p.text)
	}
	// The agent rewrites the words: the sidebar follows the file.
	if err := writeClipText(dir, "compositions/scene-01.html", "scene-01-title", "Agent title"); err != nil {
		t.Fatal(err)
	}
	f.p.reload()
	f.h.Flush()
	if f.p.text != "Agent title" {
		t.Fatalf("the sidebar kept %q", f.p.text)
	}
	// A draft the user typed survives a reload and selecting the same clip.
	f.p.text = "My title"
	f.p.selectClip("scene-01-title")
	if f.p.text != "My title" {
		t.Fatalf("selecting the clip dropped the draft: %q", f.p.text)
	}
	f.p.reload()
	f.h.Flush()
	if f.p.text != "My title" {
		t.Fatalf("the draft was lost: %q", f.p.text)
	}
	// Saving takes the draft as the file's words, so the next edit shows.
	clip, ok := f.p.clip("scene-01-title")
	if !ok {
		t.Fatal("the clip is gone")
	}
	f.p.saveText(clip)
	f.h.Flush()
	if err := writeClipText(dir, "compositions/scene-01.html", "scene-01-title", "Agent again"); err != nil {
		t.Fatal(err)
	}
	f.p.reload()
	f.h.Flush()
	if f.p.text != "Agent again" {
		t.Fatalf("after saving, the sidebar kept %q", f.p.text)
	}
}

// TestAFailedRenderShowsTheCLIsWords keeps the reason a render failed in the
// sidebar, where the user can read and copy it.
func TestAFailedRenderShowsTheCLIsWords(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	f.p.renderErr = "the HyperFrames CLI exited with exit status 1"
	f.p.renderLog = []string{"@hf-progress {\"pct\": 10}", "Error: assets/logo.png is missing"}
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if !tt.HasText("Error: assets/logo.png is missing") {
		t.Fatalf("the log is not shown; texts = %v", tt.Texts())
	}
	if err := tt.Click("Copy the render log"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	f.h.Flush()
	if !strings.Contains(f.host.Copied, "Error: assets/logo.png is missing") {
		t.Fatalf("copied %q", f.host.Copied)
	}
}

// TestLastLinesSkipsProgress keeps the tail of a failed command readable.
func TestLastLinesSkipsProgress(t *testing.T) {
	lines := []string{"first", "@hf-progress {\"pct\": 10}", "", "second", "third", "fourth"}
	got := lastLines(lines, 3)
	if strings.Join(got, "|") != "second|third|fourth" {
		t.Fatalf("lastLines = %q", got)
	}
}

// TestAClipStopsHoveringWhenThePointerLeaves clears the clip's highlight when
// the pointer goes off the timeline, and lights it again on the way back.
func TestAClipStopsHoveringWhenThePointerLeaves(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	rect, ok := tt.Find("Timeline")
	if !ok {
		t.Fatalf("timeline missing; texts = %v", tt.Texts())
	}
	at := [2]float32{rect.X + timelinePad + 60, rect.Y + rulerHeight + rowHeight/2}
	tt.Move(at[0], at[1])
	f.h.Flush()
	tt.Frame()
	if f.p.hover != "scene-01" {
		t.Fatalf("hover over the clip = %q", f.p.hover)
	}
	tt.Move(100, 400)
	f.h.Flush()
	tt.Frame()
	if f.p.hover != "" {
		t.Fatalf("the clip still hovers off the timeline: %q", f.p.hover)
	}
	tt.Move(at[0], at[1])
	f.h.Flush()
	tt.Frame()
	if f.p.hover != "scene-01" {
		t.Fatalf("hover back = %q", f.p.hover)
	}
}

// A fresh native page starts at 100%, even when the previous project's stage
// used precisely the same dimensions. The cached fit belongs to that page.
func TestPreviewFitsAfterSwitchingProjectsAtTheSameSize(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	first := f.host.WebViews[len(f.host.WebViews)-1]
	expected := first.Zoom
	if expected <= 0 || expected >= 1 {
		t.Fatalf("expected a scaled preview, got %g", expected)
	}
	dir := filepath.Join(f.dir, "videos", "video-2")
	if _, err := newProject(dir, "Another video"); err != nil {
		t.Fatal(err)
	}
	f.open(t, dir)
	settle(t, f, tt)
	second := f.host.WebViews[len(f.host.WebViews)-1]
	if second == first || !first.Closed {
		t.Fatal("project switch did not replace the native page")
	}
	if second.Zoom != expected {
		t.Fatalf("new page kept its default zoom: got %g, want %g", second.Zoom, expected)
	}
}

func TestPreviewFitIsRestoredAfterPageLoad(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	web := f.host.WebViews[len(f.host.WebViews)-1]
	expected := web.Zoom
	web.Zoom = 1 // A native navigation reset, without a change to the stage bounds.
	f.p.reload()
	f.h.Flush()
	tt.Frame()
	if web.Zoom != expected {
		t.Fatalf("page load lost its fit: got %g, want %g", web.Zoom, expected)
	}
}

func TestComponentSceneTimelineExplainsItsControls(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[1])
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	if !tt.HasText("This scene uses reusable components. Edit its fields in Vars.") {
		t.Fatal("component scene directs the user to generated HTML instead of its controls")
	}
}
