// Package terminal provides the interactive PTY terminal drawer for Wada.
package terminal

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxScreenScrollback = 2000

type parseState uint8

const (
	stateGround parseState = iota
	stateEsc
	stateCSI
	stateOSC
	stateOSCEsc
)

// screen is a pure-Go UI-thread ANSI/VT screen and scrollback model fed from
// reconnect snapshots and live PTY output bytes.
type screen struct {
	cols       int
	rows       int
	cx         int
	cy         int
	altScreen  bool
	grid       [][]rune
	savedGrid  [][]rune
	savedCX    int
	savedCY    int
	scrollback []string

	state   parseState
	csiBuf  strings.Builder
	utf8Buf []byte
}

func newScreen(cols, rows int) *screen {
	c, r := clampSize(cols, rows)
	return &screen{
		cols: c,
		rows: r,
		grid: makeRuneGrid(c, r),
	}
}

func clampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}

func makeRuneGrid(cols, rows int) [][]rune {
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

func (s *screen) Reset() {
	s.cx = 0
	s.cy = 0
	s.altScreen = false
	s.savedGrid = nil
	s.scrollback = nil
	s.state = stateGround
	s.csiBuf.Reset()
	s.utf8Buf = nil
	s.grid = makeRuneGrid(s.cols, s.rows)
}

func (s *screen) Resize(cols, rows int) {
	c, r := clampSize(cols, rows)
	if c == s.cols && r == s.rows {
		return
	}
	next := makeRuneGrid(c, r)
	copyRows := min(len(s.grid), r)
	for y := 0; y < copyRows; y++ {
		copyCols := min(len(s.grid[y]), c)
		copy(next[y][:copyCols], s.grid[y][:copyCols])
	}
	s.cols = c
	s.rows = r
	s.grid = next
	if s.cx >= c {
		s.cx = c - 1
	}
	if s.cy >= r {
		s.cy = r - 1
	}
}

func (s *screen) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	data := p
	if len(s.utf8Buf) > 0 {
		data = append(append([]byte{}, s.utf8Buf...), p...)
		s.utf8Buf = nil
	}
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(data) && len(data) < utf8.UTFMax {
			s.utf8Buf = append([]byte{}, data...)
			return
		}
		data = data[size:]
		s.step(r)
	}
}

func (s *screen) step(r rune) {
	switch s.state {
	case stateGround:
		switch r {
		case 0x1b:
			s.state = stateEsc
		case '\r':
			s.cx = 0
		case '\n', '\v', '\f':
			s.lineFeed()
		case '\b':
			if s.cx > 0 {
				s.cx--
			}
		case '\t':
			next := ((s.cx / 8) + 1) * 8
			if next >= s.cols {
				next = s.cols - 1
			}
			s.cx = next
		default:
			if r >= 0x20 {
				s.putRune(r)
			}
		}
	case stateEsc:
		switch r {
		case '[':
			s.csiBuf.Reset()
			s.state = stateCSI
		case ']':
			s.state = stateOSC
		case 'c':
			s.Reset()
		default:
			s.state = stateGround
		}
	case stateCSI:
		if r >= 0x40 && r <= 0x7e {
			s.handleCSI(s.csiBuf.String(), r)
			s.csiBuf.Reset()
			s.state = stateGround
		} else {
			s.csiBuf.WriteRune(r)
		}
	case stateOSC:
		switch r {
		case 0x07:
			s.state = stateGround
		case 0x1b:
			s.state = stateOSCEsc
		}
	case stateOSCEsc:
		if r == '\\' {
			s.state = stateGround
		} else {
			s.state = stateOSC
		}
	}
}

func (s *screen) putRune(r rune) {
	if s.cx >= s.cols {
		s.cx = 0
		s.lineFeed()
	}
	if s.cy >= 0 && s.cy < s.rows && s.cx >= 0 && s.cx < s.cols {
		s.grid[s.cy][s.cx] = r
	}
	s.cx++
}

func (s *screen) lineFeed() {
	if s.cy < s.rows-1 {
		s.cy++
		return
	}
	if !s.altScreen && len(s.grid) > 0 {
		s.scrollback = append(s.scrollback, trimSpacesRight(s.grid[0]))
		if len(s.scrollback) > maxScreenScrollback {
			s.scrollback = s.scrollback[len(s.scrollback)-maxScreenScrollback:]
		}
	}
	copy(s.grid, s.grid[1:])
	blank := make([]rune, s.cols)
	for i := range blank {
		blank[i] = ' '
	}
	s.grid[s.rows-1] = blank
}

