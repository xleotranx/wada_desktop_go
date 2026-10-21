package chat

import (
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// view draws the page: the transcript, or the new-chat screen, over the
// composer.
func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	p.prepareMarkdown(c, pal)
	ui.Column(c).Grow(1).FillHeight().Background(pal.CanvasBg).Children(func() {
		ui.Box(c).Grow(1).Children(func() {
			switch {
			case len(p.t.rows) > 0 || p.working():
				p.transcriptView(c, pal)
			case p.loading.Running():
				ui.Column(c).Fill().Center().Children(func() {
					onechat.Shimmer(c, "Loading the conversation…", 13, pal.Muted, pal.Text)
				})
			default:
				p.empty(c, pal)
			}
		})
		p.composer(c, pal)
	})
}

// working reports a turn in flight with nothing arriving right now: the
// shimmer stands at the foot of the transcript until words land, and steps
// aside while an approval waits.
func (p *Plugin) working() bool {
	return p.running() && !p.t.streaming() && !p.t.waitingApproval()
}

func (p *Plugin) prepareMarkdown(c *ui.Context, pal native.Palette) {
	p.mdTheme = markdown.Theme{
		FontSize: 14, Text: pal.Text, Muted: pal.Muted, Link: pal.Link, Strong: pal.Text, Code: pal.Text,
		CodeBackground: pal.DetailBg, CodeBlockBackground: pal.DetailBg, CodeBlockBorder: pal.Hairline,
		QuoteBar: pal.Hairline, QuoteText: pal.Muted, Rule: pal.Hairline, TableBorder: pal.Hairline,
	}
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

func (p *Plugin) empty(c *ui.Context, pal native.Palette) {
	ui.Column(c).Fill().Center().Gap(18).Padding(28, 24, 32, 24).Children(func() {
		ui.Box(c).Size(56, 56).Radius(16).Center().Background(c.Theme().Selection.Alpha(.55)).
			Border(1, pal.Hairline).Children(func() {
			ui.Icon(c, lucide.Sparkles()).FontSize(25).TextColor(pal.Orange)
		})
		title := "What should we work on?"
		if p.conv.Session != "" {
			title = "This conversation is empty"
		}
		ui.Text(c, title).FontSize(26).FontWeight(600).TextColor(pal.Text)
		ui.Text(c, "Enter sends  ·  Shift+Enter adds a line").FontSize(12).TextColor(pal.Muted)
	})
}

func (p *Plugin) transcriptView(c *ui.Context, pal native.Palette) {
	count := len(p.t.rows)
	if p.working() {
		count++
	}
	ui.List(c.Key(p.conv.Session), &p.list, count).Fill().Selectable().
		Padding(26, 24, 18, 24).Gap(13).
		ItemKey(func(i int) any {
			if i == len(p.t.rows) {
				return "working"
			}
			return p.t.rows[i].id
		}).
		Rows(func(r ui.ListRow) {
			if r.Index == len(p.t.rows) {
				column(r.Context).Children(func() {
					onechat.Shimmer(r.Context, "Working…", 13, pal.Muted, pal.Text)
				})
				return
			}
			p.rowView(r.Context, pal, p.t.rows[r.Index])
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

func (p *Plugin) rowView(c *ui.Context, pal native.Palette, r *row) {
	block := column(c)
	copyable := (r.kind == rowUser || r.kind == rowText) && strings.TrimSpace(r.text) != ""
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
			actions.Children(func() {
				b := ui.ButtonBase(c).Label("Copy message").Height(24).Radius(6).Padding(0, 7).Center().Gap(4)
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.OnClick(func() { p.copy(r.text) })
				b.Children(func() {
					ui.Icon(c, lucide.Copy()).FontSize(12).TextColor(pal.Muted)
					ui.Text(c, "Copy").FontSize(11.5).TextColor(pal.Muted).SingleLine()
				})
			})
		}
	})
}

func (p *Plugin) copy(text string) {
	if err := p.host.Copy(text); err != nil {
		p.host.Toast(ui.Toast{Title: "Could not copy the message", Description: err.Error(), Type: components.ToastError})
		return
	}
	p.host.Toast(ui.Toast{Title: "Message copied", Type: components.ToastSuccess})
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
			icon := lucide.Plug()
			switch r.tool.icon {
			case "shell":
				icon = lucide.Terminal()
			case "read":
				icon = lucide.BookOpen()
			}
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
				ui.Text(c, "Approval needed — "+approvalAsk(a.Action)).FontSize(13).FontWeight(600).SingleLine()
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

func approvalAsk(action string) string {
	switch action {
	case "bash", "shell":
		return "run a shell command"
	case "edit":
		return "edit files"
	case "webfetch":
		return "fetch from the web"
	}
	return "use " + action
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
	attachments := kernel.Items(p.ctx, chat.ComposerAttachments)
	drops := kernel.Items(p.ctx, chat.FileDrop)
	pastes := kernel.Items(p.ctx, chat.Paste)
	completions := kernel.Items(p.ctx, chat.Completions)
	ui.Column(c).FillWidth().Padding(8, 24, 16, 24).Children(func() {
		ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().Children(func() {
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
			result := onechat.Composer(c, &p.draft, options)
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
