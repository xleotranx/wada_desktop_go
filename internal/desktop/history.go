package desktop

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// historySessions is every chat this app has listed, newest first, with
// archived chats and repeats left out.
func (a *app) historySessions() []agent.Session {
	out := make([]agent.Session, 0, len(a.scope().sessions))
	seen := map[string]bool{}
	for _, s := range a.scope().sessions {
		if s.ID == "" || a.chatArchived(s.ID) || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(one, two agent.Session) int {
		switch {
		case one.Updated > two.Updated:
			return -1
		case one.Updated < two.Updated:
			return 1
		}
		return 0
	})
	return out
}

// historyRows narrows the history to the filter field's words.
func (a *app) historyRows() []agent.Session {
	sessions := a.historySessions()
	query := strings.ToLower(strings.TrimSpace(a.layout.historyQuery))
	if query == "" {
		return sessions
	}
	out := make([]agent.Session, 0, len(sessions))
	for _, s := range sessions {
		if strings.Contains(strings.ToLower(s.Title), query) ||
			strings.Contains(strings.ToLower(s.Directory), query) {
			out = append(out, s)
		}
	}
	return out
}

// openHistory shows the whole history the app has read, newest first, and
// a way to ask the server for older pages.
func (a *app) openHistory() {
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.dismissModuleDialogs()
	a.layout.historyOpen = true
	a.layout.historyFocus = true
	a.layout.historyQuery = ""
	a.layout.historySelected = 0
	a.layout.historyList.ScrollTo(0, ui.Start)
}

// openHistorySession leaves the history for one chat.
func (a *app) openHistorySession(s agent.Session) {
	a.layout.historyOpen = false
	a.layout.historyQuery = ""
	a.openConversation(s)
}

// loadOlderChats asks the server for the page after the newest one read.
func (a *app) loadOlderChats() {
	cursor := a.scope().nextCursor
	if cursor == "" || a.scope().moreLoading {
		return
	}
	if a.queueCommand(runner.More{Cursor: cursor}) == runner.Accepted {
		a.scope().moreLoading = true
	}
}

// historyWhenAt words how long ago a chat last moved.
func historyWhenAt(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms)
	age := now.Sub(t)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	case age < 48*time.Hour:
		return "yesterday"
	case age < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(age.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

// historyWhen is historyWhenAt against the clock of this frame.
func historyWhen(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return historyWhenAt(ms, time.Now())
}

// historyDialog is the centered list of chats: filter, arrows and Enter,
// with the server's older pages one button away.
func (a *app) historyDialog(c *ui.Context, p palette) {
	if !a.layout.historyOpen {
		return
	}
	rows := a.historyRows()
	dialog(c, p, &a.layout.historyOpen, 660, "Chat history", func() {
		ui.Text(c, "Chat history").FontSize(18).FontWeight(600).TextColor(p.Text)
		ui.Text(c, "Every chat this app has listed, newest first.").FontSize(12.5).
			TextColor(p.Muted)
		in := components.TextBox(c, &a.layout.historyQuery,
			components.TextBoxOptions{LeadingIcon: icSearch}).
			FillWidth().Label("Filter history").Placeholder("Filter chats").
			FontSize(13).OnTextKey(func(k ui.TextKey) bool {
			if len(rows) == 0 {
				return false
			}
			if k.Key == ui.KeyEnter {
				index := min(max(a.layout.historySelected, 0), len(rows)-1)
				a.openHistorySession(rows[index])
				return true
			}
			if k.Key == ui.KeyDown {
				a.layout.historySelected = min(a.layout.historySelected+1, len(rows)-1)
				a.layout.historyList.ScrollTo(a.layout.historySelected, ui.Center)
				return true
			}
			if k.Key == ui.KeyUp {
				a.layout.historySelected = max(a.layout.historySelected-1, 0)
				a.layout.historyList.ScrollTo(a.layout.historySelected, ui.Center)
				return true
			}
			return false
		})
		if in.Changed() {
			a.layout.historySelected = 0
		}
		if a.layout.historyFocus {
			in.Focus()
			a.layout.historyFocus = false
		}
		if a.layout.historySelected >= len(rows) {
			a.layout.historySelected = max(0, len(rows)-1)
		}
		if len(rows) == 0 {
			text := "No chats yet."
			if strings.TrimSpace(a.layout.historyQuery) != "" {
				text = "No chat matches."
			}
			ui.Text(c, text).FontSize(13).TextColor(p.Muted).Margin(14, 0)
		} else {
			ui.List(c.Key("history-list"), &a.layout.historyList, len(rows)).
				MaxHeight(360).FillWidth().Gap(1).
				ItemKey(func(i int) any { return rows[i].ID }).
				Rows(func(row ui.ListRow) {
					a.historyRow(row.Context, p, rows[row.Index], row.Index)
				})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, fmt.Sprintf("%d chats", len(rows))).FontSize(11).
				TextColor(p.Faint).SingleLine()
			ui.Spacer(c)
			if a.scope().nextCursor != "" && strings.TrimSpace(a.layout.historyQuery) == "" {
				label := "Load older chats"
				if a.scope().moreLoading {
					label = "Loading older chats…"
				}
				load := components.Button(c, label, components.ButtonSecondary)
				load.Disabled(a.scope().moreLoading)
				if load.Clicked() {
					a.loadOlderChats()
				}
			} else {
				ui.Text(c, "↑↓ choose · Enter opens").FontSize(11).TextColor(p.Faint).
					SingleLine()
			}
		})
	})
}

// historyRow is one chat in the history: its name, folder and how long ago
// it moved.
func (a *app) historyRow(c *ui.Context, p palette, s agent.Session, index int) {
	title := s.Title
	if title == "" {
		title = "New chat"
	}
	when := historyWhen(s.Updated)
	sub := when
	if s.Directory != "" {
		sub = dirBase(s.Directory)
		if when != "" {
			sub += " · " + when
		}
	}
	c.Key("history:" + s.ID)
	b := ui.ButtonBase(c).Label("Open chat "+title).Height(38).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(p.Hover)
	} else if index == a.layout.historySelected {
		b.Background(p.Selected)
	}
	b.OnClick(func() { a.openHistorySession(s) })
	b.Children(func() {
		ui.Icon(c, icChat).FontSize(13).TextColor(p.Muted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, title).FontSize(13).TextColor(p.Text).SingleLine().
				Ellipsis("…").MinWidth(0)
			if sub != "" {
				ui.Text(c, sub).FontSize(11).TextColor(p.Faint).SingleLine().MinWidth(0)
			}
		})
		if a.scope().unread[s.ID] {
			unreadDot(c, p, a.scope().attention[s.ID])
		}
		if s.ID == string(a.conversation.transcript.Busy()) {
			ui.Spinner(c).Size(12, 12).Label("Running")
		}
	})
}
