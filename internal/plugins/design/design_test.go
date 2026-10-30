package design

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

type images struct{ attached []agent.File }

func (f *images) New() kernel.Plugin { return f }
func (*images) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-images", Provides: []kernel.Named{attachment.ServiceKey}}
}
func (f *images) Setup(c *kernel.Context) error {
	return kernel.Provide[attachment.Service](c, attachment.ServiceKey, f)
}
func (f *images) Snapshot(uint64) (attachment.Image, bool) { return attachment.Image{}, false }
func (f *images) Attach(file agent.File, _ *ui.Bitmap) (uint64, error) {
	f.attached = append(f.attached, file)
	return uint64(len(f.attached)), nil
}
func (f *images) Replace(attachment.Image, agent.File, *ui.Bitmap, string, []attachment.Detail) error {
	return nil
}

type fixture struct {
	h      *kerneltest.Harness
	agent  *plugintest.Agent
	ws     *plugintest.Workspace
	p      *Plugin
	chat   *plugintest.Chat
	host   *kittest.Host
	images *images
	dir    string

	mu     sync.Mutex
	evals  []string                       // the scripts the pages ran
	answer func(code string) (any, error) // what the pages answer; nil answers nothing
}

// start opens a conversation in a folder holding files, each written an
// hour apart in the order given, the last the newest.
func start(t *testing.T, files ...string) *fixture { return startWith(t, nil, files...) }

func startWith(t *testing.T, extra []kernel.Factory, files ...string) *fixture {
	t.Helper()
	f := &fixture{chat: &plugintest.Chat{}, images: &images{}, dir: t.TempDir(), agent: plugintest.NewAgent()}
	var bitmap bytes.Buffer
	if err := png.Encode(&bitmap, image.NewNRGBA(image.Rect(0, 0, 32, 20))); err != nil {
		t.Fatal(err)
	}
	f.host = &kittest.Host{Web: func(int) *kittest.WebView {
		return &kittest.WebView{EvalFunc: f.eval, PNG: bitmap.Bytes()}
	}}
	base := time.Now().Add(-24 * time.Hour)
	for i, name := range files {
		f.write(t, name, "", base.Add(time.Duration(i)*time.Hour))
	}
	f.ws = &plugintest.Workspace{Convs: []workspace.Conversation{{ID: "s", Folder: f.dir}}}
	plugins := append([]kernel.Factory{f.agent.New, f.ws.New, f.chat.New, f.host.New, f.images.New}, extra...)
	f.h = kerneltest.New(t, append(plugins, kerneltest.Keep(&f.p, func() *Plugin { return &Plugin{} }))...)
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir})
	return f
}

func (f *fixture) eval(_ context.Context, code string) (any, error) {
	f.mu.Lock()
	f.evals = append(f.evals, code)
	answer := f.answer
	f.mu.Unlock()
	var v any
	var err error
	if answer != nil {
		v, err = answer(code)
	}
	if v == nil && err == nil && strings.Contains(code, "script#tweaks") {
		return []any{}, nil // loaded, without tweaks
	}
	if v == nil && err == nil && strings.Contains(code, "const targets =") {
		v = []any{}
	}
	if v == nil && err == nil && strings.Contains(code, "// Full-size canvas preview.") {
		v = true
	}
	if v == loading {
		return nil, nil
	}
	return v, err
}

// loading is what an answer gives for a page that has not loaded yet.
var loading = new(int)

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

func (f *fixture) write(t *testing.T, name, head string, at time.Time) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<head>"+head+"</head><h1>"+name+"</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

// open makes the conversation on show a design whose folder is the
// conversation's, on a 1000×700 canvas.
func (f *fixture) open(t *testing.T) {
	t.Helper()
	f.p.size = [2]float32{1000, 700}
	f.p.remember(design{Origin: f.agent.Status().Origin, Session: f.chat.Conv.Session, Dir: f.dir})
	f.p.update(f.chat.Current())
	f.h.Flush()
}

