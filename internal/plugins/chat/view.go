package chat

import (
	"fmt"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// view draws the page: the transcript over the composer or, for a chat with
// nothing in it yet, the greeting, the composer and suggestions centered
// together. The composer keeps its place in the tree either way, so it keeps
// its focus when the first message moves it to the foot of the page.
func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	p.prepareMarkdown(c, pal)
	p.frameDue = p.pace(c)
	blank := len(p.t.rows) == 0 && !p.working() && !p.loading.Running()
	var work []kernel.Entry[chat.WorkSection]
	if p.workWanted() {
		work = p.workSections()
	}
	drawers := kernel.Items(p.ctx, chat.Drawers)
	ui.Row(c).Fill().MinHeight(0).Children(func() {
		p.page(c, pal, blank)
		if len(work) > 0 {
			p.work(c, pal, work)
		}
		for _, e := range drawers {
			c.Key(e.ID)
			kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
		}
	})
}

// page is the conversation itself, beside the work panel.
func (p *Plugin) page(c *ui.Context, pal native.Palette, blank bool) {
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
		top := ui.Column(c).FillWidth().Grow(1).MinHeight(0)
		if blank {
			top.Justify(ui.End)
		}
		top.Children(func() {
			switch {
			case len(p.t.rows) > 0 || p.working():
				p.transcriptView(c, pal)
			case p.loading.Running():
				ui.Column(c).Fill().Center().Children(func() {
					onechat.Shimmer(c, "Loading the conversation…", 13, pal.Muted, pal.Text)
				})
			default:
				p.greeting(c, pal)
			}
		})
		p.composer(c, pal)
		bottom := ui.Box(c).FillWidth()
		if blank {
			bottom.Grow(1).MinHeight(0)
		}
		bottom.Children(func() {
			if blank {
				p.suggestions(c, pal)
			}
		})
	})
}

// working reports a turn in flight with nothing arriving right now: the
// shimmer stands at the foot of the transcript until words land, and steps
// aside while an approval waits.
func (p *Plugin) working() bool {
	return p.running() && !p.t.streaming() && !p.t.waitingApproval()
}

func (p *Plugin) prepareMarkdown(c *ui.Context, pal native.Palette) {
	p.mdTheme = native.MarkdownTheme(pal)
	p.md.Theme = &p.mdTheme
	p.md.ShareSelection = true
	p.md.OnLink = p.openLink
	p.md.Reveal = &atOnce // pace paces the words
}

func (p *Plugin) openLink(url string) {
	url = strings.TrimSpace(url)
	if ui.SafeToOpen(url) {
		p.host.OpenURL(url)
	}
}

// greeting stands over the composer of a chat with nothing in it yet.
func (p *Plugin) greeting(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(14).Padding(24, 24, 22, 24).Children(func() {
		ui.Box(c).Size(44, 44).Radius(13).Center().Background(pal.Orange.Alpha(.12)).Children(func() {
			ui.Icon(c, lucide.Sparkles()).FontSize(21).TextColor(pal.Orange)
		})
		title, detail := "What should we work on?", "Ask anything, or pick a folder so the agent can read and edit its files."
		switch {
		case p.conv.Session != "":
			title, detail = "This conversation is empty", "Send a message to pick it up."
		case p.conv.Directory != "":
			detail = "Working in " + native.Tilde(p.conv.Directory)
		}
		ui.Column(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, title).FontSize(24).FontWeight(600).TextColor(pal.Text)
			ui.Text(c, detail).FontSize(13).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
	})
}

// suggestions are starting points under the composer of an empty new chat;
// one fills the draft for the user to finish or send.
var suggestions = []struct {
	label  string
	icon   func() *ui.SVG
	prompt string
}{
	{"Explain this project", lucide.BookOpen, "Give me a tour of this project: what it does, how it is laid out and where to start reading."},
	{"Find a bug", lucide.Bug, "Look for a bug in "},
	{"Write tests", lucide.FlaskConical, "Write tests for "},
	{"Review changes", lucide.GitPullRequest, "Review my uncommitted changes for bugs and anything unclear."},
}

