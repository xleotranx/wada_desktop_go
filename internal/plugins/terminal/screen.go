// Package terminal provides the interactive PTY terminal drawer for Wada.
package terminal

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxScreenScrollback = 2000

// wideTail fills the cell after a double-width rune; lines skip it.
const wideTail rune = -1

type parseState uint8

const (
	stateGround parseState = iota
	stateEsc
	stateCSI
	stateEscInter
	stateOSC
	stateOSCEsc
)

// screen is a pure-Go UI-thread ANSI/VT screen and scrollback model fed from
// reconnect snapshots and live PTY output bytes.
type screen struct {
	cols      int
	rows      int
	cx        int
	cy        int
	altScreen bool
	grid      [][]rune
	// savedGrid and altCX/altCY are the main screen while the alternate
	// screen is shown; savedCX/savedCY are the cursor ESC 7 saved.
	savedGrid  [][]rune
	altCX      int
	altCY      int
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
	s.refit(c, r)
}

// refit fits the grid, whatever its current shape, and the cursor to c x r.
func (s *screen) refit(c, r int) {
	// Keep the cursor's row on screen: rows above it scroll into history.
	if r < len(s.grid) && s.cy >= r {
		drop := s.cy - r + 1
		if !s.altScreen {
			for y := 0; y < drop; y++ {
				s.pushScrollback(s.grid[y])
			}
		}
		s.grid = s.grid[drop:]
		s.cy -= drop
	}
	next := makeRuneGrid(c, r)
	copyRows := min(len(s.grid), r)
	for y := 0; y < copyRows; y++ {
		copyCols := min(len(s.grid[y]), c)
		copy(next[y][:copyCols], s.grid[y][:copyCols])
		if copyCols > 0 && copyCols < len(s.grid[y]) && s.grid[y][copyCols] == wideTail {
			next[y][copyCols-1] = ' ' // its second half was cut off
		}
	}
	s.cols = c
	s.rows = r
	s.grid = next
	s.cx = clampInt(s.cx, 0, c-1)
	s.cy = clampInt(s.cy, 0, r-1)
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
		s.state = stateGround
		switch r {
		case '[':
			s.csiBuf.Reset()
			s.state = stateCSI
		case ']', 'P', 'X', '^', '_':
			// OSC, DCS, SOS, PM and APC strings run to BEL or ST unprinted.
			s.state = stateOSC
		case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ':
			// Charset designators and the like take one more byte.
			s.state = stateEscInter
		case 'c':
			s.Reset()
		case '7':
			s.savedCX, s.savedCY = s.cx, s.cy
		case '8':
			s.cx, s.cy = clampInt(s.savedCX, 0, s.cols-1), clampInt(s.savedCY, 0, s.rows-1)
		case 'D':
			s.lineFeed()
		case 'E':
			s.cx = 0
			s.lineFeed()
		case 'M':
			s.reverseIndex()
		case 0x1b:
			s.state = stateEsc
		}
	case stateEscInter:
		s.state = stateGround
	case stateCSI:
		switch {
		case r >= 0x40 && r <= 0x7e:
			s.handleCSI(s.csiBuf.String(), r)
			s.csiBuf.Reset()
			s.state = stateGround
		case r == 0x1b:
			s.csiBuf.Reset()
			s.state = stateEsc
		case r == 0x18 || r == 0x1a: // CAN and SUB abort the sequence
			s.csiBuf.Reset()
			s.state = stateGround
		case r >= 0x20:
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
	w := runeWidth(r)
	if w == 0 {
		return
	}
	if w > s.cols {
		w = 1
	}
	if s.cx+w > s.cols {
		s.cx = 0
		s.lineFeed()
	}
	if s.cy >= 0 && s.cy < s.rows && s.cx >= 0 && s.cx+w <= s.cols {
		row := s.grid[s.cy]
		if row[s.cx] == wideTail && s.cx > 0 {
			row[s.cx-1] = ' ' // overwrote the second half of a wide rune
		}
		row[s.cx] = r
		if w == 2 {
			row[s.cx+1] = wideTail
		} else if s.cx+1 < s.cols && row[s.cx+1] == wideTail {
			row[s.cx+1] = ' '
		}
	}
	s.cx += w
}

func (s *screen) reverseIndex() {
	if s.cy > 0 {
		s.cy--
		return
	}
	copy(s.grid[1:], s.grid[:s.rows-1])
	s.grid[0] = blankRow(s.cols)
}

func (s *screen) pushScrollback(row []rune) {
	s.scrollback = append(s.scrollback, rowText(row))
	if len(s.scrollback) > maxScreenScrollback {
		s.scrollback = s.scrollback[len(s.scrollback)-maxScreenScrollback:]
	}
}

func (s *screen) lineFeed() {
	if s.cy < s.rows-1 {
		s.cy++
		return
	}
	if !s.altScreen && len(s.grid) > 0 {
		s.pushScrollback(s.grid[0])
	}
	copy(s.grid, s.grid[1:])
	s.grid[s.rows-1] = blankRow(s.cols)
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
			s.altCX, s.altCY = s.cx, s.cy
			s.grid = makeRuneGrid(s.cols, s.rows)
			s.cx, s.cy = 0, 0
			s.altScreen = true
		}
	case 'l':
		if params == "?1049" && s.altScreen {
			s.altScreen = false
			if s.savedGrid != nil {
				// The main screen keeps the size it had when the alternate
				// one opened; fit it to the size the screen has now.
				s.grid = s.savedGrid
				s.cx, s.cy = s.altCX, s.altCY
				s.refit(s.cols, s.rows)
			} else {
				s.grid = makeRuneGrid(s.cols, s.rows)
				s.cx, s.cy = 0, 0
			}
			s.savedGrid = nil
		}
	}
}

