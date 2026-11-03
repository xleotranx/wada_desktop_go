package video

import (
	"context"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

const (
	timelinePad   = 12 // DIPs before the first and after the last second
	rulerHeight   = 22 // DIPs of the ruler above the tracks
	rowHeight     = 34 // DIPs of one track
	rowGap        = 4  // DIPs between tracks
	clipEdge      = 6  // DIPs of a clip's edge that trim instead of move
	minClip       = 1.0 / 60
	timelineMaxH  = 232
	playheadWidth = 1.5
)

// dragKind is what a pointer drag on the timeline is doing.
type dragKind uint8

const (
	dragNone dragKind = iota
	dragScrub
	dragMove
	dragTrimStart
	dragTrimEnd
)

// dragState is a timeline drag in progress: a clip's timing as it was when
// the drag started, and where the pointer was.
type dragState struct {
	kind     dragKind
	id       string
	x        float32
	start    float64
	duration float64
}

// row is one track of the timeline.
type row struct {
	track int
	clips []*Clip
}

// rows groups the project's clips by track, in time order.
func (p *Plugin) rows() []row {
	if p.project == nil || p.project.Root == nil {
		return nil
	}
	byTrack := map[int][]*Clip{}
	for _, c := range p.project.Root.Clips {
		byTrack[c.Track] = append(byTrack[c.Track], c)
	}
	tracks := make([]int, 0, len(byTrack))
	for t := range byTrack {
		tracks = append(tracks, t)
	}
	sort.Ints(tracks)
	out := make([]row, 0, len(tracks))
	for _, t := range tracks {
		clips := byTrack[t]
		sort.SliceStable(clips, func(i, j int) bool { return clips[i].Start < clips[j].Start })
		out = append(out, row{track: t, clips: clips})
	}
	return out
}

// timelineHeight is how tall the timeline is for the rows it has.
func (p *Plugin) timelineHeight() float32 {
	n := len(p.rows())
	if n == 0 {
		n = 1
	}
	h := float32(rulerHeight + n*(rowHeight+rowGap) + timelinePad)
	return min(h, timelineMaxH)
}

// span is the time the timeline draws.
func (p *Plugin) span() float64 {
	if d := p.duration(); d > 0 {
		return d
	}
	return 1
}

// xOf puts a time on the timeline of width w.
func (p *Plugin) xOf(t float64, w float32) float32 {
	if w <= 2*timelinePad {
		return timelinePad
	}
	return timelinePad + float32(t/p.span())*(w-2*timelinePad)
}

// tOf reads the time at a timeline x.
func (p *Plugin) tOf(x float32, w float32) float64 {
	if w <= 2*timelinePad {
		return 0
	}
	t := float64((x - timelinePad) / (w - 2*timelinePad) * float32(p.span()))
	return p.clampTime(t, 0, p.span())
}

// clampTime keeps a time inside the video and on a frame.
func (p *Plugin) clampTime(t, lo, hi float64) float64 {
	step := 1 / p.fps()
	t = math.Round(t/step) * step
	if t < lo {
		t = lo
	}
	if t > hi {
		t = hi
	}
	return t
}

// shownTiming is a clip's timing as the timeline shows it: the drag's values
// while it is dragged, the file's otherwise.
func (p *Plugin) shownTiming(c *Clip) (start, duration float64) {
	if p.drag.kind != dragNone && p.drag.id == c.ID {
		return p.drag.start, p.drag.duration
	}
	return c.Start, c.Duration
}

// clipBand is the y range of a row on the timeline, from the timeline's top.
func clipBand(i int) (y, h float32) {
	y = rulerHeight + float32(i)*(rowHeight+rowGap)
	return y, rowHeight
}

// timelineView draws the ruler, the clips and the playhead, and takes the
// pointer for scrubbing and editing.
func (p *Plugin) timelineView(c *ui.Context, pal native.Palette) {
	rows := p.rows()
	w := p.timeline.Bounds(c).W
	p.tlWidth = w
	ui.Box(c.Key("timeline")).FillWidth().Height(p.timelineHeight()).Shrink(0).Clip().
		Background(pal.PanelBg).Bind(&p.timeline).Label("Timeline").FocusBind(&p.focus, true).
		HandleInput(p.timelineInput).Cursor(ui.CursorPointer).
		Draw(func(painter *ui.Painter, r ui.Rect) {
			p.drawRuler(painter, r, pal)
		}).Children(func() {
		if w <= 2*timelinePad {
			return
		}
		for i, row := range rows {
			y, h := clipBand(i)
			ui.Text(c, strconv.Itoa(row.track)).FontSize(10).TextColor(pal.Faint).FontFeatures("tnum").
				Absolute().Left(2).Top(y + h/2 - 7).Width(8).SingleLine()
			for _, clip := range row.clips {
				p.clipView(c, pal, clip, i, w, h)
			}
			_ = row
		}
		if b := p.playheadX(w); b >= 0 {
			ui.Box(c.Key("playhead")).Absolute().Left(b - 1).Top(0).Width(playheadWidth).
				FillHeight().Background(pal.Link).PassThrough()
			ui.Box(c.Key("playhead-head")).Absolute().Left(b - 4).Top(0).Width(8).Height(8).
				Background(pal.Link).Radius(2).PassThrough()
		}
	})
}

// drawRuler paints the ruler's seconds behind the clips.
func (p *Plugin) drawRuler(painter *ui.Painter, r ui.Rect, pal native.Palette) {
	w := r.W
	painter.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: rulerHeight}, pal.PanelBg, 0)
	span := p.span()
	step := tickStep(float64(r.W - 2*timelinePad))
	for t := 0.0; t <= span+1e-9; t += step {
		x := p.xOf(t, w)
		painter.Fill(ui.Rect{X: x, Y: rulerHeight - 4, W: 1, H: 4}, pal.Hairline, 0)
		if t > 0 {
			painter.Fill(ui.Rect{X: x, Y: rulerHeight, W: 1, H: r.H - rulerHeight}, pal.Hairline, 0)
		}
	}
	painter.Fill(ui.Rect{X: 0, Y: rulerHeight - 1, W: w, H: 1}, pal.Hairline, 0)
}