func (f *fixture) web(t *testing.T, name string) *kittest.WebView {
	t.Helper()
	b := f.p.boards[filepath.Join(f.dir, name)]
	if b == nil {
		t.Fatalf("%s has no page; boards %v", name, slices.Collect(mapsKeys(f.p.boards)))
	}
	return b.web.(*kittest.WebView)
}

func mapsKeys(m map[string]*board) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func (f *fixture) edited() {
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.ToolFinished, Session: "s", Tool: &agent.Tool{Kind: agent.ToolEdit}}})
	f.h.Flush()
}

func TestTheFolderListsPagesOfArtboardsWithTheirSizesAndDesignSystems(t *testing.T) {
	f := start(t)
	at := time.Now().Add(-time.Hour)
	f.write(t, "design/onboarding/Screen 10.html", `<meta name="artboard" content="390x844">`, at)
	f.write(t, "design/onboarding/Screen 2.html", `<meta content="390 x 844" name='artboard'>`, at)
	f.write(t, "design/landing/hero.html", "", at.Add(-time.Hour))
	f.write(t, "node_modules/pkg/index.html", "", time.Now())
	f.write(t, ".cache/page.html", "", time.Now())
	f.write(t, "docs/DESIGN.md", "", at)
	f.write(t, "brand/colors.tokens.json", "", at)
	l, err := list(context.Background(), f.dir, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Pages) != 2 || l.Pages[0].Dir != "design/onboarding" || l.Pages[1].Dir != "design/landing" {
		t.Fatalf("pages %+v", l.Pages)
	}
	boards := l.Pages[0].Boards
	if boards[0].name() != "Screen 2" || boards[1].name() != "Screen 10" {
		t.Fatalf("order %q, %q", boards[0].name(), boards[1].name())
	}
	if boards[0].W != 390 || boards[0].H != 844 || boards[1].X != 390+boardGap {
		t.Fatalf("layout %+v", boards)
	}
	if hero := l.Pages[1].Boards[0]; hero.W != defaultW || hero.H != defaultH {
		t.Fatalf("default size %+v", hero)
	}
	if !slices.Equal(l.Systems, []string{"brand/colors.tokens.json", "docs/DESIGN.md"}) {
		t.Fatalf("systems %q", l.Systems)
	}
}

func TestOpeningTheCanvasShowsTheNewestPageFittedAndAnotherPageReplacesIt(t *testing.T) {
	f := start(t, "design/old/a.html", "design/new/b.html", "design/new/c.html")
	f.open(t)
	if f.p.page != "design/new" || len(f.p.boards) != 2 {
		t.Fatalf("page %q, boards %d", f.p.page, len(f.p.boards))
	}
	b := f.web(t, "design/new/b.html")
	if len(b.Loaded) != 1 || b.Zoom <= 0 || b.Zoom > 1 {
		t.Fatalf("loaded %q at %v", b.Loaded, b.Zoom)
	}
	// Two 1280-wide artboards and the gap fit 1000 DIPs less the padding.
	if want := float32(1000-2*fitPad) / (2*defaultW + boardGap); abs(f.p.vp.K-want) > .001 {
		t.Fatalf("zoom %v, want %v", f.p.vp.K, want)
	}
	f.p.choosePage("design/old")
	if !b.Closed || len(f.p.boards) != 1 || f.p.page != "design/old" {
		t.Fatalf("closed %v, boards %d, page %q", b.Closed, len(f.p.boards), f.p.page)
	}
	f.write(t, "design/new/d.html", "", time.Now())
	f.edited()
	if f.p.page != "design/old" {
		t.Fatalf("a newer page replaced the one the user chose: %q", f.p.page)
	}
}

func TestTheAgentsChangesReloadAnArtboardOrShowANewerPage(t *testing.T) {
	f := start(t, "design/a.html")
	f.open(t)
	a := f.web(t, "design/a.html")
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.ToolFinished, Session: "s", Tool: &agent.Tool{Kind: agent.ToolRead}}})
	f.h.Flush()
	f.edited()
	if a.Reloads != 0 {
		t.Fatalf("an unchanged artboard reloaded: %d", a.Reloads)
	}
	f.write(t, "design/a.html", "", time.Now().Add(-time.Minute))
	f.edited()
	if a.Reloads != 1 {
		t.Fatalf("a changed artboard did not reload: %d", a.Reloads)
	}
	f.write(t, "design/next/b.html", "", time.Now())
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s"}})
	f.h.Flush()
	if f.p.page != "design/next" || !a.Closed {
		t.Fatalf("page %q, old page closed %v", f.p.page, a.Closed)
	}
	kernel.Emit(f.h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "other"}})
	f.h.Flush()
	if f.p.scanning.Running() {
		t.Fatal("another conversation's turn listed the folder")
	}
}

