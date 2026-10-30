package chat

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// An answer that is arriving shows a word at a time, at an even pace that
// catches up with what came within revealCatchUp, as chat apps show replies
// rather than in the bursts a network delivers. It builds revealFPS frames a
// second while it has words to show, revealIdleFPS while the window is not in
// front: the view paces the text itself, and Markdown shows what it is given
// at once, since its own reveal builds a frame at every refresh of the
// display. Each frame lays out the paragraph that grows again, so frames are
// what an arriving answer costs.
const (
	revealFPS     = 30
	revealIdleFPS = 15
	revealCatchUp = 250 * time.Millisecond
	revealMinRate = 240 // bytes a second at least, so that a trickle does not crawl
)

var atOnce = markdown.Reveal{Unit: markdown.AtOnce}

// pace is how much of a row's text shows while it catches up with what came.
type pace struct {
	src   string // the text as it was at the last step
	shown int
	carry float64
	last  time.Time
}

// step moves on how much of text shows at now and reports whether more is
// left to show. Text that is not more of what showed (an edit, or a final
// answer the backend worded otherwise) shows at once.
func (s *pace) step(text string, now time.Time) bool {
	if s.shown > len(text) || !strings.HasPrefix(text, s.src[:s.shown]) {
		s.shown = len(text)
	}
	s.src = text
	dt := now.Sub(s.last)
	if s.last.IsZero() || dt > 100*time.Millisecond || dt < 0 {
		dt = time.Second / revealFPS
	}
	s.last = now
	backlog := len(text) - s.shown
	if backlog <= 0 {
		s.carry = 0
		return false
	}
	rate := max(float64(backlog)/revealCatchUp.Seconds(), revealMinRate)
	s.carry += rate * dt.Seconds()
	n := int(s.carry)
	if n == 0 {
		return true
	}
	s.carry -= float64(n)
	s.shown = wordEnd(text, min(s.shown+n, len(text)))
	return s.shown < len(text)
}

// wordEnd moves offset i of text on to the end of its word and the spaces
// after it.
func wordEnd(text string, i int) int {
	for i < len(text) && text[i] != ' ' && text[i] != '\n' {
		i++
	}
	for i < len(text) && (text[i] == ' ' || text[i] == '\n') {
		i++
	}
	for i < len(text) && !utf8.RuneStart(text[i]) {
		i++
	}
	return i
}

// pace steps the answers of the last turn that have words left to show and
// asks for the frame that shows the next ones. It returns whether that frame
// is due, so that text arriving meanwhile needs none of its own.
func (p *Plugin) pace(c *ui.Context) bool {
	reduce := c.Preferences().ReduceMotion
	due := false
	for i := len(p.t.rows) - 1; i >= 0 && p.t.rows[i].kind != rowUser; i-- {
		r := p.t.rows[i]
		switch {
		case r.kind == rowStream && r.reveal == nil && !reduce:
			r.reveal = &pace{}
		case r.reveal == nil:
			continue
		}
		if reduce || !r.reveal.step(r.text, c.Now()) && r.kind != rowStream {
			r.reveal = nil // caught up with a finished answer
			continue
		}
		due = due || r.reveal.shown < len(r.text)
	}
	if due {
		fps := revealFPS
		if !p.host.Foreground() {
			fps = revealIdleFPS
		}
		c.After(time.Second / time.Duration(fps))
	}
	return due
}

// shownText is as much of r's text as shows now.
func shownText(r *row) string {
	if r.reveal == nil {
		return r.text
	}
	return r.text[:r.reveal.shown]
}