func (p *Plugin) suggestions(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(18).Padding(14, 24, 24, 24).Children(func() {
		if p.conv.Session == "" {
			ui.Row(c).Wrap().Justify(ui.Center).Gap(8).MaxWidth(onechat.ContentWidth).Children(func() {
				for i, s := range suggestions {
					c.Key(i)
					b := ui.ButtonBase(c).Label(s.label).Height(32).Radius(16).Padding(0, 13).Center().Gap(7).
						Border(1, pal.Hairline).Cursor(ui.CursorPointer)
					if b.Hovered() {
						b.Background(pal.Hover)
					}
					b.OnClick(func() { p.draft, p.focus = s.prompt, true })
					b.Children(func() {
						ui.Icon(c, s.icon()).FontSize(13).TextColor(pal.Muted)
						ui.Text(c, s.label).FontSize(12.5).TextColor(pal.Text).SingleLine()
					})
				}
			})
		}
		hint := "Enter sends  ·  Shift+Enter adds a line"
		for _, e := range kernel.Items(p.ctx, chat.Completions) {
			if e.Value.Hint != "" {
				hint += "  ·  " + string(e.Value.Trigger) + " " + e.Value.Hint
			}
		}
		ui.Text(c, hint).FontSize(11.5).TextColor(pal.Faint)
	})
}

type displayItem struct {
	key      any
	item     *item
	activity *chat.ActivityItem
}

func (p *Plugin) displayItems() []displayItem {
	items := p.t.items()
	var activities []chat.ActivityItem
	for _, e := range kernel.Items(p.ctx, chat.Activities) {
		kernel.Invoke(p.ctx, e, func(a chat.Activity) { activities = append(activities, a.Items(p.conv.Session)...) })
	}
	var out []displayItem
	user := 0
	used := map[string]bool{}
	for i := range items {
		it := &items[i]
		out = append(out, displayItem{key: it.key, item: it})
		if it.row != nil && it.row.kind == rowUser {
			user++
			for j := range activities {
				a := &activities[j]
				if a.After == user {
					out = append(out, displayItem{key: "activity:" + a.ID, activity: a})
					used[a.ID] = true
				}
			}
		}
	}
	for j := range activities {
		a := &activities[j]
		if !used[a.ID] {
			out = append(out, displayItem{key: "activity:" + a.ID, activity: a})
		}
	}
	return out
}

func (p *Plugin) transcriptView(c *ui.Context, pal native.Palette) {
	items := p.displayItems()
	count := len(items)
	if p.working() {
		count++
	}
	list := ui.List(c.Key(p.conv.Session), &p.list, count).Fill().Selectable().Padding(26, 24, 18, 24).Gap(13).
		ItemKey(func(i int) any {
			if i == len(items) {
				return "working"
			}
			return items[i].key
		}).
		Rows(func(r ui.ListRow) {
			if r.Index == len(items) {
				column(r.Context).Children(func() { p.waiting(r.Context, pal) })
				return
			}
			entry := items[r.Index]
			if entry.activity != nil {
				column(r.Context).Children(func() { entry.activity.Render(r.Context) })
				return
			}
			it := entry.item
			if it.work != nil {
				p.workView(r.Context, pal, it.work)
			} else {
				p.rowView(r.Context, pal, it.row, it.answer)
			}
		})
	p.selectionPopover(c, pal, list)
	if !p.list.AtEnd() {
		ui.Row(c).Absolute().Left(0).Right(0).Bottom(12).Justify(ui.Center).Children(func() {
			components.Button(c, "Latest", components.ButtonSecondary).Label("Jump to latest").Height(28).Radius(14).OnClick(func() { p.list.ScrollToEnd() })
		})
	}
}