func TestZoomKeepsThePointUnderThePointerAndStepsAndSpreadsToThePages(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.vp = viewport{K: .5, X: 100, Y: 50}
	f.p.zoomAt(1, 300, 250) // page point (400, 400)
	if f.p.vp.X != -100 || f.p.vp.Y != -150 || f.p.autoFit {
		t.Fatalf("viewport %+v", f.p.vp)
	}
	if z := f.web(t, "a.html").Zoom; z != 1 {
		t.Fatalf("page zoom %v", z)
	}
	f.p.zoomStep(true)
	if f.p.vp.K != 1.5 {
		t.Fatalf("zoom in from 100%% went to %v", f.p.vp.K)
	}
	f.p.zoomStep(false)
	f.p.zoomStep(false)
	if f.p.vp.K != .75 {
		t.Fatalf("zoom out twice went to %v", f.p.vp.K)
	}
	f.p.input(ui.InputEvent{Kind: ui.InputScroll, DX: 10, DY: 20})
	if f.p.vp.X != 500-.75*400-10 && f.p.vp.Y == 0 {
		t.Fatalf("scroll did not pan: %+v", f.p.vp)
	}
	f.p.setTool(handTool)
	x, y := f.p.vp.X, f.p.vp.Y
	f.p.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 10, Y: 10})
	f.p.input(ui.InputEvent{Kind: ui.InputPointerMove, X: 40, Y: 30})
	f.p.input(ui.InputEvent{Kind: ui.InputPointerUp, X: 40, Y: 30})
	if f.p.vp.X != x+30 || f.p.vp.Y != y+20 {
		t.Fatalf("the hand did not pan: %+v from %v, %v", f.p.vp, x, y)
	}
	f.p.fit()
	if !f.p.autoFit {
		t.Fatal("fitting did not fit again")
	}
}

func TestACommentNamesTheElementUnderTheClickAndGoesToTheComposerWithNotes(t *testing.T) {
	f := start(t, "design/home.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, "elementFromPoint") {
			return map[string]any{"selector": "#hero > h1", "text": "Ship calmer", "html": "<h1>Ship calmer</h1>"}, nil
		}
		return nil, nil
	}
	f.open(t)
	f.p.vp = viewport{K: .5, X: 100, Y: 100}
	f.p.setTool(commentTool)
	f.p.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 150, Y: 120})
	f.h.Flush()
	if f.ran("const x = 100, y = 40;") != 1 {
		t.Fatalf("the page was not asked about (100, 40): %q", f.evals)
	}
	if f.p.editing == 0 || f.p.mark(f.p.editing).Selector != "#hero > h1" {
		t.Fatalf("no comment open: %+v", f.p.marks)
	}
	f.p.text = "Make it bigger"
	f.p.saveCard()
	f.p.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 10, Y: 10}) // off the artboard: a note
	f.p.text = "Try a dark variant"
	f.p.saveCard()
	f.p.setTool(noteTool)
	f.p.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 20, Y: 20})
	f.p.closeCard() // a note left empty goes
	if len(f.p.marks) != 2 {
		t.Fatalf("marks %+v", f.p.marks)
	}
	f.chat.SetDraft("Two things.")
	f.p.sendMarks()
	want := "Two things.\n\nComments on the design:\n\n1. In design/home.html, `#hero > h1` (“Ship calmer”): Make it bigger\n2. Note: Try a dark variant\n\n"
	if f.chat.Draft != want || len(f.p.pending()) != 0 || len(f.p.marks) != 2 {
		t.Fatalf("draft %q, marks %d", f.chat.Draft, len(f.p.marks))
	}
}