func (s *screen) handleCSI(params string, cmd rune) {
	switch cmd {
	case 'A':
		s.cy = max(0, s.cy-csiParam(params, 0, 1))
	case 'B':
		s.cy = min(s.rows-1, s.cy+csiParam(params, 0, 1))
	case 'C':
		s.cx = min(s.cols-1, s.cx+csiParam(params, 0, 1))
	case 'D':
		s.cx = max(0, s.cx-csiParam(params, 0, 1))
	case 'G':
		s.cx = clampInt(csiParam(params, 0, 1)-1, 0, s.cols-1)
	case 'H', 'f':
		row := clampInt(csiParam(params, 0, 1)-1, 0, s.rows-1)
		col := clampInt(csiParam(params, 1, 1)-1, 0, s.cols-1)
		s.cy, s.cx = row, col
	case 'J':
		switch csiParam(params, 0, 0) {
		case 0:
			clearRowFrom(s.grid[s.cy], s.cx)
			for y := s.cy + 1; y < s.rows; y++ {
				clearRowFrom(s.grid[y], 0)
			}
		case 1:
			for y := 0; y < s.cy; y++ {
				clearRowFrom(s.grid[y], 0)
			}
			clearRowRange(s.grid[s.cy], 0, min(s.cx+1, s.cols))
		case 2, 3:
			for y := 0; y < s.rows; y++ {
				clearRowFrom(s.grid[y], 0)
			}
			if csiParam(params, 0, 0) == 3 {
				s.scrollback = nil
			}
		}
	case 'K':
		switch csiParam(params, 0, 0) {
		case 0:
			clearRowFrom(s.grid[s.cy], s.cx)
		case 1:
			clearRowRange(s.grid[s.cy], 0, min(s.cx+1, s.cols))
		case 2:
			clearRowFrom(s.grid[s.cy], 0)
		}
	case 'h':
		if params == "?1049" && !s.altScreen {
			s.savedGrid = s.grid
			s.savedCX, s.savedCY = s.cx, s.cy
			s.grid = makeRuneGrid(s.cols, s.rows)
			s.cx, s.cy = 0, 0
			s.altScreen = true
		}
	case 'l':
		if params == "?1049" && s.altScreen {
			if s.savedGrid != nil {
				s.grid = s.savedGrid
				s.Resize(s.cols, s.rows)
				s.cx, s.cy = s.savedCX, s.savedCY
			} else {
				s.grid = makeRuneGrid(s.cols, s.rows)
				s.cx, s.cy = 0, 0
			}
			s.savedGrid = nil
			s.altScreen = false
		}
	}
}

func (s *screen) Lines() []string {
	var all []string
	if !s.altScreen && len(s.scrollback) > 0 {
		all = append(all, s.scrollback...)
	}
	screenLines := make([]string, len(s.grid))
	lastNonEmpty := -1
	for i, row := range s.grid {
		line := trimSpacesRight(row)
		screenLines[i] = line
		if line != "" || (len(all) == 0 && i == s.cy) {
			lastNonEmpty = i
		}
	}
	if lastNonEmpty >= 0 {
		all = append(all, screenLines[:lastNonEmpty+1]...)
	}
	if len(all) == 0 {
		return []string{""}
	}
	return all
}

func (s *screen) PlainText() string {
	return strings.Join(s.Lines(), "\n")
}

func csiParam(params string, idx, def int) int {
	clean := strings.TrimPrefix(params, "?")
	clean = strings.TrimPrefix(clean, ">")
	parts := strings.Split(clean, ";")
	if idx >= len(parts) || parts[idx] == "" {
		return def
	}
	v, err := strconv.Atoi(parts[idx])
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clearRowFrom(row []rune, start int) {
	clearRowRange(row, start, len(row))
}

func clearRowRange(row []rune, start, end int) {
	for i := max(0, start); i < min(len(row), end); i++ {
		row[i] = ' '
	}
}

func trimSpacesRight(row []rune) string {
	return strings.TrimRight(string(row), " ")
}