// shimmerFor is how long the foot of a turn in flight shimmers before it says
// how long the wait has been instead: the shimmer repaints the window 30
// times a second, the time once a second.
const shimmerFor = 2 * time.Second

// waiting stands at the foot of a turn in flight with nothing arriving: a
// shimmer, then, as the wait goes on, how long it has been since the
// conversation last changed, as while a long command runs.
func (p *Plugin) waiting(c *ui.Context, pal native.Palette) {
	now := c.Now()
	if since := (waitMark{p.conv.Session, p.version}); since != p.waitMark || p.waitSince.IsZero() {
		p.waitMark, p.waitSince = since, now
	}
	waited := now.Sub(p.waitSince)
	if waited < shimmerFor {
		onechat.Shimmer(c, "Working…", 13, pal.Muted, pal.Text)
		c.After(shimmerFor - waited)
		return
	}
	ui.Text(c, "Working · "+elapsed(waited)).FontSize(13).TextColor(pal.Muted).SingleLine()
	c.After(time.Second - waited%time.Second)
}

// waitMark is the conversation and the count of its events as a wait began.
type waitMark struct {
	session agent.SessionID
	version uint64
}

// elapsed words a wait: 12s, 1m 05s, 1h 02m.
func elapsed(d time.Duration) string {
	s := int(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %02dm", s/3600, s/60%60)
}

func column(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).Gap(10)
}

// rowView draws one row. Of the agent's words, only a finished turn's answer
// can be copied; the user's always can.
func (p *Plugin) rowView(c *ui.Context, pal native.Palette, r *row, answer bool) {
	block := column(c)
	copyable := (r.kind == rowUser || answer) && strings.TrimSpace(r.text) != ""
	// The last message can be retried or edited, taking its turn back.
	replaceable := copyable && r.kind == rowUser && !p.replacing && p.canReplace() &&
		p.t.lastUser() >= 0 && p.t.rows[p.t.lastUser()] == r
	showCopy := copyable && (block.Hovered() || block.FocusWithin())
	block.Children(func() {
		switch r.kind {
		case rowUser:
			onechat.Message(c, true, func(c *ui.Context) {
				if len(r.files) > 0 {
					ui.Row(c).Wrap().Gap(6).Justify(ui.End).Children(func() {
						for i, f := range r.files {
							c.Key(i)
							onechat.Attachment(c, onechat.AttachmentOptions{Label: f.Name, Icon: fileIcon(f.MIME)})
						}
					})
				}
				if r.text != "" {
					onechat.Bubble(c, func(c *ui.Context) { ui.Text(c, r.text).FontSize(14).LineHeight(1.45) })
				}
			})
		case rowText:
			if r.reveal != nil {
				markdown.Stream(c, shownText(r), &p.md) // still catching up
			} else {
				markdown.View(c, r.text, &p.md)
			}
		case rowStream:
			markdown.Stream(c, shownText(r), &p.md)
		case rowTool:
			p.toolView(c, pal, r)
		case rowApproval:
			p.approvalView(c, pal, *r.approval)
		case rowInteraction:
			p.interactionView(c, pal, r.interaction)
		case rowNote:
			ui.Text(c, r.text).FontSize(13).LineHeight(1.45).TextColor(pal.Faint)
		}
		if copyable {
			justify := ui.Start
			if r.kind == rowUser {
				justify = ui.End
			}
			actions := ui.Row(c).FillWidth().Justify(justify)
			if !showCopy {
				actions.Invisible()
			}
			actions.Gap(2).Children(func() {
				if replaceable {
					if len(r.files) == 0 {
						c.Key("retry")
						action(c, pal, "Retry message", "Retry", lucide.RotateCcw(), func() { p.retry(r.text) })
					}
					c.Key("edit")
					action(c, pal, "Edit message", "Edit", lucide.Pencil(), func() { p.edit(r) })
				}
				c.Key("copy")
				action(c, pal, "Copy message", "Copy", lucide.Copy(), func() { p.copy(r.text) })
			})
		}
	})
}

