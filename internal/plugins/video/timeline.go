package video

import (
	"context"
	"fmt"
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
// the drag started, where the pointer was, and the track it would land on.
type dragState struct {
	kind      dragKind
	id        string
	x         float32
	start     float64
	duration  float64
	track     int
	fromTrack int
}

// row is one track of the timeline.
type row struct {
	track int
	clips []*Clip
}

// comp is the composition the timeline edits: the focused scene, or the
// project's root.
func (p *Plugin) comp() *Composition {
	if p.scene != nil && p.scene.Comp != nil {
		return p.scene.Comp
	}
	if p.project != nil {
		return p.project.Root
	}
	return nil
}

// sceneLength is a scene's own length: its root's data-duration, else the end
// of its last clip.
func sceneLength(s *Scene) float64 {
	if s == nil || s.Comp == nil {
		return 0
	}
	if s.Comp.HasDuration && s.Comp.DeclaredSeconds > 0 {
		return s.Comp.DeclaredSeconds
	}
	end := 0.0
	for _, c := range s.Comp.Clips {
		end = math.Max(end, c.End())
	}
	return end
}

// localAt maps a time in the film to the focused scene's own time.
func (p *Plugin) localAt(root float64) float64 {
	s := p.scene
	if s == nil || s.Host == nil {
		return root
	}
	rate := s.Rate
	if rate <= 0 {
		rate = 1
	}
	return s.PlaybackStart + (root-s.Host.Start)*rate
}

// rootAt maps the focused scene's own time back to the film's.
func (p *Plugin) rootAt(local float64) float64 {
	s := p.scene
	if s == nil || s.Host == nil {
		return local
	}
	rate := s.Rate
	if rate <= 0 {
		rate = 1
	}
	return s.Host.Start + (local-s.PlaybackStart)/rate
}

// seekLocal shows the frame at a time of what the timeline draws.
func (p *Plugin) seekLocal(t float64) { p.seek(p.rootAt(t)) }

// startOfSpan is where the timeline's time 0 sits in the film.
func (p *Plugin) startOfSpan() float64 { return p.rootAt(0) }

// displayTime is the playhead as the transport shows it: the scene's own
// time when one is focused, the film's otherwise.
func (p *Plugin) displayTime() float64 {
	if p.scene == nil {
		return p.playhead
	}
	return math.Max(0, math.Min(p.localAt(p.playhead), p.span()))
}

// displayDuration is the length the transport shows.
func (p *Plugin) displayDuration() float64 { return p.span() }

// playLimit is where playback stops: the scene's end when one is focused.
func (p *Plugin) playLimit() float64 {
	if p.scene != nil {
		return p.rootAt(p.span())
	}
	return p.duration()
}

// localNow is the playhead in the timeline's time, clamped to what it draws.
func (p *Plugin) localNow() float64 {
	local, ok := p.localPlayhead()
	if !ok {
		return p.span()
	}
	return local
}

// localPlayhead is the playhead in the timeline's time, and whether it is
// inside what the timeline draws.
func (p *Plugin) localPlayhead() (float64, bool) {
	if p.scene == nil {
		return p.playhead, true
	}
	local := p.localAt(p.playhead)
	return local, local >= -1e-6 && local <= p.span()+1e-6
}

// laneRow is the row a track draws in.
func (p *Plugin) laneRow(track int) (int, bool) {
	for i, row := range p.rows() {
		if row.track == track {
			return i, true
		}
	}
	return 0, false
}

// rows groups the composition's clips by track, in time order.
func (p *Plugin) rows() []row {
	comp := p.comp()
	if comp == nil {
		return nil
	}
	byTrack := map[int][]*Clip{}
	for _, c := range comp.Clips {
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

// viewSpan is the time the timeline shows at its zoom.
func (p *Plugin) viewSpan() float64 {
	span := p.span()
	if p.viewZoom <= 1 {
		return span
	}
	return span / p.viewZoom
}

// viewEnd is the time at the timeline's right edge.
func (p *Plugin) viewEnd() float64 { return p.viewStart + p.viewSpan() }

// clampView keeps the view inside the composition.
func (p *Plugin) clampView() {
	if p.viewZoom <= 1 {
		p.viewStart, p.viewZoom = 0, 1
		return
	}
	if high := p.span() - p.viewSpan(); p.viewStart > high {
		p.viewStart = math.Max(0, high)
	}
	if p.viewStart < 0 {
		p.viewStart = 0
	}
}

// zoomAt scales the view by factor, keeping the time under x where it is.
func (p *Plugin) zoomAt(factor float64, x float32) {
	w := p.tlWidth
	if w <= 2*timelinePad {
		return
	}
	at := p.tOf(x, w)
	p.viewZoom = math.Max(1, math.Min(20, p.viewZoom*factor))
	p.viewStart = at - float64((x-timelinePad)/float32(w-2*timelinePad))*p.viewSpan()
	p.clampView()
}

// panBy moves the view by seconds.
func (p *Plugin) panBy(seconds float64) {
	p.viewStart += seconds
	p.clampView()
}

// span is the time the timeline draws: the scene's length, or the film's.
func (p *Plugin) span() float64 {
	if p.scene != nil {
		if d := sceneLength(p.scene); d > 0 {
			return d
		}
	}
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
	return timelinePad + float32((t-p.viewStart)/p.viewSpan())*(w-2*timelinePad)
}

// tOf reads the time at a timeline x.
func (p *Plugin) tOf(x float32, w float32) float64 {
	if w <= 2*timelinePad {
		return 0
	}
	t := p.viewStart + float64((x-timelinePad)/(w-2*timelinePad))*p.viewSpan()
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
		Background(pal.PanelBg).Bind(&p.timeline).Label("Timeline").FocusBind(&p.keys, true).
		HandleInput(p.timelineInput).Cursor(ui.CursorPointer).
		Draw(func(painter *ui.Painter, r ui.Rect) {
			p.drawRuler(painter, r, pal)
		}).Children(func() {
		if w <= 2*timelinePad {
			return
		}
		step := tickStep(float64(w-2*timelinePad), p.viewSpan())
		first := math.Floor(p.viewStart/step) * step
		for t := first; t <= p.viewEnd()+step+1e-9; t += step {
			if t < p.viewStart-1e-9 {
				continue
			}
			ui.Text(c, tickLabel(t)).FontSize(9.5).TextColor(pal.Faint).FontFeatures("tnum").SingleLine().
				Absolute().Left(p.xOf(t, w) + 3).Top(2).Width(64).PassThrough()
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
	step := tickStep(float64(r.W-2*timelinePad), p.viewSpan())
	first := math.Floor(p.viewStart/step) * step
	for t := first; t <= p.viewEnd()+step+1e-9; t += step {
		if t < p.viewStart-1e-9 {
			continue
		}
		x := p.xOf(t, w)
		painter.Fill(ui.Rect{X: x, Y: rulerHeight - 4, W: 1, H: 4}, pal.Hairline, 0)
		if t > 0 {
			painter.Fill(ui.Rect{X: x, Y: rulerHeight, W: 1, H: r.H - rulerHeight}, pal.Hairline, 0)
		}
	}
	painter.Fill(ui.Rect{X: 0, Y: rulerHeight - 1, W: w, H: 1}, pal.Hairline, 0)
}

// tickLabel writes a ruler tick's time: seconds, or minutes and seconds.
func tickLabel(t float64) string {
	if t < 60 {
		return strconv.FormatFloat(t, 'f', -1, 64) + "s"
	}
	return fmt.Sprintf("%d:%02ds", int(t)/60, int(t)%60)
}

// tickStep is the seconds between ruler ticks, about 70 DIPs apart at the
// view's zoom.
func tickStep(pixels, viewSpan float64) float64 {
	if pixels <= 0 || viewSpan <= 0 {
		return 1
	}
	want := 70 * viewSpan / pixels
	for _, s := range []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300} {
		if s >= want {
			return s
		}
	}
	return 600
}

// playheadX is where the playhead sits, or -1 while it is off the timeline.
func (p *Plugin) playheadX(w float32) float32 {
	if p.span() <= 0 {
		return -1
	}
	local, ok := p.localPlayhead()
	if !ok || local < p.viewStart || local > p.viewEnd() {
		return -1
	}
	return p.xOf(local, w)
}

// clipView is one clip's bar: its label, its selection and its edges.
func (p *Plugin) clipView(c *ui.Context, pal native.Palette, clip *Clip, i int, w float32, h float32) {
	start, duration := p.shownTiming(clip)
	if p.drag.kind == dragMove && p.drag.id == clip.ID {
		if lane, ok := p.laneRow(p.drag.track); ok {
			i = lane
		}
	}
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
			p.seekLocal(p.tOf(ev.X, w))
			return true
		}
		if clip, edge := p.clipAt(ev.X, ev.Y, w); clip != nil {
			p.selected = clip.ID
			start, duration := clip.Start, clip.Duration
			switch edge {
			case dragMove:
				p.drag = dragState{kind: dragMove, id: clip.ID, x: ev.X, start: start, duration: duration,
					track: clip.Track, fromTrack: clip.Track}
			case dragTrimStart:
				p.drag = dragState{kind: dragTrimStart, id: clip.ID, x: ev.X, start: start, duration: duration,
					track: clip.Track, fromTrack: clip.Track}
			case dragTrimEnd:
				p.drag = dragState{kind: dragTrimEnd, id: clip.ID, x: ev.X, start: start, duration: duration,
					track: clip.Track, fromTrack: clip.Track}
			case dragNone, dragScrub:
				return false
			}
			return true
		}
		p.selected = ""
		p.drag = dragState{kind: dragScrub}
		p.seekLocal(p.tOf(ev.X, w))
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
		delta := float64((ev.X-p.drag.x)/(w-2*timelinePad)) * p.viewSpan()
		switch p.drag.kind {
		case dragNone:
			return false
		case dragScrub:
			p.seekLocal(p.tOf(ev.X, w))
		case dragMove:
			// A clip may move past the film's end: the root's length grows to
			// hold it when the drag is written. Dropping it on another lane
			// changes its track.
			p.drag.start = p.clampTime(p.drag.start+delta, 0, math.Max(0, p.span()))
			p.drag.x = ev.X
			if track, ok := p.trackAt(ev.Y); ok {
				p.drag.track = track
			}
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
		start, duration, track := p.drag.start, p.drag.duration, p.drag.track
		from := p.drag.fromTrack
		p.drag = dragState{}
		if kind == dragMove || kind == dragTrimStart || kind == dragTrimEnd {
			lane := -1
			if track != from {
				lane = track
			}
			p.writeTiming(id, start, duration, lane)
			return true
		}
		return kind == dragScrub
	case ui.InputKeyDown:
		if ev.Mods&ui.Cmd != 0 {
			if ev.Key == ui.KeyEqual {
				p.zoomAt(1.5, timelinePad)
				return true
			}
			if ev.Key == ui.KeyMinus {
				p.zoomAt(1/1.5, timelinePad)
				return true
			}
			if ev.Key == ui.Key0 {
				p.viewStart, p.viewZoom = 0, 1
				return true
			}
		}
		if ev.Mods&ui.Cmd != 0 && ev.Key == ui.KeyZ {
			if ev.Mods&ui.Shift != 0 {
				p.redoEdit()
			} else {
				p.undoEdit()
			}
			return true
		}
		return p.key(ev)
	case ui.InputScroll:
		if ev.Mods&ui.Cmd != 0 {
			p.zoomAt(math.Exp(float64(ev.DY)*.01), ev.X)
			return true
		}
		p.panBy(float64(ev.DY) / float64(w-2*timelinePad) * p.viewSpan())
		return true
	case ui.InputZoom:
		p.zoomAt(float64(ev.Scale), ev.X)
		return true
	case ui.InputKeyUp, ui.InputText, ui.InputCompose, ui.InputCommand, ui.InputSelect:
	}
	return false
}

// trackAt is the track of the row under a timeline point.
func (p *Plugin) trackAt(y float32) (int, bool) {
	rows := p.rows()
	for i, row := range rows {
		top, height := clipBand(i)
		if y >= top && y <= top+height {
			return row.track, true
		}
	}
	return 0, false
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
	ui.KeyLeft:  func(p *Plugin) { p.seekLocal(p.localNow() - 1/p.fps()) },
	ui.KeyRight: func(p *Plugin) { p.seekLocal(p.localNow() + 1/p.fps()) },
	ui.KeyHome:  func(p *Plugin) { p.seekLocal(0) },
	ui.KeyEnd:   func(p *Plugin) { p.seekLocal(p.span()) },
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

// writeTiming saves a clip's timing and shows the project as written. A
// negative track keeps the clip's lane.
func (p *Plugin) writeTiming(id string, start, duration float64, track int) {
	clip, ok := p.clip(id)
	if !ok || p.project == nil || p.dir == "" {
		return
	}
	p.rememberTiming(editOf(clip))
	dir, file, project := p.dir, clip.File, p.project
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writeClipTiming(dir, file, project, id, start, duration, track)
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
func writeClipTiming(dir, file string, project *Project, id string, start, duration float64, track int) error {
	path := projectFile(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := setClipTimingOn(string(data), id, start, duration, track)
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
