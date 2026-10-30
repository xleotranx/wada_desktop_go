package chat

import (
	"strings"

	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// popoverWidth is about the popover's width, to keep it inside the
// transcript near its right edge.
const popoverWidth = 230

// selectionState follows the transcript's text selection for the popover.
type selectionState struct {
	text   string  // the selection as of the last build
	x, y   float32 // where the popover goes: the pointer as it last moved over the list
	px, py float32 // the pointer as of the last build, over the list or not
	stable bool    // the selection and the pointer stayed as they were for a build: the drag is over
	done   bool    // an action ran on this selection; it shows no more
}

// selected joins the selected parts of the transcript's paragraphs.
func selected(parts []ui.SelectedText) string {
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		runes := []rune(part.Text)
		start, end := min(part.Start, len(runes)), min(part.End, len(runes))
		if start < end {
			lines = append(lines, string(runes[start:end]))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// selectionPopover offers actions for text selected in the transcript: Reply
// and the SelectionActions entries. It shows as the drag ends, under where
// the pointer let go, and closes when the selection is gone, an action ran or
// the pointer went down elsewhere. MyGo tells only the paragraph pressed that
// the pointer came up, but while it is held each frame comes from a move: the
// drag is over at a frame where neither the selection nor the pointer moved,
// so a drag within a letter, which keeps the selection, keeps it hidden.
func (p *Plugin) selectionPopover(c *ui.Context, pal native.Palette, list ui.Element) {
	text := selected(list.SelectedTexts())
	s := &p.sel
	if text == "" {
		*s = selectionState{}
		return
	}
	if changed := text != s.text; changed || !s.stable {
		// The pointer is asked for only while a selection is being made, so
		// moving over the transcript otherwise draws nothing again.
		x, y, over := list.PointerPosition()
		moved := x != s.px || y != s.py
		if changed {
			*s = selectionState{text: text, x: s.x, y: s.y}
		}
		s.px, s.py = x, y
		if over {
			s.x, s.y = x, y
		}
		if changed || moved {
			return
		}
		s.stable = true
	}
	if s.done {
		return
	}
	bounds := list.Bounds()
	left := max(8, min(s.x-24, bounds.W-popoverWidth-8))
	top := min(s.y+14, max(8, bounds.H-44))
	sel := chat.Selection{Conversation: p.conv, Text: text}
	actions := kernel.Items(p.ctx, chat.SelectionActions)
	pop := ui.Row(c.Key("selection-popover")).Absolute().Left(left).Top(top).Padding(4).Gap(2).Radius(10).
		Background(pal.Raised).Border(1, pal.Hairline).Shadow(0, 6, 18, -4, pal.Text.Alpha(.18)).
		Label("Selected text")
	if pop.PressedOutside() {
		// Such as in the composer: the selection stays, the popover goes.
		s.done = true
	}
	pop.Children(func() {
		for _, e := range actions {
			c.Key(e.ID)
			popoverButton(c, pal, e.Value.Title, e.Value.Icon, func() {
				s.done = true
				kernel.Invoke(p.ctx, e, func(a chat.SelectionAction) { a.Run(sel) })
			})
		}
		c.Key("reply")
		popoverButton(c, pal, "Reply", lucide.Reply, func() {
			s.done = true
			p.replyWith(text)
		})
	})
}

func popoverButton(c *ui.Context, pal native.Palette, title string, icon func() *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(title).Height(28).Radius(7).Padding(0, 9).Center().Gap(6).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(on)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon()).FontSize(13).TextColor(pal.Muted)
		}
		ui.Text(c, title).FontSize(12.5).TextColor(pal.Text).SingleLine()
	})
}

// replyWith quotes text in the composer, after what is there, for the user
// to answer.
func (p *Plugin) replyWith(text string) {
	draft := strings.TrimRight(p.DraftText(), "\n")
	if draft != "" {
		draft += "\n\n"
	}
	p.SetDraft(draft + quote(text) + "\n\n")
}

// quote makes text a Markdown block quote, line by line.
func quote(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}