func TestTweaksAreReadAppliedResetAndAskedToBeKept(t *testing.T) {
	f := start(t, "design/a.html")
	f.answer = func(code string) (any, error) {
		if strings.Contains(code, `script#tweaks`) {
			return []any{
				map[string]any{"var": "--radius", "label": "Corners", "type": "range", "min": 0.0, "max": 32.0, "step": 2.0, "unit": "px", "value": "8px"},
				map[string]any{"var": "--accent", "label": "Accent", "type": "color", "value": "#d9622b"},
				map[string]any{"var": "--compact", "label": "Compact", "type": "toggle", "on": "0.8", "off": "1", "value": "1"},
			}, nil
		}
		return nil, nil
	}
	f.open(t)
	path := filepath.Join(f.dir, "design/a.html")
	b := f.p.boards[path]
	if len(b.tweaks) != 3 || b.tweaks[0].num != 8 || b.tweaks[1].color != ui.RGB(0xd9, 0x62, 0x2b) || b.tweaks[2].on {
		t.Fatalf("tweaks %+v", b.tweaks)
	}
	b.tweaks[0].num = 12
	b.tweaks[0].changed()
	b.tweaks[2].on = true
	b.tweaks[2].changed()
	f.p.applyTweaks(path)
	f.h.Flush()
	if f.ran(`s.setProperty("--radius", "12px");`) != 1 || f.ran(`s.setProperty("--compact", "0.8");`) != 1 {
		t.Fatalf("evals %q", f.evals)
	}
	f.p.keepTweaks(path)
	if want := "In design/a.html, make these the values:\n- --radius: 12px\n- --compact: 0.8\n\n"; f.chat.Draft != want {
		t.Fatalf("draft %q", f.chat.Draft)
	}
	f.p.resetTweaks(path)
	if b.tweaked() || b.tweaks[0].num != 8 {
		t.Fatalf("reset left %+v", b.tweaks)
	}
}

func TestTweaksWaitForThePageToLoad(t *testing.T) {
	f := start(t)
	f.write(t, "a.html", "", time.Now())
	asked := 0
	f.answer = func(code string) (any, error) {
		if !strings.Contains(code, `const file = "a.html";`) {
			return nil, nil
		}
		if asked++; asked < 3 {
			return loading, nil
		}
		return []any{map[string]any{"var": "--gap", "type": "range", "value": "4px", "unit": "px"}}, nil
	}
	f.open(t)
	if b := f.p.boards[filepath.Join(f.dir, "a.html")]; len(b.tweaks) != 1 || b.tweaks[0].num != 4 || asked != 3 {
		t.Fatalf("tweaks %+v after %d asks", b.tweaks, asked)
	}
}

func TestTheBriefGoesWithADesignsFirstMessageAndAgainForAnotherDesignSystem(t *testing.T) {
	f := start(t, "DESIGN.md")
	draft := chat.Draft{Session: "s", Directory: f.dir, Text: "A landing page"}
	if d, _ := f.send(draft); len(d.Files) != 0 {
		t.Fatal("the brief went with a conversation that is not a design")
	}
	f.p.newDesign()
	f.h.Flush()
	d, err := f.send(chat.Draft{DraftID: f.chat.Conv.DraftID, Directory: f.dir, Text: "A landing page"})
	if err != nil || len(d.Files) != 1 || d.Files[0].Name != briefName || d.Files[0].MIME != "text/markdown" {
		t.Fatalf("files %+v, %v", d.Files, err)
	}
	text := string(d.Files[0].Data)
	for _, s := range []string{"`designs/design-1/`", `<meta name="artboard"`, `id="tweaks"`} {
		if !strings.Contains(text, s) {
			t.Fatalf("no %q in the brief:\n%s", s, text)
		}
	}
	// The chat creates the conversation while the user stays beside it.
	f.chat.Conv.Session = "new"
	kernel.Emit(f.h, chat.Changed, f.chat.Conv)
	kernel.Emit(f.h, chat.Started, f.chat.Conv)
	if i := f.p.designIndex("new"); !f.p.open || i < 0 || f.p.designs[i].Dir != filepath.Join(f.dir, "designs", "design-1") {
		t.Fatalf("the new conversation is no design: open %v, designs %+v", f.p.open, f.p.designs)
	}
	next := chat.Draft{Session: "new", Directory: f.dir}
	if d, _ := f.send(next); len(d.Files) != 0 {
		t.Fatal("the brief went twice")
	}
	f.p.chooseSystem("DESIGN.md")
	if d, _ := f.send(next); len(d.Files) != 1 || !strings.Contains(string(d.Files[0].Data), "design system in `DESIGN.md`") {
		t.Fatalf("after choosing a design system: %+v", d.Files)
	}
	if d, _ := f.send(next); len(d.Files) != 0 {
		t.Fatal("the brief went again for the same design system")
	}
}

