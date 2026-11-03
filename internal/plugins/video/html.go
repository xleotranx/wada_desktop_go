package video

import (
	"strings"
)

// element is one tag of an HTML file, with where its attributes and contents
// sit in the source. The offsets let an edit rewrite one attribute value and
// leave every other byte of the file as the agent wrote it.
type element struct {
	Tag      string
	Attrs    []attribute
	Opening  span // the start tag, from "<" through ">"
	Inner    span // between the start and close tags
	Closing  span // the close tag, empty when self-closing or unwritten
	Parent   *element
	Children []*element
}

// span is a byte range of the source; End is one past the last byte.
type span struct{ Start, End int }

// attribute is one attribute of a start tag. Value sits inside the quotes when
// the attribute is quoted, so an edit replaces the value and keeps its quotes.
type attribute struct {
	Name    string
	Value   string
	ValueAt span
	Quoted  bool
}

// voidElements have no contents and no close tag.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

// rawTextElements hold text, not markup, so their contents are not scanned.
var rawTextElements = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true, "noscript": true,
}

// scanHTML reads the elements of src into a tree. A tag it cannot parse ends
// the scan, and the elements found so far are returned with its error.
func scanHTML(src string) (*element, error) {
	root := &element{Tag: "#document", Opening: span{0, 0}, Inner: span{0, len(src)}}
	stack := []*element{root}
	open := func() *element { return stack[len(stack)-1] }
	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		rest := src[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest, "-->")
			if end < 0 {
				return root, nil
			}
			i += end + 3
			continue
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?"):
			gt := strings.IndexByte(rest, '>')
			if gt < 0 {
				return root, nil
			}
			i += gt + 1
			continue
		case strings.HasPrefix(rest, "</"):
			name, at := tagName(rest[2:])
			if name == "" {
				i += 2
				continue
			}
			gt := strings.IndexByte(rest[2+at:], '>')
			if gt < 0 {
				return root, nil
			}
			closeAt := span{i, i + 2 + at + gt + 1}
			for n := len(stack) - 1; n > 0; n-- {
				if stack[n].Tag != name {
					continue
				}
				stack[n].Closing = closeAt
				stack[n].Inner.End = i
				stack = stack[:n]
				break
			}
			i = closeAt.End
			continue
		}

		name, at := tagName(rest[1:])
		if name == "" {
			i++
			continue
		}
		el := &element{Tag: name, Parent: open()}
		el.Opening.Start = i
		pos := 1 + at
		selfClosed := false
		for {
			for pos < len(rest) && isSpace(rest[pos]) {
				pos++
			}
			if pos >= len(rest) {
				return root, nil
			}
			if rest[pos] == '>' {
				pos++
				break
			}
			if rest[pos] == '/' {
				selfClosed = true
				pos++
				continue
			}
			attr, next := scanAttribute(rest, pos, i)
			if next <= pos {
				return root, nil
			}
			el.Attrs = append(el.Attrs, attr)
			pos = next
		}
		el.Opening.End = i + pos
		el.Inner.Start = el.Opening.End
		el.Inner.End = el.Opening.End
		open().Children = append(open().Children, el)
		i = el.Opening.End
		if selfClosed || voidElements[name] {
			continue
		}
		if rawTextElements[name] {
			if end := rawEnd(src, i, name); end >= 0 {
				el.Inner.End = end
				el.Closing = span{end, end + len(name) + 3}
				i = end + len(name) + 3
			} else {
				el.Inner.End = len(src)
				i = len(src)
			}
			continue
		}
		stack = append(stack, el)
	}
	for n := 1; n < len(stack); n++ {
		stack[n].Inner.End = len(src)
	}
	return root, nil
}

