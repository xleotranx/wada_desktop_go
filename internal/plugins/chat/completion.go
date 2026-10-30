package chat

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// maxChoices is how many suggestions the list shows at once.
const maxChoices = 8

// completion is the composer's word completion: the word under the caret, the
// contribution whose trigger starts it, and the list shown for it. It is
// plugin state: rendering updates it from the frame's input, and the handlers
// for keys and clicks change it between frames.
type completion struct {
	// The word under the caret, in runes of the draft. id is empty when the
	// caret is in no word that starts with a trigger.
	id         string
	start, end int // the trigger, and the caret
	typed      string
	// What contributions were last told, so they hear of a change once.
	told word

	choices []chat.Candidate // empty while nothing is offered
	active  int
	hidden  word // the word Escape hid the list for
	drawn   string

	caret int // where the caret goes once the draft has taken a choice; -1 for nowhere
}

// word is what a contribution is told about: which one, and what is typed.
type word struct {
	id, typed string
	on        bool
}

func newCompletion() completion { return completion{caret: -1} }

func (cs *completion) word() word { return word{cs.id, cs.typed, cs.id != ""} }

// signature names what the list shows, to notice that it must be drawn again.
func (cs *completion) signature() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(cs.active))
	for _, ch := range cs.choices {
		b.WriteByte(0)
		b.WriteString(ch.ID)
	}
	return b.String()
}

// shown reports whether the list is on screen.
func (cs *completion) shown() bool { return len(cs.choices) > 0 }

// wordAt returns where the word containing the caret starts and ends: the
// runes around it that are not whitespace.
func wordAt(text []rune, caret int) (start, end int) {
	start, end = caret, caret
	for start > 0 && !unicode.IsSpace(text[start-1]) {
		start--
	}
	for end < len(text) && !unicode.IsSpace(text[end]) {
		end++
	}
	return start, end
}

// complete finds the word the caret is in, tells the contribution that owns
// it what has been typed, and takes what the list should offer. It runs after
// the editor is built in a frame.
func (p *Plugin) complete(c *ui.Context, input ui.Element, items []kernel.Entry[chat.Completion]) {
	cs := &p.comp
	if len(items) == 0 {
		*cs = newCompletion()
		return
	}
	input.Changed() // takes this frame's typing, so the draft and the caret agree
	if cs.caret >= 0 {
		input.SetTextSelection(cs.caret, cs.caret)
		cs.caret = -1
	}
	if input.Composing() { // an input method's keys are its own
		return
	}
	cs.id, cs.typed, cs.choices = "", "", nil
	if from, to := input.TextSelection(); from == to && input.Focused() {
		text := []rune(p.draft)
		if to <= len(text) {
			start, _ := wordAt(text, to)
			for _, e := range items {
				if start < to && text[start] == e.Value.Trigger {
					cs.id, cs.start, cs.end, cs.typed = e.ID, start, to, string(text[start+1:to])
					break
				}
			}
		}
	}
	now := cs.word()
	if now != cs.told {
		was := cs.told
		cs.active, cs.hidden = 0, word{} // a new word shows its list again
		cs.told = now
		p.ctx.Post(func() { p.notify(was, now) })
	}
	if now.on && now != cs.hidden {
		for _, e := range items {
			if e.ID == now.id {
				kernel.Invoke(p.ctx, e, func(v chat.Completion) { cs.choices = v.Candidates() })
			}
		}
		cs.choices = cs.choices[:min(len(cs.choices), maxChoices)]
		cs.active = min(cs.active, max(len(cs.choices)-1, 0))
	}
	if cs.signature() != cs.drawn {
		c.Invalidate() // the list was drawn before this frame learned what it holds
	}
	if cs.shown() {
		input.OnTextKey(p.completionKey)
	}
}

// notify tells contributions what changed in the word under the caret.
func (p *Plugin) notify(was, now word) {
	if was.on && (!now.on || was.id != now.id) {
		p.query(was.id, "", false)
	}
	if now.on {
		p.query(now.id, now.typed, true)
	}
}

