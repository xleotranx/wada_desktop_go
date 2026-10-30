package markup

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"

	attachment "wada-desktop-go/internal/contract/attachments"

	"github.com/xleotranx/wada-kit/native"
)

type kind uint8

const (
	arrow kind = iota
	box
	pin       // a numbered point
	pen       // a freehand line
	highlight // a box the content shows through
	redact    // a block that hides what it covers; it has no number or note
)

func (k kind) String() string {
	switch k {
	case arrow:
		return "Arrow"
	case box:
		return "Box"
	case pin:
		return "Pin"
	case pen:
		return "Freehand line"
	case highlight:
		return "Highlight"
	case redact:
		return "Hidden area"
	}
	return "Mark"
}

// area reports whether the kind is a rectangle resized by its corners.
func (k kind) area() bool { return k == box || k == highlight || k == redact }

// tool is what a press on the canvas does: pick selects, moves and
// resizes marks; the others draw a new mark of their kind.
type tool uint8

const (
	pick tool = iota
	drawArrow
	drawBox
	drawPin
	drawPen
	drawHighlight
	drawRedact
)

// kind is the mark a drawing tool makes.
func (t tool) kind() kind {
	switch t {
	case pick, drawBox:
		return box
	case drawArrow:
		return arrow
	case drawPin:
		return pin
	case drawPen:
		return pen
	case drawHighlight:
		return highlight
	case drawRedact:
		return redact
	}
	return box
}

// maxMarks keeps every badge to two digits; maxPoints bounds a freehand
// line; maxDetails bounds the full-resolution parts sent with an image.
const (
	maxMarks   = 99
	maxPoints  = 2000
	maxDetails = 4
)

// point is a position as fractions of the image, so marks are independent
// of how large the editor shows it.
type point struct{ X, Y float32 }

// mark is one annotation. Its number is its place among the numbered marks;
// ID is stable across moves, undo and reordering, and keys its note.
// Points, of a freehand line only, are never changed in place, so undo
// steps can share them.
type mark struct {
	ID     uint64
	Kind   kind
	A, B   point // an area's corners; an arrow's tail and tip; a pin's point; a line's bounds
	Ink    int   // index into native.Palette.Inks
	Points []point
	// Detail sends the mark's part of the image again at full resolution,
	// for a box, a highlight or a pin.
	Detail bool
}

// same reports whether two marks are drawn the same; a line's points
// change only with its bounds.
func (m mark) same(o mark) bool {
	return m.ID == o.ID && m.Kind == o.Kind && m.A == o.A && m.B == o.B && m.Ink == o.Ink &&
		len(m.Points) == len(o.Points) && m.Detail == o.Detail
}

// detailable reports whether the mark has a part of the image worth
// sending at full resolution.
func (m mark) detailable() bool { return m.Kind == box || m.Kind == highlight || m.Kind == pin }

// detailRect is the part of a w×h image a detail of m shows, in pixels: an
// area with a margin around it, or a square around a pin, inside the image.
func (m mark) detailRect(w, h int) image.Rectangle {
	var x0, y0, x1, y1 float32
	if m.Kind == pin {
		half := max(160, float32(min(w, h))/10)
		x, y := m.A.X*float32(w), m.A.Y*float32(h)
		x0, y0, x1, y1 = x-half, y-half, x+half, y+half
	} else {
		x0, y0 = min(m.A.X, m.B.X)*float32(w), min(m.A.Y, m.B.Y)*float32(h)
		x1, y1 = max(m.A.X, m.B.X)*float32(w), max(m.A.Y, m.B.Y)*float32(h)
		pad := max(24, max(x1-x0, y1-y0)*.15)
		x0, y0, x1, y1 = x0-pad, y0-pad, x1+pad, y1+pad
	}
	r := image.Rect(int(math.Floor(float64(x0))), int(math.Floor(float64(y0))), int(math.Ceil(float64(x1))), int(math.Ceil(float64(y1))))
	return r.Intersect(image.Rect(0, 0, w, h))
}

// wantsDetail reports whether m's part of a w×h image, sent at sw pixels
// wide, comes out so small that a full-resolution copy helps: the image is
// scaled down and the part is under 480 pixels on its longer side as sent.
func (m mark) wantsDetail(w, h, sw int) bool {
	if !m.detailable() || sw <= 0 || sw >= w {
		return false
	}
	r := m.detailRect(w, h)
	return float32(max(r.Dx(), r.Dy()))*float32(sw)/float32(w) < 480
}

func (m mark) numbered() bool { return m.Kind != redact }

// numbers gives each numbered mark its number, and 0 to the others.
func numbers(marks []mark) []int {
	out, n := make([]int, len(marks)), 0
	for i, m := range marks {
		if m.numbered() {
			n++
			out[i] = n
		}
	}
	return out
}

// corners are the points a selected mark can be resized by: an area's four
// corners and an arrow's ends. Pins and lines are only moved.
func (m mark) corners() []point {
	switch m.Kind {
	case box, highlight, redact:
		return []point{m.A, {m.B.X, m.A.Y}, m.B, {m.A.X, m.B.Y}}
	case arrow:
		return []point{m.A, m.B}
	case pin, pen:
	}
	return nil
}

// withCorner moves corner i, as corners numbers them, to at.
func (m mark) withCorner(i int, at point) mark {
	switch {
	case i == 0:
		m.A = at
	case m.Kind == arrow && i == 1, m.Kind.area() && i == 2:
		m.B = at
	case m.Kind.area() && i == 1:
		m.B.X, m.A.Y = at.X, at.Y
	case m.Kind.area() && i == 3:
		m.A.X, m.B.Y = at.X, at.Y
	}
	return m
}

