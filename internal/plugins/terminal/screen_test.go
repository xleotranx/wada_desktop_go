package terminal

import (
	"strings"
	"testing"
)

func TestScreenSkipsCharsetAndStringSequences(t *testing.T) {
	s := newScreen(40, 5)
	// tput sgr0 / ncurses reset, a DCS string, an APC string, ESC # 8 and
	// ESC % G must leave no text behind.
	s.Write([]byte("a\x1b(Bb\x1b)0c\x1bP1$r0m\x1b\\d\x1b_hidden\x1b\\e\x1b#8\x1b%Gf\x1b[0m"))
	if got := s.Line(0); got != "abcdef" {
		t.Fatalf("line = %q, want %q", got, "abcdef")
	}
}

func TestScreenSaveRestoreCursorAndReverseIndex(t *testing.T) {
	s := newScreen(10, 3)
	s.Write([]byte("ab\x1b7cd\x1b8X"))
	if got := s.Line(0); got != "abXd" {
		t.Fatalf("line = %q, want abXd", got)
	}
	s.Write([]byte("\x1b[1;1H\x1bMtop"))
	if got := s.Lines(); got[0] != "top" || got[1] != "abXd" {
		t.Fatalf("lines after reverse index = %q", got)
	}
}

func TestScreenWideRunesTakeTwoCells(t *testing.T) {
	s := newScreen(6, 2)
	s.Write([]byte("日本x"))
	if got := s.Line(0); got != "日本x" {
		t.Fatalf("line = %q", got)
	}
	if s.cx != 5 {
		t.Fatalf("cursor column = %d, want 5", s.cx)
	}
	// A wide rune that does not fit at the end of a row wraps whole.
	s.Write([]byte("語"))
	if got := s.Lines(); got[0] != "日本x" || got[1] != "語" {
		t.Fatalf("lines = %q", got)
	}
	// Overwriting half of a wide rune clears the other half.
	s.Write([]byte("\x1b[1;2Hz"))
	if got := s.Line(0); got != " z本x" {
		t.Fatalf("line after overwrite = %q", got)
	}
	// Combining marks take no cell.
	s.Write([]byte("\x1b[2J\x1b[1;1Hé!"))
	if got := s.Line(0); got != "e!" {
		t.Fatalf("line with combining mark = %q", got)
	}
}

func TestScreenLineAccessMatchesLines(t *testing.T) {
	s := newScreen(20, 3)
	for i := range 10 {
		s.Write([]byte("line " + strings.Repeat("x", i) + "\r\n"))
	}
	all := s.Lines()
	if s.LineCount() != len(all) {
		t.Fatalf("LineCount = %d, len(Lines) = %d", s.LineCount(), len(all))
	}
	for i := range all {
		if s.Line(i) != all[i] {
			t.Fatalf("Line(%d) = %q, Lines()[%d] = %q", i, s.Line(i), i, all[i])
		}
	}
	if all[0] != "line" || all[9] != "line xxxxxxxxx" {
		t.Fatalf("lines = %q", all)
	}
}

func TestScreenShrinkKeepsCursorRow(t *testing.T) {
	s := newScreen(20, 6)
	s.Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\n$ "))
	s.Resize(10, 2)
	lines := s.Lines()
	if got := strings.Join(lines, "|"); got != "one|two|three|four|$" {
		t.Fatalf("lines after shrink = %q", got)
	}
	if s.cy != 1 || s.Line(s.LineCount()-1) != "$" {
		t.Fatalf("cursor row = %d, last line %q", s.cy, s.Line(s.LineCount()-1))
	}
}

func TestFitSize(t *testing.T) {
	cols, rows := fitSize(435, 340, 6.9)
	if cols != 63 || rows != 19 {
		t.Fatalf("fitSize = %dx%d, want 63x19", cols, rows)
	}
	cols, rows = fitSize(10, 10, 6.9)
	if cols != minCols || rows != minRows {
		t.Fatalf("tiny fitSize = %dx%d, want minimums", cols, rows)
	}
}

func TestLeavingAltScreenFitsTheMainScreenToTheCurrentSize(t *testing.T) {
	for _, size := range [][2]int{{40, 20}, {120, 4}} {
		s := newScreen(80, 10)
		s.Write([]byte("hello\r\nworld"))
		s.Write([]byte("\x1b[?1049h"))
		s.Resize(size[0], size[1])
		s.Write([]byte("\x1b[?1049l"))
		if len(s.grid) != s.rows || len(s.grid[0]) != s.cols {
			t.Fatalf("%v: grid is %dx%d, screen %dx%d", size, len(s.grid[0]), len(s.grid), s.cols, s.rows)
		}
		for range 30 {
			s.Write([]byte("line\r\n"))
		}
		if got := s.Line(0); got != "hello" {
			t.Fatalf("%v: first line = %q, want hello", size, got)
		}
	}
}

func TestAltScreenKeepsTheSavedCursor(t *testing.T) {
	s := newScreen(80, 10)
	s.Write([]byte("\x1b[3;5H\x1b7")) // save the cursor at row 3, column 5
	s.Write([]byte("\x1b[8;1H"))      // move elsewhere
	s.Write([]byte("\x1b[?1049h\x1b[?1049l"))
	s.Write([]byte("\x1b8"))
	if s.cy != 2 || s.cx != 4 {
		t.Fatalf("restored cursor = (%d,%d), want (2,4)", s.cy, s.cx)
	}
}