// action is a quiet button under a message.
func action(c *ui.Context, pal native.Palette, label, text string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(24).Radius(6).Padding(0, 7).Center().Gap(4).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(on)
	b.Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(pal.Muted)
		ui.Text(c, text).FontSize(11.5).TextColor(pal.Muted).SingleLine()
	})
}

func (p *Plugin) copy(text string) {
	if err := p.host.Copy(text); err != nil {
		p.host.Toast(ui.Toast{Title: "Could not copy the message", Description: err.Error(), Type: components.ToastError})
		return
	}
	p.host.Toast(ui.Toast{Title: "Message copied", Type: components.ToastSuccess})
}

// workView folds a finished turn's steps under one quiet header; opened, it
// shows them as they were.
func (p *Plugin) workView(c *ui.Context, pal native.Palette, work []*row) {
	id := work[0].id
	open := p.worked[id]
	column(c).Gap(6).Children(func() {
		label := workLabel(work)
		head := ui.ButtonBase(c).Label(label).Height(28).Radius(6).Padding(0, 6).
			Justify(ui.Start).AlignItems(ui.Center).Gap(5).Cursor(ui.CursorPointer)
		if head.Hovered() {
			head.Background(pal.Hover)
		}
		head.OnClick(func() { p.worked[id] = !p.worked[id] })
		head.Children(func() {
			ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted).SingleLine()
			chev := lucide.ChevronRight()
			if open {
				chev = lucide.ChevronDown()
			}
			ui.Icon(c, chev).FontSize(11).TextColor(pal.Faint).Shrink(0)
		})
		ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		if open {
			ui.Column(c).FillWidth().Gap(13).Padding(4, 0, 0, 0).Children(func() {
				for _, r := range work {
					c.Key(r.id)
					p.rowView(c, pal, r, false)
				}
			})
		}
	})
}

func (p *Plugin) toolView(c *ui.Context, pal native.Palette, r *row) {
	open := p.expanded[r.id]
	card := ui.Column(c).FillWidth().Radius(9).Clip()
	if open {
		card.Border(1, pal.Hairline).Background(pal.DetailBg)
	}
	card.Children(func() {
		head := ui.ButtonBase(c).Label(r.tool.label).Height(30).Radius(0).Padding(0, 9).FillWidth().
			Justify(ui.Start).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer)
		if head.Hovered() {
			head.Background(pal.Hover)
		}
		head.OnClick(func() { p.expanded[r.id] = !p.expanded[r.id] })
		head.Children(func() {
			icon := toolIcon(r.tool.kind)
			ui.Icon(c, icon).FontSize(12).TextColor(pal.Muted).Shrink(0)
			ui.Text(c, r.tool.label).FontSize(12.5).FontWeight(500).TextColor(pal.Muted).
				Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			chev := lucide.ChevronRight()
			if open {
				chev = lucide.ChevronDown()
			}
			ui.Icon(c, chev).FontSize(10).TextColor(pal.Faint).Shrink(0)
		})
		if open && len(r.tool.detail) > 0 {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
			ui.Column(c).FillWidth().Padding(9, 12).Gap(3).Children(func() {
				for _, line := range r.tool.detail {
					ui.Text(c, line).Font("monospace").FontSize(12).LineHeight(1.55).TextColor(pal.Text).SingleLine()
				}
			})
		}
	})
}

