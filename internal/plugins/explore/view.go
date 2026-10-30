package explore

import (
	"cmp"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

const drawerWidth = 420

// drawer is the side chat beside its conversation: a header, its messages and
// its own composer.
func (p *Plugin) drawer(c *ui.Context) {
	if !p.shown() {
		return
	}
	s := p.side
	pal := native.PaletteFor(c)
	p.mdTheme = native.MarkdownTheme(pal)
	p.md.Theme = &p.mdTheme
	p.md.ShareSelection = true
	p.md.OnLink = func(url string) {
		if url = strings.TrimSpace(url); ui.SafeToOpen(url) {
			p.host.OpenURL(url)
		}
	}
	ui.Row(c.Key("side-chat")).Width(drawerWidth).FillHeight().Shrink(0).Label("Side chat").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(pal.Bg).Children(func() {
			p.header(c, pal, s)
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
				if len(s.entries) == 0 && !s.running() {
					p.empty(c, pal, s)
				} else {
					p.messages(c, pal, s)
				}
			})
			p.composer(c, pal, s)
		})
	})
}

func (p *Plugin) header(c *ui.Context, pal native.Palette, s *side) {
	ui.Row(c).FillWidth().Height(48).Shrink(0).Padding(0, 8, 0, 16).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, lucide.MessageSquarePlus()).FontSize(14).TextColor(pal.Muted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, "Side chat").FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine()
			ui.Text(c, "Beside “"+cmp.Or(s.parent.Title, "Untitled chat")+"” · read-only").
				FontSize(11.5).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		b := ui.ButtonBase(c.Key("close")).Label("Close side chat").Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
		b.OnClick(p.close)
		b.Children(func() { ui.Icon(c, lucide.X()).FontSize(14).TextColor(pal.Muted) })
	})
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
}

func (p *Plugin) empty(c *ui.Context, pal native.Palette, s *side) {
	ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
		if s.loading.Running() {
			onechat.Shimmer(c, "Loading the side chat…", 13, pal.Muted, pal.Text)
			return
		}
		ui.Text(c, "No messages yet").FontSize(14).FontWeight(500).TextColor(pal.Text)
		ui.Text(c, "Ask about the quote here. The main chat stays as it is; the side chat can read files but not change them.").
			FontSize(12).LineHeight(1.45).TextColor(pal.Muted).TextAlign(ui.Center).MaxWidth(300)
	})
}

func (p *Plugin) messages(c *ui.Context, pal native.Palette, s *side) {
	working := s.running() && !slices.ContainsFunc(s.entries, func(e *entry) bool { return e.streaming && e.text != "" })
	count := len(s.entries)
	if working {
		count++
	}
	ui.List(c.Key(string(s.child)), &s.list, count).Fill().Selectable().Padding(16, 16, 12, 16).Gap(12).
		ItemKey(func(i int) any {
			if i == len(s.entries) {
				return "working"
			}
			return s.entries[i].id
		}).
		Rows(func(r ui.ListRow) {
			c := r.Context
			if r.Index == len(s.entries) {
				onechat.Shimmer(c, "Working…", 12.5, pal.Muted, pal.Text)
				return
			}
			e := s.entries[r.Index]
			switch e.kind {
			case entryUser:
				onechat.Message(c, true, func(c *ui.Context) {
					onechat.Bubble(c, func(c *ui.Context) { ui.Text(c, e.text).FontSize(13.5).LineHeight(1.45) })
				})
			case entryAnswer:
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					if e.streaming {
						markdown.Stream(c, e.text, &p.md)
						return
					}
					markdown.View(c, e.text, &p.md)
					if !s.running() {
						ui.Row(c).Children(func() { p.addButton(c, pal, e.text) })
					}
				})
			case entryNote:
				ui.Text(c, e.text).FontSize(12).LineHeight(1.45).TextColor(pal.Faint)
			}
		})
}

// addButton brings an answer to the main chat: the side chat's way back.
func (p *Plugin) addButton(c *ui.Context, pal native.Palette, text string) {
	b := ui.ButtonBase(c.Key("add")).Label("Add to main chat").Height(26).Radius(7).Padding(0, 9).Gap(6).
		AlignItems(ui.Center).Border(1, pal.Hairline).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.addToChat(text) })
	b.Children(func() {
		ui.Icon(c, lucide.CornerDownLeft()).FontSize(12).TextColor(pal.Muted)
		ui.Text(c, "Add to main chat").FontSize(12).TextColor(pal.Text).SingleLine()
	})
}

func (p *Plugin) composer(c *ui.Context, pal native.Palette, s *side) {
	ui.Column(c).FillWidth().Shrink(0).Padding(8, 12, 12, 12).Children(func() {
		result := onechat.Composer(c.Key("composer"), &s.draft, onechat.ComposerOptions{
			Label: "Side chat message", Placeholder: "Ask in the side chat", SendLabel: "Send", StopLabel: "Stop",
			Running:      s.running(),
			SendDisabled: p.agent.Status().State != agent.Ready || s.loading.Running() || p.creating.Running(),
		})
		result.Card.Radius(14)
		if s.focus {
			result.Input.Focus()
			s.focus = false
		}
		if result.Send {
			p.send()
		}
		if result.Stop {
			p.stop()
		}
	})
}