// tickStep is the seconds between ruler ticks, so ticks stay readable.
func tickStep(pixelWidth float64) float64 {
	if pixelWidth <= 0 {
		return 1
	}
	// One tick per ~70 DIPs, rounded up to 1, 2 or 5 times a power of ten.
	want := 70 / pixelWidth * 10
	steps := []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300}
	for _, s := range steps {
		if s >= want {
			return s
		}
	}
	return 600
}

// playheadX is where the playhead sits, or -1 while it is off the timeline.
func (p *Plugin) playheadX(w float32) float32 {
	if p.duration() <= 0 {
		return -1
	}
	return p.xOf(p.playhead, w)
}

// clipView is one clip's bar: its label, its selection and its edges.
func (p *Plugin) clipView(c *ui.Context, pal native.Palette, clip *Clip, i int, w float32, h float32) {
	start, duration := p.shownTiming(clip)
	y, _ := clipBand(i)
	x0 := p.xOf(start, w)
	x1 := p.xOf(start+duration, w)
	if !clip.HasDuration || duration <= 0 {
		x1 = x0 + 6
	}
	if x1-x0 < 6 {
		x1 = x0 + 6
	}
	selected := clip.ID == p.selected
	bg := pal.Raised
	border := pal.Hairline
	if clip.Mount != "" {
		bg = pal.DetailBg
	}
	switch {
	case selected:
		bg, border = pal.Selected, pal.Link
	case clip.ID == p.hover:
		bg = pal.Hover
	}
	bar := ui.Box(c.Key(clip.ID)).Absolute().Left(x0).Top(y).Width(x1-x0).Height(h).
		Background(bg).Radius(5).Border(1, border).Clip().PassThrough().Label(clip.Label)
	bar.Children(func() {
		ui.Text(c, clip.Label).FontSize(11.5).TextColor(pal.Text).SingleLine().Ellipsis("…").
			Padding(0, 6).MaxLines(1)
	})
}

