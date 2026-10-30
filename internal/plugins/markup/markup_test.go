package markup

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"github.com/xleotranx/wada-kit/native"
)

const shotW, shotH = 400, 300

type images struct {
	current  attachment.Image
	details  []attachment.Detail
	replaced int
}

func (f *images) New() kernel.Plugin { return f }
func (*images) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-images", Provides: []kernel.Named{attachment.ServiceKey}}
}
func (f *images) Setup(c *kernel.Context) error {
	return kernel.Provide[attachment.Service](c, attachment.ServiceKey, f)
}
func (f *images) Snapshot(id uint64) (attachment.Image, bool) {
	return f.current, id == f.current.ID && id != 0
}
func (f *images) Attach(file agent.File, _ *ui.Bitmap) (uint64, error) {
	f.current = attachment.Image{ID: f.current.ID + 1, Revision: 1, File: file}
	return f.current.ID, nil
}
func (f *images) Replace(source attachment.Image, file agent.File, _ *ui.Bitmap, notes string, details []attachment.Detail) error {
	file.Data = bytes.Clone(file.Data)
	f.current.File, f.current.Notes, f.details = file, notes, details
	f.current.Revision++
	f.replaced++
	return nil
}

// screenshot is a light gray image with a gradient, so a changed pixel
// anywhere shows.
func screenshot(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, shotW, shotH))
	for y := range shotH {
		for x := range shotW {
			img.SetNRGBA(x, y, color.NRGBA{R: 200 + uint8(x%50), G: 210, B: 220 + uint8(y%30), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// sizes is a backend that sends images at half their size.
type sizes struct{ model agent.ModelRef }

func (f *sizes) New() kernel.Plugin { return f }
func (*sizes) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-sizes", Provides: []kernel.Named{agent.ImagesKey}}
}
func (f *sizes) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Images](c, agent.ImagesKey, f)
}
func (f *sizes) Fit(_ context.Context, model agent.ModelRef, file agent.File) (int, int, error) {
	f.model = model
	config, _, err := image.DecodeConfig(bytes.NewReader(file.Data))
	return config.Width / 2, config.Height / 2, err
}

func start(t *testing.T, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *images, []byte, *plugintest.Chat) {
	t.Helper()
	ctl, host := &plugintest.Chat{}, &kittest.Host{}
	data := screenshot(t)
	files := &images{current: attachment.Image{ID: 1, Revision: 1, File: agent.File{Name: "screen.jpg", MIME: "image/png", Data: data}}}
	var p *Plugin
	plugins := append([]kernel.Factory{files.New, ctl.New, host.New}, extra...)
	h := kerneltest.New(t, append(plugins, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))...)
	return h, p, files, data, ctl
}

// editor opens the editor on the image and returns its tester and a way to
// find an image position on the window.
func editor(t *testing.T, h *kerneltest.Harness, p *Plugin) (*ui.Tester, func(x, y float32) (float32, float32)) {
	t.Helper()
	p.begin(1)
	h.Flush()
	tt := ui.NewTester(p.editor, 1400, 900)
	tt.Frame()
	tt.Advance(time.Second)
	r, ok := tt.Find("Screenshot canvas")
	if !ok {
		t.Fatal("canvas is missing")
	}
	return tt, func(x, y float32) (float32, float32) {
		v := p.viewport(r.W, r.H, shotW, shotH)
		cx, cy := v.canvas(point{x, y})
		return r.X + cx, r.Y + cy
	}
}

func dragOn(tt *ui.Tester, at func(x, y float32) (float32, float32), from, to point) {
	x0, y0 := at(from.X, from.Y)
	x1, y1 := at(to.X, to.Y)
	tt.Press(x0, y0)
	tt.Move((x0+x1)/2, (y0+y1)/2)
	tt.Move(x1, y1)
	tt.Release(x1, y1)
	tt.Frame()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < .01 }