func (p *Plugin) approvalView(c *ui.Context, pal native.Palette, a agent.Approval) {
	ui.Box(c).FillWidth().Radius(12).Padding(12, 14).Background(pal.DetailBg).Border(1, pal.Hairline).Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, lucide.ShieldCheck()).FontSize(15).TextColor(pal.Orange)
				ui.Text(c, "Approval needed — "+approvalAsk(a)).FontSize(13).FontWeight(600).SingleLine()
			})
			for _, res := range a.Resources {
				ui.Box(c).FillWidth().Radius(7).Padding(6, 10).Background(pal.Bg).Border(1, pal.Hairline).Children(func() {
					ui.Text(c, res).Font("monospace").FontSize(12).LineHeight(1.5).TextColor(pal.Text).SingleLine()
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				for _, choice := range []struct {
					label string
					kind  components.ButtonVariant
					d     agent.Decision
				}{{"Allow once", components.ButtonPrimary, agent.Once}, {"Always", components.ButtonSecondary, agent.Always},
					{"Deny", components.ButtonGhost, agent.Reject}} {
					components.Button(c, choice.label, choice.kind).OnClick(func() { p.reply(a, choice.d) })
				}
			})
			if len(a.Save) > 0 {
				ui.Text(c, "Always would remember "+strings.Join(a.Save, ", ")+".").FontSize(12).TextColor(pal.Muted).SingleLine()
			}
		})
	})
}

func toolIcon(kind agent.ToolKind) *ui.SVG {
	switch kind {
	case agent.ToolShell:
		return lucide.Terminal()
	case agent.ToolRead:
		return lucide.BookOpen()
	case agent.ToolSearch:
		return lucide.Search()
	case agent.ToolEdit:
		return lucide.FilePen()
	case agent.ToolFetch:
		return lucide.Globe()
	case agent.ToolOther:
	}
	return lucide.Plug()
}

// approvalAsk words what an approval asks to do, for "Approval needed — …".
func approvalAsk(a agent.Approval) string {
	switch a.Kind {
	case agent.ToolShell:
		return "run a shell command"
	case agent.ToolRead:
		return "read files"
	case agent.ToolSearch:
		return "search files"
	case agent.ToolEdit:
		return "edit files"
	case agent.ToolFetch:
		return "fetch from the web"
	case agent.ToolOther:
	}
	if a.Action == "" {
		return "continue"
	}
	return "use " + a.Action
}

// fileIcon is the chip icon for a file of this MIME type.
func fileIcon(mime string) func() *ui.SVG {
	if strings.HasPrefix(mime, "image/") {
		return lucide.Image
	}
	return lucide.FileText
}

func (p *Plugin) composer(c *ui.Context, pal native.Palette) {
	accessories := kernel.Items(p.ctx, chat.ComposerAccessory)
	trailing := kernel.Items(p.ctx, chat.ComposerTrailing)
	attachments := kernel.Items(p.ctx, chat.ComposerAttachments)
	drops := kernel.Items(p.ctx, chat.FileDrop)
	pastes := kernel.Items(p.ctx, chat.Paste)
	completions := kernel.Items(p.ctx, chat.Completions)
	ui.Column(c).FillWidth().Padding(8, 24, 16, 24).Children(func() {
		ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().Children(func() {
			if p.replacing {
				p.editBanner(c, pal)
			}
			p.completionList(c, pal)
			if p.queueable() && p.running() {
				components.Button(c, "Stop", components.ButtonSecondary).OnClick(p.stop)
			}
			options := onechat.ComposerOptions{
				Label: "Compose", Placeholder: "Message the agent", SendLabel: "Send", StopLabel: "Stop",
				Running: p.running() && !p.queueable(), SendDisabled: p.agent.Status().State != agent.Ready || p.loading.Running(),
			}
			if len(attachments) > 0 {
				options.Attachments = func(c *ui.Context) {
					for _, e := range attachments {
						kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
					}
				}
			}
			options.AcceptFiles = len(drops) > 0
			if len(pastes) > 0 {
				options.OnPaste = func(text string) bool {
					took := false
					for _, e := range pastes {
						if !took {
							kernel.Invoke(p.ctx, e, func(paste func(string) bool) { took = paste(text) })
						}
					}
					return took
				}
			}
			result := onechat.Composer(c, &p.draft, options)
			p.composerFooter(c, accessories, trailing)
			result.Card.Radius(16).Shadow(0, 6, 22, -6, pal.Text.Alpha(.12))
			if p.focus {
				result.Input.Focus()
				p.focus = false
			}
			p.complete(c, result.Input, completions)
			if result.Send {
				// Enter while the list is up chooses from it, as Tab does; the Send
				// button, which has the focus when it is pressed, always sends.
				if p.comp.shown() && result.Input.Focused() {
					p.chooseSoon(p.comp.choices[p.comp.active])
				} else {
					p.send()
				}
			}
			if result.Stop {
				p.stop()
			}
			if paths := result.Dropped; len(paths) > 0 && len(drops) > 0 {
				// Handlers may change state, so they run after the frame.
				p.ctx.Post(func() {
					kernel.Invoke(p.ctx, drops[0], func(drop func([]string)) { drop(paths) })
				})
			}
			if st := p.agent.Status(); st.State != agent.Ready {
				ui.Text(c, connectionLine(st)).FontSize(12).TextColor(pal.Muted).Margin(6, 0, 0, 0).SingleLine()
			}
		})
	})
}