// moved shifts m by d, stopping at the image's edges.
func (m mark) moved(d point) mark {
	lo := point{min(m.A.X, m.B.X), min(m.A.Y, m.B.Y)}
	hi := point{max(m.A.X, m.B.X), max(m.A.Y, m.B.Y)}
	d.X = max(-lo.X, min(1-hi.X, d.X))
	d.Y = max(-lo.Y, min(1-hi.Y, d.Y))
	m.A = point{m.A.X + d.X, m.A.Y + d.Y}
	m.B = point{m.B.X + d.X, m.B.Y + d.Y}
	if m.Points != nil {
		points := make([]point, len(m.Points))
		for i, p := range m.Points {
			points[i] = point{p.X + d.X, p.Y + d.Y}
		}
		m.Points = points
	}
	return m
}

// extended adds a point to a freehand line, widening its bounds.
func (m mark) extended(at point) mark {
	if len(m.Points) >= maxPoints {
		return m
	}
	m.Points = append(m.Points[:len(m.Points):len(m.Points)], at)
	m.A = point{min(m.A.X, at.X), min(m.A.Y, at.Y)}
	m.B = point{max(m.B.X, at.X), max(m.B.Y, at.Y)}
	return m
}

// Sizes in image pixels, so a mark reads the same at every zoom and in the
// exported PNG, and survives the agent scaling a large screenshot down.
func strokeWidth(w, h int) float32 { return max(2, min(10, float32(min(w, h))/240)) }
func badgeRadius(w, h int) float32 { return max(10, min(40, float32(min(w, h))/60)) }

// radius is the size of a mark's badge; a pin, which is only its badge, is
// larger.
func (m mark) radius(w, h int) float32 {
	if m.Kind == pin {
		return badgeRadius(w, h) * 1.3
	}
	return badgeRadius(w, h)
}

// badgeAt is where a mark's number sits, in image pixels: on an area's
// top-left corner, on a pin's point, at a line's start, or just behind an
// arrow's tail, so the line and the tail's handle leave it clear. It stays
// inside the image.
func (m mark) badgeAt(w, h int) (x, y float32) {
	r := m.radius(w, h)
	switch m.Kind {
	case box, highlight, redact:
		x, y = min(m.A.X, m.B.X)*float32(w), min(m.A.Y, m.B.Y)*float32(h)
	case pin:
		x, y = m.A.X*float32(w), m.A.Y*float32(h)
	case pen:
		start := m.A
		if len(m.Points) > 0 {
			start = m.Points[0]
		}
		x, y = start.X*float32(w), start.Y*float32(h)
	case arrow:
		x, y = m.A.X*float32(w), m.A.Y*float32(h)
		dx, dy := x-m.B.X*float32(w), y-m.B.Y*float32(h)
		if l := float32(math.Hypot(float64(dx), float64(dy))); l > 0 {
			x, y = x+dx/l*r*1.2, y+dy/l*r*1.2
		}
	}
	clamp := func(v, size float32) float32 { return max(min(r, size/2), min(size-r, v)) }
	return clamp(x, float32(w)), clamp(y, float32(h))
}

// legend is what the agent reads with the marked-up image: what the image
// is about, then each numbered mark by number, color, kind and position,
// with its note, and whether parts were hidden. Positions run from 0 to
// 1000 so they don't depend on how a backend scales the image. Hidden areas
// are not located, so the words don't point at what was hidden.
func legend(marks []mark, notes map[uint64]string, about string, inks [5]native.Ink) string {
	var lines []string
	if about = strings.TrimSpace(about); about != "" {
		lines = append(lines, about)
	}
	at := func(p point) string {
		return fmt.Sprintf("(%d, %d)", int(math.Round(float64(p.X)*1000)), int(math.Round(float64(p.Y)*1000)))
	}
	lo := func(m mark) point { return point{min(m.A.X, m.B.X), min(m.A.Y, m.B.Y)} }
	hi := func(m mark) point { return point{max(m.A.X, m.B.X), max(m.A.Y, m.B.Y)} }
	var described []string
	hidden := false
	for i, n := range numbers(marks) {
		m := marks[i]
		if n == 0 {
			hidden = true
			continue
		}
		line := fmt.Sprintf("%d. %s %s ", n, inkOf(inks, m).Name, strings.ToLower(m.Kind.String()))
		switch m.Kind {
		case box, highlight:
			line += "from " + at(lo(m)) + " to " + at(hi(m))
		case arrow:
			line += "from " + at(m.A) + " pointing at " + at(m.B)
		case pin:
			line += "at " + at(m.A)
		case pen:
			line += "spanning " + at(lo(m)) + " to " + at(hi(m))
		case redact:
		}
		if note := strings.Join(strings.Fields(notes[m.ID]), " "); note != "" {
			line += ": " + note
		}
		described = append(described, line)
	}
	if len(described) > 0 {
		lines = append(lines, "I drew the numbered marks; they are not part of the image. Positions are x, y from 0 to 1000 across and down.")
		lines = append(lines, described...)
	}
	if hidden {
		lines = append(lines, "Solid dark blocks hide parts of the image on purpose.")
	}
	return strings.Join(lines, "\n")
}

func inkOf(inks [5]native.Ink, m mark) native.Ink { return inks[max(0, min(len(inks)-1, m.Ink))] }

func validDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= attachment.MaxImageSide && h <= attachment.MaxImageSide && int64(w)*int64(h) <= attachment.MaxImagePixels
}

func decode(data []byte) (image.Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !validDimensions(config.Width, config.Height) {
		return nil, errors.New("choose a screenshot of at most 16 megapixels and 16,384 pixels per side")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func abs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