// scanAttribute reads the attribute at pos, at the offset of the tag in src,
// and returns it with where the next one starts.
func scanAttribute(tag string, pos, at int) (attribute, int) {
	var a attribute
	start := pos
	for pos < len(tag) && !isSpace(tag[pos]) && tag[pos] != '=' && tag[pos] != '>' && tag[pos] != '/' {
		pos++
	}
	a.Name = strings.ToLower(tag[start:pos])
	if a.Name == "" {
		return a, pos
	}
	valueStart := pos
	for valueStart < len(tag) && isSpace(tag[valueStart]) {
		valueStart++
	}
	if valueStart >= len(tag) || tag[valueStart] != '=' {
		return a, pos
	}
	valueStart++
	for valueStart < len(tag) && isSpace(tag[valueStart]) {
		valueStart++
	}
	if valueStart >= len(tag) {
		return a, valueStart
	}
	if q := tag[valueStart]; q == '"' || q == '\'' {
		end := strings.IndexByte(tag[valueStart+1:], q)
		if end < 0 {
			return a, len(tag)
		}
		a.Value = tag[valueStart+1 : valueStart+1+end]
		a.ValueAt = span{at + valueStart + 1, at + valueStart + 1 + end}
		a.Quoted = true
		return a, valueStart + 1 + end + 1
	}
	end := valueStart
	for end < len(tag) && !isSpace(tag[end]) && tag[end] != '>' {
		end++
	}
	a.Value = tag[valueStart:end]
	a.ValueAt = span{at + valueStart, at + end}
	return a, end
}

// rawEnd finds the close tag of a raw text element, starting at from.
func rawEnd(src string, from int, name string) int {
	needle := "</" + name
	lower := strings.ToLower(src)
	for from < len(src) {
		i := strings.Index(lower[from:], needle)
		if i < 0 {
			return -1
		}
		from += i
		after := from + len(needle)
		if after >= len(src) || isSpace(src[after]) || src[after] == '>' {
			return from
		}
		from = after
	}
	return -1
}

// tagName reads a tag's name from the text after "<", returning it with how
// many bytes it took.
func tagName(s string) (string, int) {
	i := 0
	for i < len(s) && (isLetter(s[i]) || s[i] == '-' || s[i] == ':' || isDigit(s[i])) {
		i++
	}
	return strings.ToLower(s[:i]), i
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// attr returns the element's attribute and whether it has it.
func (e *element) attr(name string) (attribute, bool) {
	for _, a := range e.Attrs {
		if a.Name == name {
			return a, true
		}
	}
	return attribute{}, false
}

// value returns the attribute's value, or "" when it has none.
func (e *element) value(name string) string {
	a, _ := e.attr(name)
	return a.Value
}

// has reports whether the element has the attribute, written or not.
func (e *element) has(name string) bool {
	_, ok := e.attr(name)
	return ok
}

// classes returns the element's class names.
func (e *element) classes() []string { return strings.Fields(e.value("class")) }

// hasClass reports whether one of the element's classes is name.
func (e *element) hasClass(name string) bool {
	for _, c := range e.classes() {
		if c == name {
			return true
		}
	}
	return false
}

// walk calls fn for the element and every element inside it, in source order.
func (e *element) walk(fn func(*element) bool) {
	if !fn(e) {
		return
	}
	for _, c := range e.Children {
		c.walk(fn)
	}
}

// byID finds the element with the id, or the data-hf-id, in e's tree.
func (e *element) byID(id string) *element {
	var found *element
	e.walk(func(n *element) bool {
		if found != nil {
			return false
		}
		if n != e && (n.value("id") == id || n.value("data-hf-id") == id) {
			found = n
		}
		return found == nil
	})
	return found
}

// text returns the words of the element's contents, with tags left out.
func (e *element) text(src string) string {
	inner := e.Inner
	if inner.End > len(src) {
		inner.End = len(src)
	}
	if inner.Start >= inner.End {
		return ""
	}
	return plainText(src[inner.Start:inner.End])
}

// plainText removes tags from markup and collapses its whitespace.
func plainText(markup string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(markup); i++ {
		switch markup[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(markup[i])
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
