package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestParseMarkdown cuts an answer the way the view draws it: fences
// verbatim, headings leveled, lists marked, paragraphs joined.
func TestParseMarkdown(t *testing.T) {
	s := "A short intro.\n\nTurn one went fine:\n```sh\ngo test ./...\nok\n```\n\n### Notes\n\n- first thing\n10. the fiftieth\n\nand the tail."
	got := parseMarkdown(s)
	want := []mdPiece{
		{kind: "text", text: "A short intro."},
		{kind: "text", text: "Turn one went fine:"},
		{kind: "code", text: "go test ./...\nok"},
		{kind: "head", depth: 3, text: "Notes"},
		{kind: "list", text: "first thing"},
		{kind: "list", num: "10.", text: "the fiftieth"},
		{kind: "text", text: "and the tail."},
	}
	if len(got) != len(want) {
		t.Fatalf("pieces %+v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("piece %d: %+v, wanted %+v", i, got[i], want[i])
		}
	}
}

// TestParseMarkdownOpenFence keeps an unterminated fence, as a still
// streaming answer carries, up to its last line.
func TestParseMarkdownOpenFence(t *testing.T) {
	got := parseMarkdown("here comes code:\n```go\nx := 1")
	if len(got) != 2 || got[1].kind != "code" || got[1].text != "x := 1" {
		t.Fatalf("pieces %+v", got)
	}
	// An empty open fence Held its piece, and the view draws nothing
	// for it yet.
	got = parseMarkdown("code:\n```")
	if len(got) != 2 || got[1].kind != "code" || got[1].text != "" {
		t.Fatalf("pieces %+v", got)
	}
}

// TestMdSpans strips the markers and keeps the words: bold, italic,
// code and links come out as their labelled run.
func TestMdSpans(t *testing.T) {
	p := palette{detailBg: ui.Hex("#efefef"), muted: ui.Hex("#8e8e8e"),
		link: ui.Hex("#3b82f6")}
	spans := mdSpans("Say **bold** and `code` too, *soft* and [a link](https://x.y).", p)
	var b strings.Builder
	for i, span := range spans {
		b.WriteString(span.Text)
		_ = i
	}
	want := "Say bold and code too, soft and a link."
	if b.String() != want {
		t.Fatalf("read %q, wanted %q", b.String(), want)
	}
	style := map[string]int{"bold": 0, "code": 0, "soft": 0, "a link": 0}
	for _, span := range spans {
		switch span.Text {
		case "bold":
			if span.Weight != 600 {
				t.Fatalf("bold weight %+v", span)
			}
			style["bold"]++
		case "code":
			if span.Font != "monospace" || span.Radius == 0 {
				t.Fatalf("code style %+v", span)
			}
			style["code"]++
		case "soft":
			if !span.Italic {
				t.Fatalf("italic style %+v", span)
			}
			style["soft"]++
		case "a link":
			if !span.Underline {
				t.Fatalf("link style %+v", span)
			}
			style["a link"]++
		}
	}
	for k, n := range style {
		if n != 1 {
			t.Fatalf("span %q seen %d times", k, n)
		}
	}
	// Unmatched markers stay as plain words: the agent may write math.
	plain := mdSpans("a * b is 2", p)
	if len(plain) != 1 || plain[0].Text != "a * b is 2" {
		t.Fatalf("plain runs %+v", plain)
	}
}

// TestMarkdownView drives the renderer: markers vanish, the pieces land
// in order and the stream preview stays plain.
func TestMarkdownView(t *testing.T) {
	a := newApp()
	a.selectedSession = "ses_seed"
	a.selected = "Turned out"
	a.blocks = []block{
		{kind: "text", text: "## Welcome\n\nRun `go test` and **be happy**:\n\n```sh\ncd .\nls\n```\n\n- bullets first\n\n7. then numbers"},
		{kind: "stream", text: "rough answer draft with `no rendering`"},
	}
	tt := ui.NewTester(a.view, 1400, 920)

	for _, keep := range []string{"Welcome", "go test", "be happy", "cd .", "ls", "bullets first", "then numbers"} {
		if !tt.HasText(keep) {
			t.Fatalf("piece %q missing: %v", keep, tt.Texts())
		}
	}
	for _, gone := range []string{"##", "**"} {
		if tt.HasText(gone) {
			t.Fatalf("marker %q left visible: %v", gone, tt.Texts())
		}
	}
	// The in-flight preview still reads as plain words with their markers,
	// the only "`" left anywhere.
	if !tt.HasText("rough answer draft with `no rendering`") {
		t.Fatalf("preview lost: %v", tt.Texts())
	}
	if n := strings.Count(strings.Join(tt.Texts(), "|"), "`"); n != 2 {
		t.Fatalf("%d backticks left visible: %v", n, tt.Texts())
	}
}