func TestAMarkedUpImageGoesWithNumberedNotesAndKeepsItsOtherPixels(t *testing.T) {
	h, p, files, original, _ := start(t)
	tt, at := editor(t, h, p)
	dragOn(tt, at, point{.25, .25}, point{.75, .75})
	if len(p.marks) != 1 || p.marks[0].Kind != box || !near(p.marks[0].A.X, .25) || !near(p.marks[0].B.Y, .75) {
		t.Fatalf("marks %+v", p.marks)
	}
	if !tt.Focused("Note for mark 1") {
		t.Fatal("drawing a mark did not focus its note")
	}
	tt.Type("Move this panel")
	tt.Frame()
	if err := tt.Click("Use marked-up image"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if p.open || files.replaced != 1 || files.current.File.MIME != "image/png" || files.current.File.Name != "screen.png" {
		t.Fatalf("not replaced: open %v file %+v", p.open, files.current.File.Name)
	}
	want := "I drew the numbered marks; they are not part of the image. Positions are x, y from 0 to 1000 across and down.\n" +
		"1. Red box from (250, 250) to (750, 750): Move this panel"
	if files.current.Notes != want {
		t.Fatalf("notes %q", files.current.Notes)
	}
	got, source := decodePNG(t, files.current.File.Data), decodePNG(t, original)
	edge := color.NRGBAModel.Convert(got.At(200, 75)).(color.NRGBA)
	if edge.R < 200 || edge.G > 90 {
		t.Fatalf("the box's edge is not red: %+v", edge)
	}
	for _, xy := range [][2]int{{10, 10}, {200, 150}, {390, 290}, {150, 120}} {
		if a, b := color.NRGBAModel.Convert(got.At(xy[0], xy[1])), color.NRGBAModel.Convert(source.At(xy[0], xy[1])); a != b {
			t.Fatalf("pixel %v changed from %+v to %+v", xy, b, a)
		}
	}
}

func TestMarkingUpAgainEditsTheOriginalAndRemovingEveryMarkRestoresIt(t *testing.T) {
	h, p, files, original, _ := start(t)
	tt, at := editor(t, h, p)
	dragOn(tt, at, point{.1, .1}, point{.5, .5})
	tt.Type("Header")
	tt.Frame()
	tt.Click("Use marked-up image")
	h.Flush()
	tt, _ = editor(t, h, p)
	if len(p.marks) != 1 || *p.note(p.marks[0].ID) != "Header" || !bytes.Equal(p.original.Data, original) {
		t.Fatalf("reopening lost the marks: %+v", p.marks)
	}
	if err := tt.Click("Remove mark 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Use original image"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if !bytes.Equal(files.current.File.Data, original) || files.current.Notes != "" || files.current.File.Name != "screen.jpg" {
		t.Fatalf("the original did not come back: notes %q", files.current.Notes)
	}
	p.begin(1)
	h.Flush()
	if len(p.marks) != 0 {
		t.Fatal("the restored image kept its marks")
	}
}

func TestSelectMovesResizesAndDeletesMarksWithUndoAndRedo(t *testing.T) {
	h, p, _, _, _ := start(t)
	tt, at := editor(t, h, p)
	dragOn(tt, at, point{.2, .2}, point{.4, .4})
	tt.Click("Select")
	// Dragging the box's right edge moves it.
	dragOn(tt, at, point{.4, .3}, point{.5, .4})
	if m := p.marks[0]; !near(m.A.X, .3) || !near(m.A.Y, .3) || !near(m.B.X, .5) {
		t.Fatalf("moved %+v", m)
	}
	// Its bottom-right corner resizes it.
	dragOn(tt, at, point{.5, .5}, point{.9, .8})
	if m := p.marks[0]; !near(m.A.X, .3) || !near(m.B.X, .9) || !near(m.B.Y, .8) {
		t.Fatalf("resized %+v", m)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if m := p.marks[0]; !near(m.B.X, .5) {
		t.Fatalf("undo left %+v", m)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if m := p.marks[0]; !near(m.B.X, .9) {
		t.Fatalf("redo left %+v", m)
	}
	tt.Key(0, ui.Key4)
	if p.marks[0].Ink != 3 || p.ink != 3 {
		t.Fatal("4 did not recolor the selected mark green")
	}
	tt.Key(0, ui.KeyBackspace)
	if len(p.marks) != 0 {
		t.Fatal("Backspace left the selected mark")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if len(p.marks) != 1 {
		t.Fatal("undo did not bring the mark back")
	}
	tt.Key(0, ui.KeyA)
	dragOn(tt, at, point{.1, .9}, point{.6, .6})
	if len(p.marks) != 2 || p.marks[1].Kind != arrow || p.marks[1].Ink != 3 {
		t.Fatalf("arrow %+v", p.marks)
	}
}

func TestZoomKeepsThePointUnderThePointerAndFitResets(t *testing.T) {
	h, p, _, _, _ := start(t)
	tt, at := editor(t, h, p)
	r, _ := tt.Find("Screenshot canvas")
	x, y := at(.3, .6)
	x, y = x-r.X, y-r.Y
	v := p.viewport(r.W, r.H, shotW, shotH)
	// The Tester's scroll carries no modifiers, so ⌘-scroll goes to the
	// canvas's handler as the window gives it.
	if !p.input(ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Cmd, X: x, Y: y, DY: -250}, v, 0) || p.zoom <= v.K {
		t.Fatalf("⌘-scroll did not zoom in: %v", p.zoom)
	}
	v = p.viewport(r.W, r.H, shotW, shotH)
	if f := v.image(x, y); !near(f.X, .3) || !near(f.Y, .6) {
		t.Fatalf("zoom moved the point under the pointer to %+v", f)
	}
	before := p.scrollX
	p.input(ui.InputEvent{Kind: ui.InputScroll, X: x, Y: y, DX: 40}, v, 0)
	if p.scrollX <= before {
		t.Fatal("scrolling did not pan the zoomed image")
	}
	tt.Frame()
	if err := tt.Click("Fit"); err != nil {
		t.Fatal(err)
	}
	if p.zoom != 0 {
		t.Fatal("Fit kept the zoom")
	}
}

func TestPinchZoomsAtThePointer(t *testing.T) {
	h, p, _, _, _ := start(t)
	tt, at := editor(t, h, p)
	r, _ := tt.Find("Screenshot canvas")
	x, y := at(.3, .6)
	x, y = x-r.X, y-r.Y
	v := p.viewport(r.W, r.H, shotW, shotH)
	// The Tester's pinch carries the pointer, as the window gives it over
	// the canvas.
	if !p.input(ui.InputEvent{Kind: ui.InputZoom, X: x, Y: y, Scale: 5}, v, 0) {
		t.Fatal("the canvas did not take the pinch")
	}
	if !near(p.zoom, v.K*5) {
		t.Fatalf("pinch zoomed to %v, want %v", p.zoom, v.K*5)
	}
	v = p.viewport(r.W, r.H, shotW, shotH)
	if f := v.image(x, y); !near(f.X, .3) || !near(f.Y, .6) {
		t.Fatalf("pinch moved the point under the pointer to %+v", f)
	}
}

func TestAZoomedImageStaysInsideTheCanvas(t *testing.T) {
	h, p, _, _, _ := start(t)
	tt, _ := editor(t, h, p)
	r, _ := tt.Find("Screenshot canvas")
	outside := [][2]int{{int(r.X) + 20, int(r.Y) - 6}, {int(r.X + r.W + 6), int(r.Y) + 40}, {int(r.X) + 40, int(r.Y + r.H + 6)}}
	before := make([]color.RGBA, len(outside))
	for i, xy := range outside {
		before[i] = tt.Image().RGBAAt(xy[0], xy[1])
	}
	v := p.viewport(r.W, r.H, shotW, shotH)
	p.zoomAt(v, 8, r.W/2, r.H/2)
	tt.Frame()
	for i, xy := range outside {
		if got := tt.Image().RGBAAt(xy[0], xy[1]); got != before[i] {
			t.Fatalf("the zoomed image painted %v beside the canvas: %+v, was %+v", xy, got, before[i])
		}
	}
}

func TestCancelAndNavigationDropAnExport(t *testing.T) {
	h, p, files, _, ctl := start(t)
	tt, at := editor(t, h, p)
	dragOn(tt, at, point{.1, .1}, point{.8, .8})
	p.apply(native.PaletteOf(&ui.Theme{}))
	ctl.NewChat("/another-project")
	h.Flush()
	if p.open || files.replaced != 0 {
		t.Fatal("a stale export followed navigation")
	}
	tt, _ = editor(t, h, p)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	h.Flush()
	if p.open || files.replaced != 0 || p.decoded != nil || p.preview != nil || p.scope != nil {
		t.Fatal("Escape kept the editor or changed the attachment")
	}
}

func TestPinsLinesAndHighlightsAreNumberedAndHiddenAreasBlackOutPixels(t *testing.T) {
	h, p, files, original, _ := start(t)
	tt, at := editor(t, h, p)
	pal := native.PaletteOf(&ui.Theme{})
	// A hidden area first: the numbers skip it.
	tt.Key(0, ui.KeyR)
	dragOn(tt, at, point{.6, .1}, point{.9, .3})
	p.focus = canvasFocus
	tt.Frame()
	tt.Key(0, ui.KeyP)
	x, y := at(.2, .8)
	tt.Press(x, y)
	tt.Release(x, y)
	tt.Frame()
	tt.Type("This icon is blurry")
	p.focus = canvasFocus
	tt.Frame()
	tt.Key(0, ui.KeyD)
	x0, y0 := at(.1, .4)
	tt.Press(x0, y0)
	for i := 1; i <= 10; i++ {
		x, y := at(.1+float32(i)*.03, .4+float32(i%2)*.05)
		tt.Move(x, y)
	}
	x1, y1 := at(.4, .4)
	tt.Release(x1, y1)
	tt.Frame()
	p.focus = canvasFocus
	tt.Frame()
	tt.Key(0, ui.KeyH)
	tt.Key(0, ui.Key3)
	dragOn(tt, at, point{.5, .6}, point{.8, .9})
	if len(p.marks) != 4 || p.marks[0].Kind != redact || p.marks[1].Kind != pin || p.marks[2].Kind != pen || p.marks[3].Kind != highlight {
		t.Fatalf("marks %+v", p.marks)
	}
	if len(p.marks[2].Points) < 10 {
		t.Fatalf("the line kept %d points", len(p.marks[2].Points))
	}
	if _, ok := tt.Find("Remove hidden area"); !ok {
		t.Fatal("the hidden area has no row")
	}
	if _, ok := tt.Find("Note for mark 3"); !ok {
		t.Fatal("the highlight is not mark 3")
	}
	if err := tt.Click("Use marked-up image"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	notes := files.current.Notes
	for _, line := range []string{"1. Red pin at (200, 800): This icon is blurry", "2. Red freehand line spanning (100, 400) to (400, 450)",
		"3. Yellow highlight from (500, 600) to (800, 900)", "Solid dark blocks hide parts of the image on purpose."} {
		if !strings.Contains(notes, line) {
			t.Fatalf("legend lacks %q:\n%s", line, notes)
		}
	}
	if strings.Contains(notes, "(600, 100)") {
		t.Fatal("the legend locates the hidden area")
	}
	got, source := decodePNG(t, files.current.File.Data), decodePNG(t, original)
	// Every pixel of the hidden area is the redaction color, edges included.
	want := color.Color(color.NRGBA{R: pal.Redaction.R, G: pal.Redaction.G, B: pal.Redaction.B, A: 255})
	for py := 30; py < 90; py++ {
		for px := 240; px < 360; px++ {
			if c := color.NRGBAModel.Convert(got.At(px, py)); c != want {
				t.Fatalf("hidden pixel (%d, %d) is %+v", px, py, c)
			}
		}
	}
	for _, xy := range [][2]int{{239, 60}, {360, 60}, {300, 29}, {300, 90}} {
		if a, b := color.NRGBAModel.Convert(got.At(xy[0], xy[1])), color.NRGBAModel.Convert(source.At(xy[0], xy[1])); a != b {
			t.Fatalf("pixel %v beside the hidden area changed from %+v to %+v", xy, b, a)
		}
	}
	lit, plain := color.NRGBAModel.Convert(got.At(280, 240)).(color.NRGBA), color.NRGBAModel.Convert(source.At(280, 240)).(color.NRGBA)
	if lit == plain || lit.B >= plain.B {
		t.Fatalf("the highlight did not tint yellow: %+v over %+v", lit, plain)
	}
}

func TestALineMovesWithAllItsPoints(t *testing.T) {
	h, p, _, _, _ := start(t)
	tt, at := editor(t, h, p)
	tt.Key(0, ui.KeyD)
	dragOn(tt, at, point{.2, .2}, point{.4, .4})
	p.focus = canvasFocus
	tt.Frame()
	tt.Key(0, ui.KeyV)
	before := p.marks[0]
	dragOn(tt, at, point{.3, .3}, point{.5, .3})
	m := p.marks[0]
	if !near(m.A.X, before.A.X+.2) || !near(m.Points[len(m.Points)-1].X, .6) || !near(before.Points[len(before.Points)-1].X, .4) {
		t.Fatalf("moved %+v from %+v", m, before)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if !p.marks[0].same(before) || !near(p.marks[0].Points[0].X, .2) {
		t.Fatal("undo did not put the line back")
	}
}

func TestAScaledDownImageSendsSmallMarksAgainAtFullResolution(t *testing.T) {
	fit := &sizes{}
	h, p, files, _, ctl := start(t, fit.New)
	ctl.Conv.Model = agent.ModelRef{Provider: "p", ID: "m"}
	tt, at := editor(t, h, p)
	if fit.model.ID != "m" || p.sentW != 200 || !tt.HasText("screen.jpg · 400 × 300 · sent at 200 × 150") {
		t.Fatalf("sent size %d×%d for %+v", p.sentW, p.sentH, fit.model)
	}
	tt.Key(0, ui.KeyM)
	if !p.modelView {
		t.Fatal("M did not show the model's view")
	}
	if w, h := p.sentPreview.Size(); w != 200 || h != 150 {
		t.Fatalf("the model's view is %d×%d", w, h)
	}
	dragOn(tt, at, point{.25, .25}, point{.5, .5})
	tt.Type("Too small to read")
	p.focus = canvasFocus
	tt.Frame()
	tt.Key(0, ui.KeyA)
	dragOn(tt, at, point{.9, .9}, point{.7, .7})
	if !p.marks[0].Detail || p.marks[1].Detail {
		t.Fatalf("details %+v", p.marks)
	}
	if err := tt.Click("Use marked-up image"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	if len(files.details) != 1 || files.details[0].Name != "mark 1 at full resolution" || files.details[0].File.Name != "screen mark 1.png" {
		t.Fatalf("details %+v", files.details)
	}
	part := decodePNG(t, files.details[0].File.Data)
	if part.Bounds().Dx() != p.kept[1].marks[0].detailRect(shotW, shotH).Dx() || part.Bounds().Dx() != 148 {
		t.Fatalf("the detail is %v", part.Bounds())
	}
}

func TestDetailsTurnOnPerMarkWithUndo(t *testing.T) {
	h, p, files, _, _ := start(t)
	tt, at := editor(t, h, p)
	if p.sentW != 0 || p.sentPreview != nil {
		t.Fatal("a backend without sizes reported one")
	}
	dragOn(tt, at, point{.1, .1}, point{.3, .3})
	if p.marks[0].Detail {
		t.Fatal("a detail without a known scale")
	}
	if err := tt.Click("Send mark 1 at full resolution"); err != nil {
		t.Fatal(err)
	}
	if !p.marks[0].Detail {
		t.Fatal("the toggle did not ask for a detail")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if p.marks[0].Detail {
		t.Fatal("undo kept the detail")
	}
	tt.Click("Send mark 1 at full resolution")
	tt.Frame()
	tt.Click("Use marked-up image")
	h.Flush()
	if len(files.details) != 1 {
		t.Fatalf("details %d", len(files.details))
	}
}

func TestLegendNamesEveryMarkInOrder(t *testing.T) {
	inks := native.PaletteOf(&ui.Theme{}).Inks
	marks := []mark{
		{ID: 7, Kind: box, A: point{.6, .5}, B: point{.2, .1}, Ink: 4},
		{ID: 3, Kind: arrow, A: point{.1, .9}, B: point{.4, .45}},
	}
	got := legend(marks, map[uint64]string{7: "  too\nlarge ", 3: ""}, " Fix the layout ", inks)
	want := "Fix the layout\nI drew the numbered marks; they are not part of the image. Positions are x, y from 0 to 1000 across and down.\n" +
		"1. Blue box from (200, 100) to (600, 500): too large\n" +
		"2. Red arrow from (100, 900) pointing at (400, 450)"
	if got != want {
		t.Fatalf("legend\n%s", got)
	}
	if legend(nil, nil, "", inks) != "" || legend(nil, nil, "Just this", inks) != "Just this" {
		t.Fatal("a legend without marks")
	}
	if !strings.Contains(legend(marks[:1], nil, "", inks), "1. Blue box") {
		t.Fatal("a mark without a note")
	}
}

func TestDecodeRefusesOversizedOrInvalidImagesBeforeAllocatingPixels(t *testing.T) {
	if _, err := decode([]byte("not an image")); err == nil {
		t.Fatal("invalid image accepted")
	}
	if validDimensions(100000, 100000) || validDimensions(0, 4) || !validDimensions(3840, 2160) {
		t.Fatal("invalid image dimension limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := exportPNG(ctx, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil, native.PaletteOf(&ui.Theme{}), "x.png"); err == nil {
		t.Fatal("cancelled export succeeded")
	}
}

func startCapturing(t *testing.T, capture captureFunc) (*kerneltest.Harness, *Plugin, *images, *kittest.Host) {
	t.Helper()
	ctl, host := &plugintest.Chat{}, &kittest.Host{}
	files := &images{}
	var p *Plugin
	h := kerneltest.New(t, files.New, ctl.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{capture: capture} }))
	return h, p, files, host
}

func runCommand(t *testing.T, h *kerneltest.Harness, id string) {
	t.Helper()
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == id {
			kernel.Invoke(h, e, func(c kernel.Command) { c.Run() })
			return
		}
	}
	t.Fatalf("no command %s", id)
}

func TestACapturedScreenshotIsAttachedAndOpensInTheEditor(t *testing.T) {
	data := screenshot(t)
	var hiddenDuring bool
	var host *kittest.Host
	h, p, files, host := startCapturing(t, func(context.Context) ([]byte, error) {
		hiddenDuring = host.Hidden
		return data, nil
	})
	runCommand(t, h, "markup.capture")
	if !host.Hidden {
		t.Fatal("the window stayed on screen for the capture")
	}
	h.Flush()
	if !hiddenDuring || host.Hidden {
		t.Fatalf("hidden during %v, after %v", hiddenDuring, host.Hidden)
	}
	if files.current.ID != 1 || files.current.File.Name != "Screenshot.png" || !bytes.Equal(files.current.File.Data, data) {
		t.Fatalf("attached %+v", files.current.File.Name)
	}
	if !p.open || p.source.ID != 1 {
		t.Fatal("the editor did not open on the screenshot")
	}
	h.Flush()
	if p.decoded == nil {
		t.Fatal("the screenshot did not load in the editor")
	}
	runCommand(t, h, "markup.capture")
	h.Flush()
	if files.current.File.Name != "Screenshot 2.png" {
		t.Fatalf("second screenshot %q", files.current.File.Name)
	}
}

func TestCaptureWorksFromAnyAppOnItsOwnKeys(t *testing.T) {
	h, _, _, _ := startCapturing(t, func(context.Context) ([]byte, error) { return nil, nil })
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == "markup.capture" && (e.Value.Global != "ctrl+cmd+shift+2" || !slices.Equal(e.Value.Keys, []string{"cmd+shift+2"})) {
			t.Fatalf("keys %q, global %q", e.Value.Keys, e.Value.Global)
		}
	}
}

func TestACancelledOrFailedCaptureBringsTheWindowBackAndAttachesNothing(t *testing.T) {
	var fail error
	h, p, files, host := startCapturing(t, func(context.Context) ([]byte, error) { return nil, fail })
	runCommand(t, h, "markup.capture")
	h.Flush()
	if host.Hidden || files.current.ID != 0 || p.open || len(host.Toasts) != 0 {
		t.Fatal("Escape during the capture attached or reported something")
	}
	fail = errors.New("no permission")
	runCommand(t, h, "markup.capture")
	h.Flush()
	if host.Hidden || files.current.ID != 0 || len(host.Toasts) != 1 || host.Toasts[0] != "The screenshot was not taken" {
		t.Fatalf("toasts %q hidden %v", host.Toasts, host.Hidden)
	}
}