func TestTheBriefGoesAgainWhenItsMessageWasNotSent(t *testing.T) {
	f := start(t, "DESIGN.md")
	f.p.newDesign()
	f.h.Flush()
	first := chat.Draft{DraftID: f.chat.Conv.DraftID, Directory: f.dir, Text: "A landing page"}
	if _, _, err := f.p.outgoing(first); err != nil { // and a later transform rejects it
		t.Fatal(err)
	}
	if len(f.p.starts) != 0 {
		t.Fatalf("an unsent message started a design: %+v", f.p.starts)
	}
	if _, err := f.send(first); err != nil {
		t.Fatal(err)
	}
	f.chat.Conv.Session = "new"
	kernel.Emit(f.h, chat.Changed, f.chat.Conv)
	kernel.Emit(f.h, chat.Started, f.chat.Conv)
	f.p.chooseSystem("DESIGN.md")
	next := chat.Draft{Session: "new", Directory: f.dir}
	if _, _, err := f.p.outgoing(next); err != nil { // rejected again
		t.Fatal(err)
	}
	if d, _ := f.send(next); len(d.Files) != 1 || !strings.Contains(string(d.Files[0].Data), "design system in `DESIGN.md`") {
		t.Fatalf("the brief did not go again: %+v", d.Files)
	}
}

func TestANewDesignLeftWhileStartingBecomesADesign(t *testing.T) {
	f := start(t)
	f.p.newDesign()
	f.h.Flush()
	starting := f.chat.Conv
	f.send(chat.Draft{DraftID: starting.DraftID, Directory: f.dir, Text: "A deck"})
	f.chat.Conv.Starting = true
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir}) // away, while it starts
	if f.p.open {
		t.Fatal("the canvas shows beside a conversation that is no design")
	}
	kernel.Emit(f.h, chat.Started, chat.Conversation{DraftID: starting.DraftID, Session: "later", Directory: f.dir})
	if i := f.p.designIndex("later"); i < 0 || f.p.designs[i].Dir != filepath.Join(f.dir, "designs", "design-1") {
		t.Fatalf("designs %+v", f.p.designs)
	}
	// A new chat that was never sent leaves nothing to become a design.
	f.p.newDesign()
	f.h.Flush()
	f.chat.NewChat(f.dir)
	kernel.Emit(f.h, chat.Started, chat.Conversation{Session: "plain", Directory: f.dir})
	if f.p.designIndex("plain") >= 0 || f.p.open {
		t.Fatalf("a plain chat became a design: %+v", f.p.designs)
	}
}

func TestPresentingShowsAnArtboardAt100PercentAndStepsThroughThePage(t *testing.T) {
	f := start(t, "design/a.html", "design/b.html")
	f.open(t)
	a, b := f.web(t, "design/a.html"), f.web(t, "design/b.html")
	before := f.p.vp
	f.p.present(filepath.Join(f.dir, "design/a.html"))
	if a.Zoom != 1 {
		t.Fatalf("presented at %v", a.Zoom)
	}
	tt := ui.NewTester(f.p.presentView, 1200, 800)
	tt.Frame()
	if !tt.HasText("1 of 2") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Next artboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b.Zoom != 1 || a.Zoom != float64(max(1, before.zoom())) || !tt.HasText("2 of 2") {
		t.Fatalf("zooms %v, %v; texts %q", a.Zoom, b.Zoom, tt.Texts())
	}
	if err := tt.Click("Stop presenting  Esc"); err != nil {
		t.Fatal(err)
	}
	if f.p.presenting != "" || b.Zoom != float64(max(1, before.zoom())) || f.p.vp != before {
		t.Fatalf("presenting %q at %v", f.p.presenting, b.Zoom)
	}
}

