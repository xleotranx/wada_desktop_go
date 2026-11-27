package terminal

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxScrollback   = 10000
	maxSnapshotRows = 2000
)

type parseState uint8

const (
	stateGround parseState = iota
	stateEsc
	stateCSI
	stateOSC
	stateOSCEsc
)

// VTBuffer is a concurrency-safe headless ANSI/VT terminal screen & 10,000-line
// scrollback buffer supporting screen capture and atomic reconnect snapshots.
type VTBuffer struct {
	mu         sync.Mutex
	cols       int
	rows       int
	cx         int
	cy         int
	altScreen  bool
	screen     [][]rune
	mainSave   [][]rune
	mainCX     int
	mainCY     int
	scrollback []string

	state   parseState
	csiBuf  strings.Builder
	utf8Buf []byte
}

func NewVTBuffer(cols, rows int) *VTBuffer {
	c, r := normalizeSize(cols, rows)
	b := &VTBuffer{cols: c, rows: r}
	b.screen = makeGrid(c, r)
	return b
}

func makeGrid(cols, rows int) [][]rune {
	g := make([][]rune, rows)
	for i := range g {
		row := make([]rune, cols)
		for j := range row {
			row[j] = ' '
		}
		g[i] = row
	}
	return g
}

func trimGridRow(row []rune) string {
	return strings.TrimRight(string(row), " ")
}

func (b *VTBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	data := p
	if len(b.utf8Buf) > 0 {
		data = append(append([]byte{}, b.utf8Buf...), p...)
		b.utf8Buf = nil
	}

	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(data) && len(data) < utf8.UTFMax {
			b.utf8Buf = append([]byte{}, data...)
			return
		}
		data = data[size:]
		b.stepRune(r)
	}
}

func (b *VTBuffer) stepRune(r rune) {
	switch b.state {
	case stateGround:
		switch r {
		case 0x1b:
			b.state = stateEsc
		case '\r':
			b.cx = 0
		case '\n', '\v', '\f':
			b.lineFeed()
		case '\b':
			if b.cx > 0 {
				b.cx--
			}
		case '\t':
			next := ((b.cx / 8) + 1) * 8
			if next >= b.cols {
				next = b.cols - 1
			}
			b.cx = next
		default:
			if r >= 0x20 {
				b.putRune(r)
			}
		}
	case stateEsc:
		switch r {
		case '[':
			b.csiBuf.Reset()
			b.state = stateCSI
		case ']':
			b.state = stateOSC
		default:
			b.state = stateGround
		}
	case stateCSI:
		if r >= 0x40 && r <= 0x7e {
			seq := b.csiBuf.String()
			b.state = stateGround
			b.execCSI(seq, r)
		} else {
			b.csiBuf.WriteRune(r)
		}
	case stateOSC:
		switch r {
		case 0x07:
			b.state = stateGround
		case 0x1b:
			b.state = stateOSCEsc
		}
	case stateOSCEsc:
		if r == '\\' {
			b.state = stateGround
		} else if r != 0x1b {
			b.state = stateOSC
		}
	}
}

func (b *VTBuffer) putRune(r rune) {
	if b.cx >= b.cols {
		b.cx = 0
		b.lineFeed()
	}
	b.screen[b.cy][b.cx] = r
	b.cx++
}

func (b *VTBuffer) lineFeed() {
	if b.cy < b.rows-1 {
		b.cy++
		return
	}
	if !b.altScreen {
		b.scrollback = append(b.scrollback, trimGridRow(b.screen[0]))
		if len(b.scrollback) > maxScrollback {
			b.scrollback = b.scrollback[len(b.scrollback)-maxScrollback:]
		}
	}
	copy(b.screen[0:], b.screen[1:])
	last := make([]rune, b.cols)
	for i := range last {
		last[i] = ' '
	}
	b.screen[b.rows-1] = last
}

