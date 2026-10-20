package main

import (
	"strings"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// prepareMarkdown tunes the transcript's shared markdown options to this
// frame's palette: the app's colors over the package's own meters of
// spacing, heading and code. The selection stays one across the message
// blocks, as the wrapping scroll gives it.
func (a *app) prepareMarkdown(p palette) {
	a.layout.mdTheme = markdown.Theme{
		FontSize:            a.chatFont(14),
		Text:                p.text,
		Muted:               p.muted,
		Link:                p.link,
		Strong:              p.text,
		Code:                p.text,
		CodeBackground:      p.detailBg,
		CodeBlockBackground: p.detailBg,
		CodeBlockBorder:     p.hairline,
		QuoteBar:            p.hairline,
		QuoteText:           p.muted,
		Rule:                p.hairline,
		TableBorder:         p.hairline,
	}
	a.layout.md.Theme = &a.layout.mdTheme
	a.layout.md.ShareSelection = true
}

// newFeed is the transcript's list: it opens at its end and keeps the
// newest text in view as the answer streams. It pins in whole lines rather
// than gliding, so arriving words do not drag the lines above them along.
func newFeed() ui.ListState { return ui.ListState{FollowEnd: true} }

// contentPane draws the conversation title, transcript and composer.
func (a *app) contentPane(c *ui.Context, p palette) {
	a.prepareMarkdown(p)
	ui.Column(c).Grow(1).FillHeight().Background(p.bg).Children(func() {
		a.chatTabs(c, p)
		// The transcript fills the card: the conversation's name lives in
		// the sidebar, as macOS 26 draws it.
		area := ui.Box(c).Grow(1)
		area.Children(func() {
			if a.conversation.target.isDraft() {
				a.draftPanels(c, p)
			} else {
				count := a.conversation.transcript.Len()
				if a.turnWorking() {
					count++
				}
				if count == 0 {
					// A conversation the server opened but nothing has been
					// said in yet: say so rather than draw an empty page.
					ui.Column(c).Fill().Center().Gap(7).Padding(24).Children(func() {
						ui.Icon(c, icChat).FontSize(28).TextColor(p.faint)
						ui.Text(c, "This conversation is empty").FontSize(15).
							FontWeight(500).TextColor(p.muted)
						ui.Text(c, "Send a message below to start it.").FontSize(12.5).
							TextColor(p.faint)
					})
				} else {
					ui.List(c.Key(a.conversation.target.session), &a.layout.feed, count).Fill().Selectable().
						Padding(26, 24, 18, 24).Gap(13).ItemKey(a.blockIdentity).
						Rows(func(row ui.ListRow) {
							if row.Index == a.conversation.transcript.Len() {
								a.blockRow(row.Context, p, chat.Block{Kind: chat.KindWorking, Text: "Working…"})
							} else {
								a.blockRow(row.Context, p, a.conversation.transcript.Row(row.Index))
							}
						})
					// Reading history leaves the newest rows out of sight; the
					// pill brings them back and resumes following the stream.
					if !a.layout.feed.AtEnd() {
						ui.Row(c).Absolute().Left(0).Right(0).Bottom(12).Justify(ui.Center).Children(func() {
							jump := components.Button(c, "", components.ButtonSecondary).
								Label("Jump to latest").Height(28).Radius(14).Padding(0, 12)
							jump.OnClick(func() { a.layout.feed.ScrollToEnd() })
							jump.Children(func() {
								ui.Icon(c, icChevDown).FontSize(12).TextColor(p.muted)
								components.Text(c, "Latest").FontSize(12).MarginStart(5).TextColor(p.text)
							})
						})
					}
				}
			}
			ui.Box(c).Absolute().Start(0).End(0).Bottom(0).Height(26).
				Gradient(p.bg.Alpha(0), p.bg, 180)
			if a.layout.findOpen {
				a.chatFindBar(c, p)
			}
			a.chatDropZone(c, p, area)
		})

		a.composer(c, p)
	})
}

// turnRunning reports the open chat's turn still going.
func (a *app) turnRunning() bool {
	return a.conversation.transcript.Busy() != "" && a.conversation.transcript.Busy() == a.conversation.target.session
}

// awaitingPerm reports an approval of the open chat's turn waiting on the
// user.
func (a *app) awaitingPerm() bool {
	for _, perm := range a.conversation.transcript.Pending() {
		if perm.Session == a.conversation.target.session {
			return true
		}
	}
	return false
}

// turnWorking reports the open chat's turn still going with nothing being
// said right now: the loader stands at the transcript's foot before the
// first words, between them while a tool runs, and steps aside while an
// approval waits on the user.
func (a *app) turnWorking() bool {
	if !a.turnRunning() || a.awaitingPerm() {
		return false
	}
	// The newest stream row is the words landing: empty means there is
	// nothing to read yet, and no stream row at all means the turn sits
	// between steps.
	for i := a.conversation.transcript.Len() - 1; i >= 0; i-- {
		if a.conversation.transcript.Row(i).Kind == chat.KindStream {
			return strings.TrimSpace(a.conversation.transcript.Row(i).Text) == ""
		}
	}
	return true
}

// blockIdentity follows a row when approvals disappear or a stream
// settles. IDs are assigned lazily, so opening history visits only what
// the virtual list needs.
func (a *app) blockIdentity(i int) any {
	if i == a.conversation.transcript.Len() {
		return "working"
	}
	return a.conversation.transcript.Identity(i)
}

// blockRow draws one block of the transcript: a paragraph, what the user
// said, a line of what the agent ran, or the loader of the turn in
// flight.
func (a *app) blockRow(c *ui.Context, p palette, b chat.Block) {
	block := ui.Column(c).FillWidth().MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).Gap(10)
	// User and answer rows earn copy and quote actions while the pointer
	// or keyboard is on them; the row is empty otherwise, leaving the
	// reading rhythm alone.
	actions := (b.Kind == chat.KindUser || b.Kind == chat.KindText) && strings.TrimSpace(b.Text) != ""
	showActions := actions && (block.Hovered() || block.FocusWithin())
	block.Children(func() {
		switch b.Kind {
		case chat.KindUser:
			onechat.Message(c, true, func(c *ui.Context) {
				onechat.Bubble(c, func(c *ui.Context) {
					ui.Text(c, b.Text).FontSize(a.chatFont(14)).LineHeight(1.45)
				})
			})
		case chat.KindActivity:
			a.activityRow(c, p, b)
		case chat.KindPermission:
			a.permissionRow(c, p, b)
		case chat.KindWorking:
			// The turn in flight, until words land: Onevra's shimmer gives
			// the row the motion of a thought being written.
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				onechat.Shimmer(c, b.Text, 13, p.muted, p.text)
			})
		case chat.KindNote:
			// Engine trouble stays a faint note under the answers; it
			// wraps, so an error's "at" lines stay readable.
			ui.Text(c, b.Text).FontSize(a.chatFont(14)).LineHeight(1.45).TextColor(p.faint)
		case chat.KindStream:
			// Markdown still being written: the package parses the new
			// words alone, shows half-written syntax as what it will
			// become, and paces the words in.
			markdown.Stream(c, b.Text, &a.layout.md)
		case chat.KindText:
			markdown.View(c, b.Text, &a.layout.md)
			if line := usageLine(b.Cost, b.Tokens); line != "" {
				ui.Text(c, line).FontSize(a.chatFont(11.5)).TextColor(p.faint).SingleLine()
			}
		}
		if showActions {
			a.blockActions(c, p, b)
		}
	})
}