func TestANewArtboardIsWrittenOnThePageAndAskedFor(t *testing.T) {
	f := start(t)
	f.open(t)
	f.p.newArtboard()
	f.h.Flush()
	path := filepath.Join(f.dir, "artboard-1.html")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `<meta name="artboard" content="1280x800">`) {
		t.Fatalf("%v: %s", err, data)
	}
	if f.chat.Draft != "Design artboard-1.html: \n\n" || f.p.page != "." || f.p.boards[path] == nil {
		t.Fatalf("draft %q, page %q", f.chat.Draft, f.p.page)
	}
	f.p.newArtboard()
	f.h.Flush()
	if _, err := os.Stat(filepath.Join(f.dir, "artboard-2.html")); err != nil {
		t.Fatal(err)
	}
}

func TestASnapshotGoesWithTheNextMessage(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	f.web(t, "a.html").PNG = b.Bytes()
	path := filepath.Join(f.dir, "a.html")
	f.p.snapshot(path)
	f.h.Flush()
	f.p.snapshot(path)
	f.h.Flush()
	if len(f.images.attached) != 2 || f.images.attached[0].Name != "Design.png" || f.images.attached[1].Name != "Design 2.png" {
		t.Fatalf("attached %+v", f.images.attached)
	}
}

func TestTheCanvasDrawsItsToolsAndArtboards(t *testing.T) {
	f := start(t, "design/a.html", "design/b.html")
	f.open(t)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 800)
	tt.Frame()
	f.h.Flush() // the fit the first frame posts
	tt.Frame()
	for _, s := range []string{"Design canvas", "Canvas", "Select  V", "Hand  H", "Comment  C", "Note  N", "New artboard",
		"Present", "Zoom to fit  ⇧1", "Select a", "Select b", "Web page"} {
		if _, ok := tt.Find(s); !ok {
			t.Fatalf("no %q", s)
		}
	}
	if !tt.HasText("design") {
		t.Fatalf("no page name in %q", tt.Texts())
	}
	if err := tt.Click("Comment  C"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.tool != commentTool {
		t.Fatal("the comment tool was not chosen")
	}
	if err := tt.Click("Select b"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.p.selected != filepath.Join(f.dir, "design/b.html") {
		t.Fatalf("selected %q", f.p.selected)
	}
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	tt.Frame()
	if _, ok := tt.Find("Design canvas"); ok {
		t.Fatal("the canvas shows beside a conversation that is no design")
	}
}

func TestAnEmptyFolderSuggestsDesigns(t *testing.T) {
	f := start(t, "README.md")
	f.open(t)
	tt := ui.NewTester(f.p.drawer, 1000, 800)
	tt.Frame()
	if !tt.HasText("Describe an idea and turn it into a design") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Landing page"); err != nil {
		t.Fatal(err)
	}
	if f.chat.Draft != "Design a landing page for \n\n" {
		t.Fatalf("draft %q", f.chat.Draft)
	}
}