// composerFooter keeps the editor's action row free of settings. The committed
// footer width follows the chat pane, including drawers and the work panel.
func (p *Plugin) composerFooter(c *ui.Context, accessories, trailing []kernel.Entry[shell.View]) {
	width := p.footer.Bounds(c).W
	compact := width == 0 || width < 600
	services := c.Services()
	render := func(entries []kernel.Entry[shell.View]) {
		for _, e := range entries {
			c.Key(e.ID)
			kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
		}
	}
	ui.Row(c).Key("settings").Bind(&p.footer).FillWidth().MinWidth(0).MinHeight(32).Wrap().Shrink(0).Padding(4, 0, 0, 0).AlignItems(ui.Center).Gap(6).
		DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
			// Geometry is committed after building. Rebuild once when the new
			// layout crosses the breakpoint, including when a drawer opens.
			if (bounds.W < 600) != compact {
				services.Invalidate()
			}
		}).Children(func() {
		if !compact {
			render(accessories)
		}
		ui.Box(c).Grow(1).MinWidth(0)
		render(kernel.Items(p.ctx, chat.ComposerPrimary))
		if compact && len(accessories)+len(trailing) > 0 {
			trigger := onechat.IconAction(c.Key("overflow"), "More composer options", lucide.Ellipsis)
			trigger.OnClick(func() { p.composerOptions = !p.composerOptions })
			components.Popover(c, trigger, &p.composerOptions, func(panel ui.Element) {
				panel.Label("Composer options").Width(280).Children(func() {
					ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
						render(accessories)
						render(trailing)
					})
				})
			})
		} else if !compact {
			render(trailing)
		}
	})
}

// editBanner stands over the composer while it edits the last message.
func (p *Plugin) editBanner(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(0, 4, 8, 12).Children(func() {
		ui.Icon(c, lucide.Pencil()).FontSize(12).TextColor(pal.Orange).Shrink(0)
		detail := "Editing your last message. Sending replaces it and everything after it."
		if p.replacingFiles {
			detail += " Its files are not sent again."
		}
		ui.Text(c, detail).FontSize(12).TextColor(pal.Muted).Grow(1).MinWidth(0)
		components.Button(c, "Cancel", components.ButtonGhost).Label("Cancel edit").Height(24).OnClick(p.cancelEdit)
	})
}

func connectionLine(st agent.Status) string {
	switch st.State {
	case agent.Connecting:
		return "Connecting to the agent…"
	case agent.Down:
		if st.Err != nil {
			return "The agent is unavailable: " + st.Err.Error() + ". Retrying…"
		}
		return "The agent is unavailable. Retrying…"
	case agent.Stopped:
		return "The agent is not running."
	case agent.Ready:
	}
	return ""
}