// blockActions is the row under a message: copy its words or start a
// reply that quotes them. The pair sits on the message's own side, under
// the bubble or answer it acts on.
func (a *app) blockActions(c *ui.Context, p palette, b chat.Block) {
	justify := ui.Start
	if b.Kind == chat.KindUser {
		justify = ui.End
	}
	ui.Row(c).FillWidth().Justify(justify).Gap(2).Children(func() {
		a.blockAction(c, p, "Copy message", "Copy", icCopy, func() { a.copyMessage(b.Text) })
		a.blockAction(c, p, "Quote in reply", "Quote", icQuote, func() { a.quoteMessage(b.Text) })
	})
}

func (a *app) blockAction(c *ui.Context, p palette, label, text string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(24).Radius(6).Padding(0, 7).
		Center().Gap(4).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(on)
	b.Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(p.muted)
		ui.Text(c, text).FontSize(11.5).TextColor(p.muted).SingleLine()
	})
}

// copyMessage puts one message's words on the clipboard.
func (a *app) copyMessage(text string) {
	if err := a.runtime.copy(text); err != nil {
		a.notify(ui.Toast{Title: "Could not copy the message", Description: err.Error(),
			Type: components.ToastError})
		return
	}
	a.notify(ui.Toast{Title: "Message copied", Type: components.ToastSuccess})
}