func (p *Plugin) query(id, typed string, on bool) {
	for _, e := range kernel.Items(p.ctx, chat.Completions) {
		if e.ID == id {
			kernel.Invoke(p.ctx, e, func(v chat.Completion) { v.Query(typed, on) })
		}
	}
}

// completionKey moves through the list and chooses from it. It runs between
// frames, for the keys the editor would otherwise act on.
func (p *Plugin) completionKey(k ui.TextKey) bool {
	cs := &p.comp
	n := len(cs.choices)
	if n == 0 || k.Mods != 0 {
		return false
	}
	// Not a switch over k.Key: the lint wants every key of the keyboard named.
	if k.Key == ui.KeyDown {
		cs.active = (cs.active + 1) % n
		return true
	}
	if k.Key == ui.KeyUp {
		cs.active = (cs.active + n - 1) % n
		return true
	}
	if k.Key == ui.KeyTab {
		p.chooseSoon(cs.choices[cs.active])
		return true
	}
	if k.Key == ui.KeyEscape {
		cs.hidden = cs.word()
		return true
	}
	return false
}

// chooseSoon takes a choice once the frame or the key being handled is over.
// It carries the candidate, not its place in the list, which a lookup that
// lands in between may have changed.
func (p *Plugin) chooseSoon(choice chat.Candidate) { p.ctx.Post(func() { p.choose(choice) }) }

// choose replaces the word under the caret with what its contribution makes of
// the choice, and puts the caret after it.
func (p *Plugin) choose(choice chat.Candidate) {
	cs := &p.comp
	text := []rune(p.draft)
	_, end := wordAt(text, min(cs.end, len(text)))
	if cs.start >= end || cs.start >= len(text) {
		return
	}
	var repl string
	for _, e := range kernel.Items(p.ctx, chat.Completions) {
		if e.ID == cs.id && text[cs.start] == e.Value.Trigger {
			kernel.Invoke(p.ctx, e, func(v chat.Completion) { repl = v.Accept(choice) })
		}
	}
	if repl == "" {
		return
	}
	tail := text[end:]
	caret := cs.start + utf8.RuneCountInString(repl)
	if len(tail) > 0 && unicode.IsSpace(tail[0]) && strings.HasSuffix(repl, " ") {
		// The space that follows the word does the choice's trailing one's work.
		repl = strings.TrimSuffix(repl, " ")
		caret = cs.start + utf8.RuneCountInString(repl) + 1
	}
	p.draft = string(text[:cs.start]) + repl + string(tail)
	cs.caret = caret
	p.focus = true
}

// completionList draws the suggestions above the editor. The box that holds
// it has no height, and the list hangs from its bottom edge over the page, so
// showing it moves nothing. The box is built even while the list is empty, so
// the editor after it keeps its identity.
func (p *Plugin) completionList(c *ui.Context, pal native.Palette) {
	cs := &p.comp
	cs.drawn = cs.signature()
	ui.Box(c.Key("completions")).FillWidth().Height(0).Shrink(0).Children(func() {
		if !cs.shown() {
			return
		}
		ui.Column(c).Absolute().Left(0).Right(0).Bottom(8).Padding(4).Gap(1).Radius(10).
			Background(pal.DetailBg).Border(1, pal.Hairline).Shadow(0, 4, 14, 0, pal.Text.Alpha(.14)).
			Label("Suggestions").KeepFocus().Children(func() {
			for i, ch := range cs.choices {
				c.Key(ch.ID)
				row := ui.ButtonBase(c).Label(ch.Label).Description(ch.Detail).Height(30).Radius(7).Padding(0, 8).FillWidth().
					Justify(ui.Start).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer).Selected(i == cs.active)
				switch {
				case i == cs.active:
					row.Background(pal.Selected)
				case row.Hovered():
					row.Background(pal.Hover)
				}
				row.OnClick(func() { p.chooseSoon(ch) })
				row.Children(func() {
					if ch.Icon != nil {
						ui.Icon(c, ch.Icon()).FontSize(14).TextColor(pal.Muted).Shrink(0)
					}
					ui.Text(c, ch.Label).FontSize(13).TextColor(pal.Text).Shrink(0).SingleLine()
					ui.Text(c, ch.Detail).FontSize(12).TextColor(pal.Faint).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				})
			}
		})
	})
}
