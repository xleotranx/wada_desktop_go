package design

import (
	"context"
	_ "embed"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// probeScript describes the element at x, y of the page, which the
// preamble declares: its selector, words and markup.
//
//go:embed probe.js
var probeScript string

type markKind uint8

const (
	commentMark markKind = iota // on an element of an artboard
	noteMark                    // on the canvas
)

// mark is a comment or a note. A comment's X, Y are page pixels in its
// artboard; a note's are page pixels on the canvas.
type mark struct {
	ID        int
	Kind      markKind
	Path      string // the comment's artboard
	Selector  string
	Element   string // the element's words, cut short
	HTML      string
	X, Y      float32
	Words     string
	Added     bool // already copied to a message draft
	Resolved  bool
	Unlocated bool `json:"-"` // the target is loading, missing or hidden in this viewport
}

// element is what the page says is at a point.
type element struct {
	Selector, Text, HTML string
	X, Y, W, H           float32
}

// comment asks the page under x, y what element is there and opens a card
// to comment on it. Off the artboards, it leaves a note instead.
func (p *Plugin) comment(x, y float32) {
	a, ok := p.boardAt(x, y)
	if !ok {
		p.addNote(x, y)
		return
	}
	b := p.boards[a.Path]
	if b == nil {
		return
	}
	k := p.vp.zoom()
	ax, ay, _, _ := p.vp.screen(a)
	px, py := (x-ax)/k, (y-ay)/k
	web, path := b.web, a.Path
	kernel.Go(p.navigation, &p.probing, func(ctx context.Context) (element, error) {
		return probeElement(ctx, web, px, py)
	}, func(el element, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The element was not found", Description: err.Error(), Type: components.ToastError})
			return
		}
		if el.Selector == "" || p.boards[path] == nil {
			return
		}
		p.closeCard()
		p.nextID++
		p.marks = append(p.marks, &mark{ID: p.nextID, Kind: commentMark, Path: path, Selector: el.Selector,
			Element: el.Text, HTML: el.HTML, X: px, Y: py})
		p.selectBoard(path)
		p.edit(p.nextID)
	})
}

// addNote puts a note on the canvas at x, y and opens it.
func (p *Plugin) addNote(x, y float32) {
	p.closeCard()
	k := p.vp.zoom()
	p.nextID++
	p.marks = append(p.marks, &mark{ID: p.nextID, Kind: noteMark, X: (x - p.vp.X) / k, Y: (y - p.vp.Y) / k})
	p.edit(p.nextID)
}

func (p *Plugin) mark(id int) *mark {
	for _, m := range p.marks {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// edit opens the card of a mark.
func (p *Plugin) edit(id int) {
	if p.editing != id {
		p.editing = id // closing the card before keeps this mark, empty as it may be
		p.marks = deleteFunc(p.marks, func(m *mark) bool { return m.Words == "" && m.ID != id })
	}
	m := p.mark(id)
	if m == nil {
		p.editing = 0
		return
	}
	p.text, p.cardOn = m.Words, true
}

// saveCard keeps the card's words; a mark left without words goes.
func (p *Plugin) saveCard() {
	if m := p.mark(p.editing); m != nil {
		words := strings.TrimSpace(p.text)
		if words != m.Words {
			m.Added = false
		}
		m.Words = words
	}
	p.editing, p.cardOn = 0, false
	p.prune()
	p.saveMarks()
}

// closeCard closes the card without keeping its words.
func (p *Plugin) closeCard() {
	p.editing, p.cardOn = 0, false
	p.prune()
}

func (p *Plugin) deleteMark(id int) {
	p.marks = deleteFunc(p.marks, func(m *mark) bool { return m.ID == id })
	if p.editing == id {
		p.editing, p.cardOn = 0, false
	}
	p.saveMarks()
}

func (p *Plugin) resolveMark(id int) {
	if p.editing == id {
		p.saveCard()
	}
	if m := p.mark(id); m != nil {
		m.Resolved = !m.Resolved
		if !m.Resolved {
			m.Added = false
		}
		p.saveMarks()
	}
	p.closeCard()
}

func (p *Plugin) prune() {
	p.marks = deleteFunc(p.marks, func(m *mark) bool { return m.Words == "" && m.ID != p.editing })
}

func deleteFunc(marks []*mark, del func(*mark) bool) []*mark {
	out := marks[:0]
	for _, m := range marks {
		if !del(m) {
			out = append(out, m)
		}
	}
	clear(marks[len(out):])
	return out
}

// number is how the canvas and the message count a mark.
func (p *Plugin) number(id int) int { return id }

func (m *mark) status() string {
	if m.Resolved {
		return "Resolved"
	}
	if m.Added {
		return "Added to message"
	}
	return "Draft"
}

// pending is the comments and notes with words.
func (p *Plugin) pending() []*mark {
	var out []*mark
	for _, m := range p.marks {
		if m.Words != "" && !m.Added && !m.Resolved {
			out = append(out, m)
		}
	}
	return out
}

// sendMarks adds the comments and notes to the composer, numbered as on
// the canvas, and keeps their pins. Nothing is sent.
func (p *Plugin) sendMarks() {
	p.saveCard()
	marks := p.pending()
	if len(marks) == 0 {
		return
	}
	p.addFeedbackToDraft(marks)
}

func (p *Plugin) addFeedbackToDraft(marks []*mark) {
	p.chat.SetDraft(appendDraft(p.chat.DraftText(), describe(p.project, marks)))
	for _, m := range marks {
		m.Added = true
	}
	p.saveMarks()
}

// addMarkAgain lets a user recover feedback after replacing or discarding a draft.
func (p *Plugin) addMarkAgain(id int) {
	if p.editing == id {
		p.saveCard()
	}
	if m := p.mark(id); m != nil && m.Words != "" && !m.Resolved {
		p.addFeedbackToDraft([]*mark{m})
	}
}

// describe words comments and notes for the agent.
func describe(folder string, marks []*mark) string {
	var b strings.Builder
	b.WriteString("Comments on the design:\n")
	for _, m := range marks {
		b.WriteString("\n" + strconv.Itoa(m.ID) + ". ")
		switch m.Kind {
		case commentMark:
			b.WriteString("In " + rel(folder, m.Path) + ", `" + m.Selector + "`")
			if m.Element != "" {
				b.WriteString(" (“" + m.Element + "”)")
			}
			b.WriteString(": " + m.Words)
		case noteMark:
			b.WriteString("Note: " + m.Words)
		}
	}
	return b.String()
}

// appendDraft adds text to what the user has typed, after a blank line,
// leaving one for their words.
func appendDraft(draft, text string) string {
	if d := strings.TrimRight(draft, " \n"); d != "" {
		return d + "\n\n" + text + "\n\n"
	}
	return text + "\n\n"
}