// LineCount is the number of lines Line serves: the scrollback, then the
// screen down to its last non-blank row (or the cursor's, with no history).
func (s *screen) LineCount() int {
	n := s.historyLen()
	last := -1
	for y := len(s.grid) - 1; y >= 0; y-- {
		if !rowBlank(s.grid[y]) {
			last = y
			break
		}
	}
	if n == 0 && s.cy > last {
		last = s.cy
	}
	return max(1, n+last+1)
}

// Line is line i of LineCount, without trailing spaces. It copies nothing
// but that line, so a view can draw just the rows it shows.
func (s *screen) Line(i int) string {
	n := s.historyLen()
	if i < n {
		return s.scrollback[i]
	}
	if y := i - n; y >= 0 && y < len(s.grid) {
		return rowText(s.grid[y])
	}
	return ""
}

func (s *screen) historyLen() int {
	if s.altScreen {
		return 0
	}
	return len(s.scrollback)
}

func (s *screen) Lines() []string {
	n := s.LineCount()
	all := make([]string, n)
	for i := range all {
		all[i] = s.Line(i)
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

func blankRow(cols int) []rune {
	row := make([]rune, cols)
	for i := range row {
		row[i] = ' '
	}
	return row
}

func rowBlank(row []rune) bool {
	for _, r := range row {
		if r != ' ' && r != wideTail {
			return false
		}
	}
	return true
}

// rowText is a row's text without wide-rune tails or trailing spaces.
func rowText(row []rune) string {
	end := len(row)
	for end > 0 && (row[end-1] == ' ' || row[end-1] == wideTail) {
		end--
	}
	var b strings.Builder
	b.Grow(end)
	for _, r := range row[:end] {
		if r != wideTail {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// runeWidth is the number of cells r takes: 0 for combining and format
// characters, 2 for East Asian wide and fullwidth characters and emoji.
func runeWidth(r rune) int {
	switch {
	case r < 0x300:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	}
	for _, rg := range wideRanges {
		if r < rg[0] {
			return 1
		}
		if r <= rg[1] {
			return 2
		}
	}
	return 1
}

// wideRanges are the main wide and fullwidth blocks, in order.
var wideRanges = [...][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo initials
	{0x231A, 0x231B},   // watch, hourglass
	{0x23E9, 0x23F3},   // media symbols
	{0x25FD, 0x25FE},   // small squares
	{0x2614, 0x2615},   // umbrella, hot beverage
	{0x2648, 0x2653},   // zodiac
	{0x26A1, 0x26A1},   // high voltage
	{0x26AA, 0x26AB},   // circles
	{0x26BD, 0x26BE},   // balls
	{0x26C4, 0x26C5},   // snowman, sun
	{0x26D4, 0x26D4},   // no entry
	{0x26EA, 0x26EA},   // church
	{0x26F2, 0x26F5},   // fountain..sailboat
	{0x26FA, 0x26FD},   // tent..fuel pump
	{0x2705, 0x2705},   // check mark
	{0x270A, 0x270B},   // fists
	{0x2728, 0x2728},   // sparkles
	{0x274C, 0x274C},   // cross mark
	{0x2753, 0x2755},   // question marks
	{0x2757, 0x2757},   // exclamation
	{0x2795, 0x2797},   // plus, minus, divide
	{0x27B0, 0x27B0},   // curly loop
	{0x2B1B, 0x2B1C},   // large squares
	{0x2B50, 0x2B50},   // star
	{0x2B55, 0x2B55},   // circle
	{0x2E80, 0x303E},   // CJK radicals .. CJK symbols
	{0x3041, 0x33FF},   // kana .. CJK compatibility
	{0x3400, 0x4DBF},   // CJK extension A
	{0x4E00, 0x9FFF},   // CJK unified ideographs
	{0xA000, 0xA4CF},   // Yi
	{0xA960, 0xA97F},   // Hangul Jamo extended A
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE10, 0xFE19},   // vertical forms
	{0xFE30, 0xFE6F},   // CJK compatibility forms, small forms
	{0xFF00, 0xFF60},   // fullwidth forms
	{0xFFE0, 0xFFE6},   // fullwidth signs
	{0x16FE0, 0x18AFF}, // ideographic symbols, Tangut
	{0x1B000, 0x1B2FF}, // kana supplement
	{0x1F004, 0x1F004}, // mahjong
	{0x1F0CF, 0x1F0CF}, // joker
	{0x1F18E, 0x1F18E}, // AB button
	{0x1F191, 0x1F19A}, // squared words
	{0x1F200, 0x1F251}, // enclosed ideographs
	{0x1F300, 0x1F64F}, // pictographs, emoticons
	{0x1F680, 0x1F6FF}, // transport
	{0x1F7E0, 0x1F7EB}, // colored circles and squares
	{0x1F90C, 0x1F9FF}, // supplemental pictographs
	{0x1FA70, 0x1FAFF}, // symbols and pictographs extended A
	{0x20000, 0x3FFFD}, // CJK extensions B and later
}