// quoteMessage starts a reply that quotes the message line by line, after
// whatever the composer already held.
func (a *app) quoteMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	quoted := "> " + strings.ReplaceAll(text, "\n", "\n> ")
	if draft := strings.TrimRight(a.conversation.draft, "\n"); draft != "" {
		a.conversation.draft = draft + "\n\n" + quoted + "\n\n"
	} else {
		a.conversation.draft = quoted + "\n\n"
	}
	a.layout.composerFocus = true
}

// activityRow is one line the agent ran, opening its detail below it.
// It wears Onevra's activity surface: a bordered card whose header states
// the action and whose detail unfolds inside the same outline.
func (a *app) activityRow(c *ui.Context, p palette, b chat.Block) {
	open := a.layout.expanded[b.Key]
	c.Key("activity:" + b.Key)
	card := ui.Column(c).FillWidth().Radius(9).Clip()
	if open {
		card.Border(1, p.hairline).Background(c.Theme().Background.Mix(c.Theme().Text, .025))
	}
	card.Children(func() {
		row := ui.ButtonBase(c).Label(b.Text).Height(30).Radius(0).Padding(0, 9).
			FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
			Cursor(ui.CursorPointer).Tooltip(b.Text)
		if row.Hovered() {
			row.Background(p.hover)
		}
		row.OnClick(func() { a.layout.expanded[b.Key] = !a.layout.expanded[b.Key] })
		row.Children(func() {
			ui.Icon(c, toolIcon(b.Icon)).FontSize(12).TextColor(p.muted).Shrink(0)
			ui.Text(c, b.Text).FontSize(a.chatFont(12.5)).FontWeight(500).TextColor(p.muted).
				Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			chev := icChevRight
			if open {
				chev = icChevDown
			}
			ui.Icon(c, chev).FontSize(10).TextColor(p.faint).Shrink(0)
		})
		if open && b.Detail != nil {
			ui.Box(c).FillWidth().Height(1).Background(p.hairline)
			a.drawCommandDetail(c, p, b.Detail)
		}
	})
}

// permissionRow is one approval the agent raised mid-turn: what it wants
// to do, with the three decisions the server takes. It stays until the
// runner's reply lands, then the card leaves the transcript.
func (a *app) permissionRow(c *ui.Context, p palette, b chat.Block) {
	r := b.Request
	if r == nil {
		return
	}
	ui.Box(c).FillWidth().Radius(12).Padding(12, 14).Margin(4, 0, 0, 0).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icShield).FontSize(15).TextColor(p.orange)
				ui.Text(c, "Approval needed — "+permAsk(r.Action)).
					FontSize(a.chatFont(13)).FontWeight(600).SingleLine()
			})
			for _, res := range r.Resources {
				ui.Box(c).FillWidth().Radius(7).Padding(6, 10).
					Background(p.bg).Border(1, p.hairline).Children(func() {
					ui.Text(c, "$ "+res).Font("monospace").FontSize(a.chatFont(12)).LineHeight(1.5).
						TextColor(p.text).SingleLine()
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				allow := components.Button(c, "Allow once", components.ButtonPrimary)
				allow.Tooltip("Let it happen this turn.")
				if allow.Clicked() {
					a.answerPerm(r, "once")
				}
				always := components.Button(c, "Always", components.ButtonSecondary)
				always.Tooltip("Keep allowing this pattern.")
				if always.Clicked() {
					a.answerPerm(r, "always")
				}
				deny := components.Button(c, "Deny", components.ButtonGhost)
				deny.Tooltip("Refuse and let the agent carry on.")
				if deny.Clicked() {
					a.answerPerm(r, "reject")
				}
			})
			if len(r.Save) > 0 {
				ui.Text(c, "Always would remember "+strings.Join(r.Save, ", ")+".").
					FontSize(a.chatFont(12)).TextColor(p.muted).SingleLine()
			}
		})
	})
}

