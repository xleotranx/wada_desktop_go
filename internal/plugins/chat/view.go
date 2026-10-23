package chat

import (
	"strings"

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
	blank := len(p.t.rows) == 0 && !p.working() && !p.loading.Running()
	var work []kernel.Entry[chat.WorkSection]
	if p.conv.Session != "" && p.workOn.Get() {
		work = p.workSections()
	}
	ui.Row(c).Fill().MinHeight(0).Children(func() {
		p.page(c, pal, blank)
		if len(work) > 0 {
			p.work(c, pal, work)
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
		ui.Text(c, "Enter sends  ·  Shift+Enter adds a line  ·  @ mentions a file").FontSize(11.5).TextColor(pal.Faint)
	})
}

func (p *Plugin) transcriptView(c *ui.Context, pal native.Palette) {
	items := p.t.items()
	count := len(items)
	if p.working() {
		count++
	}
	ui.List(c.Key(p.conv.Session), &p.list, count).Fill().Selectable().
		Padding(26, 24, 18, 24).Gap(13).
		ItemKey(func(i int) any {
			if i == len(items) {
				return "working"
			}
			return items[i].key
		}).
		Rows(func(r ui.ListRow) {
			if r.Index == len(items) {
				column(r.Context).Children(func() {
					onechat.Shimmer(r.Context, "Working…", 13, pal.Muted, pal.Text)
				})
				return
			}
			if it := items[r.Index]; it.work != nil {
				p.workView(r.Context, pal, it.work)
			} else {
				p.rowView(r.Context, pal, it.row, it.answer)
			}
		})
	if !p.list.AtEnd() {
		ui.Row(c).Absolute().Left(0).Right(0).Bottom(12).Justify(ui.Center).Children(func() {
			jump := components.Button(c, "Latest", components.ButtonSecondary).Label("Jump to latest").Height(28).Radius(14)
			jump.OnClick(func() { p.list.ScrollToEnd() })
		})
	}
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
			markdown.View(c, r.text, &p.md)
		case rowStream:
			markdown.Stream(c, r.text, &p.md)
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
			options := onechat.ComposerOptions{
				Label: "Compose", Placeholder: "Message the agent", SendLabel: "Send", StopLabel: "Stop",
				Running: p.running(), SendDisabled: p.agent.Status().State != agent.Ready || p.loading.Running(),
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
			if len(accessories) > 0 {
				options.Accessory = func(c *ui.Context) {
					for _, e := range accessories {
						kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
					}
				}
			}
			if len(trailing) > 0 {
				options.Trailing = func(c *ui.Context) {
					for _, e := range trailing {
						kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
					}
				}
			}
			result := onechat.Composer(c, &p.draft, options)
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
