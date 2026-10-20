package main

import (
	"fmt"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// toggleActivity shows the Activity view beside the chat, or folds the
// tools pane when that view is already on show.
func (a *app) toggleActivity() {
	if !a.layout.browserCollapsed && a.layout.browserTool == "Activity" {
		a.layout.browserCollapsed = true
		return
	}
	a.openTool("Activity")
}

// activityBody is the Activity view: what the turn and the conversation
// have run, newest first, with the detail the transcript folds away. It
// reads the transcript directly, so the view stays in step with streaming
// without a second stream of events.
func (a *app) activityBody(c *ui.Context, p palette) {
	entries := a.activityEntries()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf("%d steps", len(entries))).FontSize(a.chatFont(12)).
			TextColor(p.faint).SingleLine()
		ui.Spacer(c)
		if a.turnRunning() {
			ui.Box(c).Size(7, 7).Radius(999).Background(p.orange)
			ui.Text(c, "Live").FontSize(11.5).TextColor(p.muted)
		}
	})
	if len(entries) == 0 {
		if a.turnWorking() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				onechat.Shimmer(c, "Working…", a.chatFont(13), p.muted, p.text)
			})
			return
		}
		ui.Text(c, "Nothing has run in this chat yet.").FontSize(12.5).TextColor(p.muted)
		ui.Text(c, "Shell commands, file edits and approvals appear here as the agent works.").
			FontSize(12).LineHeight(1.45).TextColor(p.faint)
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Gap(3).Children(func() {
		if a.turnWorking() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(4, 8).Children(func() {
				onechat.Shimmer(c, "Working…", a.chatFont(12.5), p.muted, p.text)
			})
		}
		for _, entry := range entries {
			a.activityEntry(c, p, entry)
		}
	})
}

// activityEntry is one line of the view. Activity and permission rows are
// the transcript's own blocks, read by value so the stream can keep
// writing behind the panel.
type activityEntry struct {
	block chat.Block
}

// activityEntries gathers the rows the Activity view lists, newest first.
func (a *app) activityEntries() []activityEntry {
	entries := []activityEntry{}
	for i := a.conversation.transcript.Len() - 1; i >= 0; i-- {
		block := a.conversation.transcript.Row(i)
		switch block.Kind {
		case chat.KindActivity, chat.KindPermission, chat.KindNote:
			entries = append(entries, activityEntry{block: block})
		case chat.KindText, chat.KindUser, chat.KindStream, chat.KindWorking:
			// Answers and the loader describe the conversation, not the
			// work this view gathers.
		}
	}
	return entries
}

func (a *app) activityEntry(c *ui.Context, p palette, entry activityEntry) {
	block := entry.block
	switch block.Kind {
	case chat.KindNote:
		ui.Row(c).FillWidth().Padding(4, 8).Children(func() {
			ui.Text(c, block.Text).FontSize(a.chatFont(12.5)).LineHeight(1.45).TextColor(p.faint)
		})
		return
	case chat.KindPermission:
		a.activityPermission(c, p, block)
		return
	case chat.KindActivity:
	case chat.KindText, chat.KindUser, chat.KindStream, chat.KindWorking:
		return
	}
	open := block.Key != "" && a.layout.expanded[block.Key]
	if block.Key != "" {
		c.Key("activity-pane:" + block.Key)
	}
	card := ui.Column(c).FillWidth().Radius(8).Clip()
	if open {
		card.Background(c.Theme().Background.Mix(c.Theme().Text, .025)).Border(1, p.hairline)
	}
	card.Children(func() {
		row := ui.ButtonBase(c).Label("Activity step: "+block.Text).Height(30).Radius(0).Padding(0, 8).
			FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
			Cursor(ui.CursorPointer).Tooltip(block.Text)
		if row.Hovered() {
			row.Background(p.hover)
		}
		row.OnClick(func() {
			if block.Key != "" {
				a.layout.expanded[block.Key] = !a.layout.expanded[block.Key]
			}
		})
		row.Children(func() {
			ui.Icon(c, toolIcon(block.Icon)).FontSize(12).TextColor(p.muted).Shrink(0)
			ui.Text(c, block.Text).FontSize(a.chatFont(12.5)).FontWeight(500).TextColor(p.muted).
				Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			chev := icChevRight
			if open {
				chev = icChevDown
			}
			ui.Icon(c, chev).FontSize(10).TextColor(p.faint).Shrink(0)
		})
		if open && block.Detail != nil {
			ui.Box(c).FillWidth().Height(1).Background(p.hairline)
			a.drawCommandDetail(c, p, block.Detail)
		}
	})
}

// activityPermission is an approval waiting in the turn: its action and
// the paths or commands it names, without duplicating the answer buttons
// that belong in the transcript.
func (a *app) activityPermission(c *ui.Context, p palette, block chat.Block) {
	request := block.Request
	if request == nil {
		return
	}
	ui.Column(c).FillWidth().Radius(8).Padding(7, 9).Gap(5).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, icShield).FontSize(13).TextColor(p.orange)
			ui.Text(c, "Approval needed — "+permAsk(request.Action)).
				FontSize(a.chatFont(12.5)).FontWeight(500).TextColor(p.text).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
		for _, resource := range request.Resources {
			ui.Text(c, "$ "+resource).Font("monospace").FontSize(a.chatFont(11)).
				TextColor(p.muted).SingleLine().Ellipsis("…")
		}
	})
}