// timelineInput scrubs, selects and drags clips.
func (p *Plugin) timelineInput(ev ui.InputEvent) bool {
	w := p.tlWidth
	if w <= 2*timelinePad {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		if ev.Y < rulerHeight {
			p.drag = dragState{kind: dragScrub}
			p.seek(p.tOf(ev.X, w))
			return true
		}
		if clip, edge := p.clipAt(ev.X, ev.Y, w); clip != nil {
			p.selected = clip.ID
			start, duration := clip.Start, clip.Duration
			switch edge {
			case dragMove:
				p.drag = dragState{kind: dragMove, id: clip.ID, x: ev.X, start: start, duration: duration}
			case dragTrimStart:
				p.drag = dragState{kind: dragTrimStart, id: clip.ID, x: ev.X, start: start, duration: duration}
			case dragTrimEnd:
				p.drag = dragState{kind: dragTrimEnd, id: clip.ID, x: ev.X, start: start, duration: duration}
			case dragNone, dragScrub:
				return false
			}
			return true
		}
		p.selected = ""
		p.drag = dragState{kind: dragScrub}
		p.seek(p.tOf(ev.X, w))
		return true
	case ui.InputPointerMove:
		if p.drag.kind == dragNone {
			clip, _ := p.clipAt(ev.X, ev.Y, w)
			id := ""
			if clip != nil {
				id = clip.ID
			}
			if id != p.hover {
				p.hover = id
			}
			return clip != nil
		}
		delta := float64((ev.X - p.drag.x) / (w - 2*timelinePad) * float32(p.span()))
		switch p.drag.kind {
		case dragNone:
			return false
		case dragScrub:
			p.seek(p.tOf(ev.X, w))
		case dragMove:
			// A clip may move past the film's end: the root's length grows to
			// hold it when the drag is written.
			p.drag.start = p.clampTime(p.drag.start+delta, 0, math.Max(0, p.span()))
			p.drag.x = ev.X
		case dragTrimStart:
			end := p.drag.start + p.drag.duration
			start := p.clampTime(p.drag.start+delta, 0, end-minClip)
			p.drag.start = start
			p.drag.duration = end - start
			p.drag.x = ev.X
		case dragTrimEnd:
			p.drag.duration = p.clampTime(p.drag.duration+delta, minClip, math.Max(minClip, 2*p.span()-p.drag.start))
			p.drag.x = ev.X
		}
		return true
	case ui.InputPointerUp:
		kind, id := p.drag.kind, p.drag.id
		start, duration := p.drag.start, p.drag.duration
		p.drag = dragState{}
		if kind == dragMove || kind == dragTrimStart || kind == dragTrimEnd {
			p.writeTiming(id, start, duration)
			return true
		}
		return kind == dragScrub
	case ui.InputKeyDown:
		return p.key(ev)
	case ui.InputScroll, ui.InputZoom:
		return false
	case ui.InputKeyUp, ui.InputText, ui.InputCompose, ui.InputCommand, ui.InputSelect:
	}
	return false
}

// clipAt finds the clip under a timeline point, and what dragging there does.
func (p *Plugin) clipAt(x, y float32, w float32) (*Clip, dragKind) {
	rows := p.rows()
	for i, row := range rows {
		top, height := clipBand(i)
		if y < top || y > top+height {
			continue
		}
		for _, clip := range row.clips {
			start, duration := p.shownTiming(clip)
			x0 := p.xOf(start, w)
			x1 := p.xOf(start+duration, w)
			if !clip.HasDuration || duration <= 0 || x1-x0 < 6 {
				x1 = x0 + 6
			}
			if x < x0-2 || x > x1+2 {
				continue
			}
			switch {
			case x <= x0+clipEdge:
				return clip, dragTrimStart
			case x >= x1-clipEdge:
				return clip, dragTrimEnd
			}
			return clip, dragMove
		}
	}
	return nil, dragNone
}

// transportKeys are the keys that drive the transport while the timeline has
// the keyboard.
var transportKeys = map[ui.Key]func(*Plugin){
	ui.KeySpace: func(p *Plugin) { p.togglePlay() },
	ui.KeyLeft:  func(p *Plugin) { p.seek(p.playhead - 1/p.fps()) },
	ui.KeyRight: func(p *Plugin) { p.seek(p.playhead + 1/p.fps()) },
	ui.KeyHome:  func(p *Plugin) { p.seek(0) },
	ui.KeyEnd:   func(p *Plugin) { p.seek(p.duration()) },
}

// key drives the transport while the timeline has the keyboard.
func (p *Plugin) key(ev ui.InputEvent) bool {
	if ev.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) != 0 {
		return false
	}
	run, ok := transportKeys[ev.Key]
	if !ok {
		return false
	}
	run(p)
	return true
}

// writeTiming saves a clip's timing and shows the project as written.
func (p *Plugin) writeTiming(id string, start, duration float64) {
	clip, ok := p.clip(id)
	if !ok || p.project == nil || p.dir == "" {
		return
	}
	dir, file, project := p.dir, clip.File, p.project
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writeClipTiming(dir, file, project, id, start, duration)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The clip did not change", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.reload()
	})
}

// writeClipTiming writes one clip's timing into its file, and extends the
// root's duration when the clip now ends past it.
func writeClipTiming(dir, file string, project *Project, id string, start, duration float64) error {
	path := projectFile(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := setClipTiming(string(data), id, start, duration)
	if err != nil {
		return err
	}
	if project.Root != nil && project.Root.HasDuration && file == project.File {
		if end := start + duration; end > project.Root.DeclaredSeconds {
			if extended, err := setRootDuration(out, project.Root.ID, end); err == nil {
				out = extended
			}
		}
	}
	return writeFileAtomic(path, []byte(out), 0o644)
}

// trackName is what the sidebar calls a track.
func trackName(kind string) string {
	switch kind {
	case "video":
		return "Video"
	case "audio":
		return "Audio"
	case "captions":
		return "Captions"
	}
	return "Graphics"
}