func TestTheTitleStripButtonStartsANewDesignAndOnlyDesignsShowTheCanvas(t *testing.T) {
	f := start(t, "a.html")
	tt := ui.NewTester(f.p.titleButton, 200, 40)
	tt.Frame()
	if f.p.open {
		t.Fatal("the canvas shows beside a conversation that is no design")
	}
	if err := tt.Click("New design"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	fresh := filepath.Join(f.dir, "designs", "design-1")
	if f.chat.Conv.Session != "" || f.chat.Conv.Directory != f.dir || !f.p.open || f.p.folder != fresh || len(f.p.boards) != 0 {
		t.Fatalf("conversation %+v, open %v, folder %q, boards %d", f.chat.Conv, f.p.open, f.p.folder, len(f.p.boards))
	}
	if info, err := os.Stat(fresh); err != nil || !info.IsDir() {
		t.Fatalf("the design's folder: %v", err)
	}
	f.chat.NewChat(f.dir) // ⌘N: a plain chat
	if f.p.open || len(f.p.boards) != 0 {
		t.Fatal("a plain new chat shows the canvas")
	}
	f.host.ShowPage("settings")
	tt.Frame()
	if _, ok := tt.Find("New design"); ok {
		t.Fatal("the button shows beside another page")
	}
}

func TestADesignShowsItsCanvasAgainWithItsCommentsWhenItIsOpened(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(10, 10)
	f.p.text = "Darker"
	f.p.saveCard()
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	if f.p.open || len(f.p.marks) != 0 {
		t.Fatalf("open %v, marks %d", f.p.open, len(f.p.marks))
	}
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir})
	f.h.Flush()
	if !f.p.open || len(f.p.marks) != 1 || f.p.marks[0].Words != "Darker" || len(f.p.boards) != 1 {
		t.Fatalf("open %v, marks %+v", f.p.open, f.p.marks)
	}
}

func TestADesignWhoseConversationIsGoneIsForgotten(t *testing.T) {
	f := start(t)
	f.open(t)
	f.ws.Convs = nil
	kernel.Emit(f.h, workspace.Changed, struct{}{})
	if len(f.p.designs) != 0 {
		t.Fatalf("designs %+v", f.p.designs)
	}
}

func TestANewDesignWithoutAFolderAsksForOne(t *testing.T) {
	f := start(t)
	f.chat.NewChat("")
	f.ws.Chosen = f.dir
	f.p.newDesign()
	f.h.Flush()
	if f.chat.Conv.Directory != f.dir || !f.p.open {
		t.Fatalf("conversation %+v, open %v", f.chat.Conv, f.p.open)
	}
}

func TestADesignInAnotherFolderListsItsOwnArtboards(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	a := f.web(t, "a.html")
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "b.html"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.p.remember(design{Origin: f.agent.Status().Origin, Session: "t", Dir: other})
	f.chat.Open(agent.Session{ID: "t", Directory: other})
	f.h.Flush()
	if !a.Closed || f.p.boards[filepath.Join(other, "b.html")] == nil {
		t.Fatalf("closed %v, boards %v", a.Closed, f.p.boards)
	}
}

func TestWithoutWebPagesTheCanvasCloses(t *testing.T) {
	f := start(t, "a.html")
	f.host.Web = nil
	f.open(t)
	if f.p.open || len(f.host.Toasts) != 1 {
		t.Fatalf("open %v, toasts %q", f.p.open, f.host.Toasts)
	}
}

func TestDisablingThePluginClosesItsPages(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	a := f.web(t, "a.html")
	if err := f.h.Disable("design"); err != nil {
		t.Fatal(err)
	}
	if !a.Closed {
		t.Fatal("the page outlived the plugin")
	}
}

func abs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestKeysChooseToolsAndZoom(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	for key, want := range map[ui.Key]tool{ui.KeyH: handTool, ui.KeyC: commentTool, ui.KeyN: noteTool, ui.KeyV: selectTool} {
		if !f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: key}) || f.p.tool != want {
			t.Fatalf("key %v chose %v", key, f.p.tool)
		}
	}
	if f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Cmd}) {
		t.Fatal("⌘C was taken from the edit menu")
	}
	f.p.zoomTo(1)
	f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEqual})
	if f.p.vp.K != 1.5 {
		t.Fatalf("= zoomed to %v", f.p.vp.K)
	}
	f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key1, Mods: ui.Shift})
	if !f.p.autoFit {
		t.Fatal("⇧1 did not fit")
	}
	f.p.setTool(handTool)
	f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape})
	if f.p.tool != selectTool {
		t.Fatal("Escape kept the hand")
	}
	if f.p.key(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape}) {
		t.Fatal("Escape was taken with nothing to leave")
	}
}

