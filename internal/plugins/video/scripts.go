package video

import (
	"regexp"
	"sort"
	"strings"
)

// script is one inline script of a file, with where it sits.
type script struct {
	text string
	at   span
	line int
}

// scripts returns the inline scripts of src, in source order. A script with a
// src is not included: what it runs is not in this file.
func scripts(src string) []*script {
	doc, err := scanHTML(src)
	if err != nil {
		return nil
	}
	var out []*script
	doc.walk(func(n *element) bool {
		if n.Tag != "script" || n.has("src") {
			return true
		}
		if n.Inner.End > len(src) || n.Inner.Start > n.Inner.End {
			return true
		}
		out = append(out, &script{text: src[n.Inner.Start:n.Inner.End], at: n.Inner, line: lineOf(src, n.Inner.Start)})
		return true
	})
	return out
}

// timelineRegister matches `window.__timelines["main"] = tl`.
var timelineRegister = regexp.MustCompile(`__timelines\s*\[\s*["']([^"']+)["']\s*\]\s*=`)

// timelineObject matches a whole registry written as `window.__timelines = { main: tl }`.
var timelineObject = regexp.MustCompile(`__timelines\s*=\s*\{([^}]*)\}`)

// keyInObject matches the keys of a timeline object literal.
var keyInObject = regexp.MustCompile(`(?:^|[,{])\s*["']?([A-Za-z_$][\w$-]*)["']?\s*:`)

// registeredTimelines lists the composition ids a file registers a timeline
// for, as the runtime reads them.
func registeredTimelines(src string) []string {
	seen := map[string]bool{}
	for _, s := range scripts(src) {
		for _, m := range timelineRegister.FindAllStringSubmatch(s.text, -1) {
			seen[m[1]] = true
		}
		for _, m := range timelineObject.FindAllStringSubmatch(s.text, -1) {
			for _, k := range keyInObject.FindAllStringSubmatch(m[1], -1) {
				seen[k[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// stripStrings blanks the quoted text of JavaScript, so a word in a message
// is not read as code.
func stripStrings(js string) string {
	var b strings.Builder
	b.Grow(len(js))
	for i := 0; i < len(js); i++ {
		switch q := js[i]; q {
		case '"', '\'', '`':
			b.WriteByte(q)
			for i++; i < len(js); i++ {
				if js[i] == '\\' && i+1 < len(js) {
					i++
					continue
				}
				if js[i] == q {
					break
				}
			}
			b.WriteByte(q)
		default:
			b.WriteByte(js[i])
		}
	}
	return b.String()
}

// stripComments blanks JavaScript comments, keeping their newlines.
func stripComments(js string) string {
	var b strings.Builder
	b.Grow(len(js))
	for i := 0; i < len(js); i++ {
		if js[i] == '/' && i+1 < len(js) {
			switch js[i+1] {
			case '/':
				for i < len(js) && js[i] != '\n' {
					i++
				}
				if i < len(js) {
					b.WriteByte('\n')
				}
				continue
			case '*':
				i += 2
				for i+1 < len(js) && (js[i] != '*' || js[i+1] != '/') {
					if js[i] == '\n' {
						b.WriteByte('\n')
					}
					i++
				}
				i++
				continue
			}
		}
		b.WriteByte(js[i])
	}
	return b.String()
}
