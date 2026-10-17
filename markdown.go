package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// mdPiece is one piece an answer reads as: a paragraph, a heading, a
// list item or a fenced block of code.
type mdPiece struct {
	kind  string // "text", "head", "code" or "list"
	text  string // the contents, lines joined with \n for paragraphs
	depth int    // a heading's level, 1 to 6
	num   string // a list item's marker: "" for a bullet, else "1."
}

// parseMarkdown cuts an answer into the pieces the view draws. Fences
// keep their lines verbatim, short of the fence marks; a fence without
// its close, as a landing answer still streams, runs to the end. Lists
// keep to one level.
func parseMarkdown(s string) []mdPiece {
	out := []mdPiece{}
	para := []string{}
	flush := func() {
		if len(para) > 0 {
			out = append(out, mdPiece{kind: "text", text: strings.Join(para, "\n")})
			para = nil
		}
	}
	fence := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = false
				continue
			}
			// Keep the code's own indenting as it was written.
			if len(out) > 0 && out[len(out)-1].kind == "code" && out[len(out)-1].text != "" {
				out[len(out)-1].text += "\n" + line
			} else if len(out) > 0 && out[len(out)-1].kind == "code" {
				out[len(out)-1].text = line
			} else {
				out = append(out, mdPiece{kind: "code", text: line})
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			flush()
			fence = true
			out = append(out, mdPiece{kind: "code"})
		case headingLevel(trimmed) > 0:
			flush()
			n := headingLevel(trimmed)
			out = append(out, mdPiece{kind: "head",
				depth: n, text: strings.TrimSpace(trimmed[n:])})
		case isItem(trimmed):
			flush()
			out = append(out, mdPiece{kind: "list", num: itemMark(trimmed),
				text: strings.TrimSpace(trimmed[itemWidth(trimmed):])})
		case trimmed == "":
			flush()
		default:
			para = append(para, trimmed)
		}
	}
	flush()
	return out
}

// headingLevel counts a line's opening hashes, from 1 to 6, or 0 when it
// begins with none.
func headingLevel(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		return n
	}
	return 0
}

// isItem reads a list entry: a bullet's dash or a number with a dot.
func isItem(s string) bool {
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") ||
		strings.HasPrefix(s, "+ ") || itemOrdered(s) != ""
}

// itemMark is the entry's marker: "" for a bullet, the number's "2." as
// it was written.
func itemMark(s string) string {
	if itemOrdered(s) != "" {
		return itemOrdered(s)
	}
	return ""
}

// itemOrdered is the "2." at a line's head when the entry is numbered.
func itemOrdered(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') &&
		i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
		return s[:i+1]
	}
	return ""
}

// itemWidth is how much a line's marker takes, so the item's words
// follow it.
func itemWidth(s string) int {
	for _, pre := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, pre) {
			return len(pre)
		}
	}
	if n := itemOrdered(s); n != "" {
		return len(n) + 1
	}
	return 0
}

// richMarkdown draws one answer piecewise: headings, fenced code, list
// items and their inline emphasis, as the agent set them down.
func (a *app) richMarkdown(c *ui.Context, p palette, s string) {
	for i, piece := range parseMarkdown(s) {
		switch piece.kind {
		case "code":
			if piece.text == "" {
				continue // an open fence drew nothing yet
			}
			c.Key("md:" + strconv.Itoa(i))
			ui.Box(c).FillWidth().Radius(10).Padding(10, 12).Margin(2, 0).
				Background(p.detailBg).Border(1, p.hairline).Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					for _, l := range strings.Split(piece.text, "\n") {
						ui.Text(c, l).Font("monospace").FontSize(12.5).TextColor(p.text)
					}
				})
			})
		case "head":
			c.Key("md:" + strconv.Itoa(i))
			size, weight := float32(15.5), 600
			switch {
			case piece.depth <= 1:
				size, weight = 17.5, 700
			case piece.depth == 2:
				size, weight = 16, 700
			}
			ui.RichText(c, mdSpans(piece.text, p)...).
				FontSize(size).FontWeight(weight).LineHeight(1.4).TextColor(p.text).FillWidth()
		case "list":
			c.Key("md:" + strconv.Itoa(i))
			bullet := "•"
			if piece.num != "" {
				bullet = piece.num
			}
			spans := append([]ui.Span{{Text: bullet + "  ", Color: p.muted}},
				mdSpans(piece.text, p)...)
			ui.RichText(c, spans...).
				FontSize(14).LineHeight(1.45).TextColor(p.text).FillWidth()
		default: // "text"
			c.Key("md:" + strconv.Itoa(i))
			ui.RichText(c, mdSpans(piece.text, p)...).
				FontSize(14).LineHeight(1.45).TextColor(p.text).FillWidth()
		}
	}
}

// mdSpans turns one line (or paragraph's lines, breaks at the newlines
// kept) into styled runs: bold, italic, code and underlined link text,
// the markers themselves going away.
func mdSpans(s string, p palette) []ui.Span {
	out := []ui.Span{}
	run := strings.Builder{}
	drop := func() {
		if run.Len() > 0 {
			out = append(out, ui.Span{Text: run.String()})
			run.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "**"):
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				drop()
				out = append(out, ui.Span{Text: s[i+2 : i+2+end], Weight: 600})
				i += end + 4
				continue
			}
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				drop()
				out = append(out, ui.Span{Text: s[i+1 : i+1+end],
					Font: "monospace", Background: p.detailBg, Radius: 4})
				i += end + 2
				continue
			}
		case s[i] == '*' || s[i] == '_':
			if end := strings.IndexByte(s[i+1:], s[i]); end >= 0 {
				drop()
				out = append(out, ui.Span{Text: s[i+1 : i+1+end], Italic: true})
				i += end + 2
				continue
			}
		case s[i] == '[':
			if end := strings.Index(s[i:], "]("); end >= 0 {
				if url := strings.Index(s[i+end+2:], ")"); url >= 0 {
					drop()
					out = append(out, ui.Span{Text: s[i+1 : i+end],
						Underline: true, Color: p.link})
					i += end + 2 + url + 1
					continue
				}
			}
		}
		run.WriteByte(s[i])
		i++
	}
	drop()
	return out
}