func TestTheOpenCanvasHidesTheWorkPanel(t *testing.T) {
	f := start(t, "a.html")
	hidden := func() bool {
		for _, e := range kernel.Items(f.h, chat.HideWork) {
			if e.Value() {
				return true
			}
		}
		return false
	}
	if hidden() {
		t.Fatal("the closed canvas hides the work panel")
	}
	f.open(t)
	if !hidden() {
		t.Fatal("the open canvas leaves the work panel")
	}
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	if hidden() {
		t.Fatal("the closed canvas still hides the work panel")
	}
}

func TestADesignStaysADesignWhenThePluginStartsAgain(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	if err := f.h.Disable("design"); err != nil {
		t.Fatal(err)
	}
	if err := f.h.Enable("design"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if !f.p.open || f.p.designIndex("s") < 0 {
		t.Fatalf("open %v, designs %+v", f.p.open, f.p.designs)
	}
}

func TestEachNewDesignStartsEmptyInAFolderOfItsOwn(t *testing.T) {
	f := start(t, "index.html", "designs/design-1/old.html")
	f.p.newDesign()
	f.h.Flush()
	if want := filepath.Join(f.dir, "designs", "design-2"); f.p.folder != want || len(f.p.found.Pages) != 0 {
		t.Fatalf("folder %q, pages %+v", f.p.folder, f.p.found.Pages)
	}
	tt := ui.NewTester(f.p.drawer, 1000, 800)
	tt.Frame()
	if !tt.HasText("Describe an idea and turn it into a design") || !tt.HasText("designs/design-2") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// What the agent writes there shows; the folder's other pages don't.
	f.write(t, "designs/design-2/home.html", "", time.Now())
	f.p.scan()
	f.h.Flush()
	if f.p.boards[filepath.Join(f.dir, "designs/design-2/home.html")] == nil || len(f.p.boards) != 1 {
		t.Fatalf("boards %v", f.p.boards)
	}
}

// fakeTabs is the tabs service with one tab on show.
type fakeTabs struct{ active tabs.Tab }

func (f *fakeTabs) New() kernel.Plugin { return f }
func (*fakeTabs) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-tabs", Provides: []kernel.Named{tabs.ServiceKey}}
}
func (f *fakeTabs) Setup(c *kernel.Context) error {
	return kernel.Provide[tabs.Service](c, tabs.ServiceKey, f)
}
func (f *fakeTabs) Tabs() []tabs.Tab         { return []tabs.Tab{f.active} }
func (f *fakeTabs) Active() (tabs.Tab, bool) { return f.active, f.active.ID != "" }
func (f *fakeTabs) Select(string)            {}
func (f *fakeTabs) Close(string)             {}
func (f *fakeTabs) Move(string, int)         {}
func (f *fakeTabs) Space() string            { return f.active.Folder }

func TestDesignTabsShowAsDesigns(t *testing.T) {
	tb := &fakeTabs{}
	f := startWith(t, []kernel.Factory{tb.New}, "a.html")
	looks := kernel.Items(f.h, tabs.Looks)
	if len(looks) != 1 || looks[0].Value.Draft != "New design" || looks[0].Value.Name != "Design" {
		t.Fatalf("looks %+v", looks)
	}
	applies := looks[0].Value.Applies
	draft := tabs.Tab{ID: "d1", Kind: tabs.Draft, Folder: f.dir}
	if applies(tabs.Tab{Kind: tabs.Conversation, Session: "s"}) || applies(draft) {
		t.Fatal("a plain conversation or new chat shows as a design")
	}
	f.p.newDesign()
	f.h.Flush()
	tb.active = draft
	if !applies(draft) || applies(tabs.Tab{ID: "d2", Kind: tabs.Draft, Folder: f.dir}) {
		t.Fatal("only the new design on show is one")
	}
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir})
	f.open(t)
	if !applies(tabs.Tab{Kind: tabs.Conversation, Session: "s"}) || applies(tabs.Tab{Kind: tabs.Page, Page: "settings"}) {
		t.Fatal("a design conversation's tab is no design")
	}
}

// send runs the design's outgoing transform as the chat does, committing what
// it accepts.
func (f *fixture) send(d chat.Draft) (chat.Draft, error) {
	out, commit, err := f.p.outgoing(d)
	if err == nil && commit != nil {
		commit()
	}
	return out, err
}