func (b *VTBuffer) execCSI(seq string, cmd rune) {
	private := strings.HasPrefix(seq, "?")
	paramStr := strings.TrimPrefix(seq, "?")
	params := parseCSIParams(paramStr)
	p0 := 0
	if len(params) > 0 {
		p0 = params[0]
	}

	switch cmd {
	case 'A':
		n := max(1, p0)
		b.cy = max(0, b.cy-n)
	case 'B':
		n := max(1, p0)
		b.cy = min(b.rows-1, b.cy+n)
	case 'C':
		n := max(1, p0)
		b.cx = min(b.cols-1, b.cx+n)
	case 'D':
		n := max(1, p0)
		b.cx = max(0, b.cx-n)
	case 'G':
		col := max(1, p0) - 1
		b.cx = min(b.cols-1, col)
	case 'H', 'f':
		row := max(1, p0) - 1
		col := 0
		if len(params) > 1 {
			col = max(1, params[1]) - 1
		}
		b.cy = min(b.rows-1, row)
		b.cx = min(b.cols-1, col)
	case 'J':
		switch p0 {
		case 0:
			for x := b.cx; x < b.cols; x++ {
				b.screen[b.cy][x] = ' '
			}
			for y := b.cy + 1; y < b.rows; y++ {
				for x := 0; x < b.cols; x++ {
					b.screen[y][x] = ' '
				}
			}
		case 1:
			for y := 0; y < b.cy; y++ {
				for x := 0; x < b.cols; x++ {
					b.screen[y][x] = ' '
				}
			}
			for x := 0; x <= b.cx && x < b.cols; x++ {
				b.screen[b.cy][x] = ' '
			}
		case 2, 3:
			b.screen = makeGrid(b.cols, b.rows)
			if p0 == 3 {
				b.scrollback = nil
			}
		}
	case 'K':
		switch p0 {
		case 0:
			for x := b.cx; x < b.cols; x++ {
				b.screen[b.cy][x] = ' '
			}
		case 1:
			for x := 0; x <= b.cx && x < b.cols; x++ {
				b.screen[b.cy][x] = ' '
			}
		case 2:
			for x := 0; x < b.cols; x++ {
				b.screen[b.cy][x] = ' '
			}
		}
	case 'h':
		if private && containsParam(params, 1049) && !b.altScreen {
			b.mainSave = b.screen
			b.mainCX, b.mainCY = b.cx, b.cy
			b.altScreen = true
			b.screen = makeGrid(b.cols, b.rows)
			b.cx, b.cy = 0, 0
		}
	case 'l':
		if private && containsParam(params, 1049) && b.altScreen {
			b.altScreen = false
			if b.mainSave != nil {
				b.screen = b.mainSave
				b.mainSave = nil
			} else {
				b.screen = makeGrid(b.cols, b.rows)
			}
			b.cx = min(b.mainCX, b.cols-1)
			b.cy = min(b.mainCY, b.rows-1)
		}
	}
}

func parseCSIParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			out = append(out, 0)
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, v)
	}
	return out
}

func containsParam(params []int, target int) bool {
	for _, v := range params {
		if v == target {
			return true
		}
	}
	return false
}

// Resize updates buffer dimensions while preserving bottom-aligned screen content.
func (b *VTBuffer) Resize(cols, rows int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, r := normalizeSize(cols, rows)
	if c == b.cols && r == b.rows {
		return
	}
	next := makeGrid(c, r)
	copyRows := min(r, b.rows)
	for y := 0; y < copyRows; y++ {
		copyCols := min(c, len(b.screen[y]))
		copy(next[y], b.screen[y][:copyCols])
	}
	b.cols, b.rows = c, r
	b.screen = next
	b.cx = min(b.cx, c-1)
	b.cy = min(b.cy, r-1)
}

// CaptureText returns up to lastLines lines of plain terminal text (scrollback + active screen).
func (b *VTBuffer) CaptureText(lastLines int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lastLines <= 0 {
		lastLines = 200
	}

	var all []string
	if !b.altScreen {
		all = append(all, b.scrollback...)
	}
	lastNonEmpty := -1
	screenLines := make([]string, len(b.screen))
	for i, row := range b.screen {
		line := trimGridRow(row)
		screenLines[i] = line
		if line != "" || i <= b.cy {
			lastNonEmpty = i
		}
	}
	if lastNonEmpty >= 0 {
		all = append(all, screenLines[:lastNonEmpty+1]...)
	}
	if len(all) > lastLines {
		all = all[len(all)-lastLines:]
	}
	return strings.Join(all, "\n")
}

// Snapshot emits an ANSI replay frame restoring scrollback, active screen, and cursor position.
func (b *VTBuffer) Snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	var buf bytes.Buffer
	if b.altScreen {
		buf.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
		for y, row := range b.screen {
			if y > 0 {
				buf.WriteString("\r\n")
			}
			buf.WriteString(trimGridRow(row))
		}
	} else {
		buf.WriteString("\x1b[2J\x1b[H")
		sb := b.scrollback
		if len(sb) > maxSnapshotRows {
			sb = sb[len(sb)-maxSnapshotRows:]
		}
		for _, line := range sb {
			buf.WriteString(line)
			buf.WriteString("\r\n")
		}
		lastNonEmpty := -1
		for y, row := range b.screen {
			if trimGridRow(row) != "" || y <= b.cy {
				lastNonEmpty = y
			}
		}
		for y := 0; y <= lastNonEmpty; y++ {
			if y > 0 {
				buf.WriteString("\r\n")
			}
			buf.WriteString(trimGridRow(b.screen[y]))
		}
	}
	fmt.Fprintf(&buf, "\x1b[%d;%dH", b.cy+1, b.cx+1)
	return buf.Bytes()
}