// permAsk words what a permission action would do, as the card's header.
func permAsk(action string) string {
	switch action {
	case "bash", "shell":
		return "run a shell command"
	case "edit":
		return "edit files in the workspace"
	case "webfetch":
		return "fetch from the web"
	}
	return "use " + action
}

// drawCommandDetail is the expanded detail of an activity: what ran, written
// as a terminal keeps it, inside the activity card's outline.
func (a *app) drawCommandDetail(c *ui.Context, p palette, lines []string) {
	ui.Box(c).FillWidth().Padding(9, 12).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			for _, l := range lines {
				ui.Text(c, l).Font("monospace").FontSize(a.chatFont(12)).LineHeight(1.55).
					TextColor(p.text).SingleLine()
			}
		})
	})
}

func toolIcon(kind string) *ui.SVG {
	switch kind {
	case "shell":
		return icTerminal
	case "read":
		return icBook
	default:
		return icPlug
	}
}

// draftPanels is the first screen of a new conversation: a mark, the
// question the chat starts from, three starts that fill the composer, and
// the two keys that send or break the line. A narrow chat column drops the
// blurbs and tightens the spacing so the composer keeps its room.
func (a *app) draftPanels(c *ui.Context, p palette) {
	title := "What should we work on?"
	if a.conversation.draftProject != "" {
		title = "What should we build in " + dirBase(a.conversation.draftProject) + "?"
	}
	compact := a.tightComposer(c)
	gap, mark, titleSize := float32(22), float32(58), float32(30)
	if compact {
		gap, mark, titleSize = 14, 46, 24
	}
	ui.Column(c).Fill().Center().Gap(gap).Padding(28, 24, 32, 24).Children(func() {
		ui.Box(c).Size(mark, mark).Radius(mark*.29).Center().
			Background(c.Theme().Selection.Alpha(.55)).Border(1, p.hairline).Children(func() {
			ui.Icon(c, icSparkle).FontSize(mark * .45).TextColor(p.orange)
		})
		ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(titleSize).FontWeight(600).TextColor(p.text).Wrap()
			ui.Text(c, "Describe the change you want. The agent reads the workspace, plans, and edits.").
				FontSize(14).TextColor(p.muted).LineHeight(1.4)
		})
		ui.Row(c).Wrap().Justify(ui.Center).Gap(10).MaxWidth(onechat.ContentWidth).Children(func() {
			a.suggestion(c, p, "Explain this codebase", "A guided tour of the app's pieces",
				"Explain this codebase", icBook, compact)
			a.suggestion(c, p, "Fix a failing test", "Run the tests, find the break, patch it",
				"Find the failing tests and fix them", icTerminal, compact)
			a.suggestion(c, p, "Review my changes", "Read the diff and flag the risks",
				"Review my uncommitted changes", icGitBranch, compact)
		})
		if !compact {
			ui.Text(c, "Enter sends  ·  Shift+Enter adds a line").FontSize(11.5).TextColor(p.faint)
		}
	})
}

// suggestion is one of the draft screen's starts: a card with an icon, a
// title and a line of what it asks for. Choosing it writes the prompt into
// the composer and puts the keyboard there. The compact face keeps the
// title alone, so three starts still fit beside a narrow chat.
func (a *app) suggestion(c *ui.Context, p palette, title, blurb, prompt string, icon *ui.SVG, compact bool) {
	width, height, chip := float32(224), float32(80), float32(30)
	if compact {
		width, height, chip = 214, 46, 24
	}
	b := ui.ButtonBase(c).Label(title+". "+blurb).Width(width).Height(height).Radius(12).
		Padding(10, 12).Justify(ui.Start).AlignItems(ui.Center).Gap(9).
		Background(c.Theme().Surface).Border(1, p.hairline).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.BorderColor(p.orange.Alpha(.55)).Background(p.hover)
	}
	b.OnClick(func() {
		a.conversation.draft = prompt
		a.layout.composerFocus = true
	})
	b.Children(func() {
		ui.Box(c).Size(chip, chip).Radius(chip * .3).Center().
			Background(c.Theme().Selection.Alpha(.5)).Children(func() {
			ui.Icon(c, icon).FontSize(chip * .5).TextColor(p.orange)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(p.text).SingleLine()
			if !compact {
				ui.Text(c, blurb).FontSize(11.5).TextColor(p.muted).LineHeight(1.3)
			}
		})
	})
}
