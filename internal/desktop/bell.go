package desktop

import (
	"fmt"
	"slices"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// bellChat is one chat the bell lists: a row of the scope's list with
// whether an approval is what keeps it marked.
type bellChat struct {
	session   agent.Session
	attention bool
}

// bellChats is the scope's unread chats in the order the server lists
// them, the ones waiting on an approval apart from the rest.
func (a *app) bellChats() (attention, unread []bellChat) {
	scope := a.scope()
	for _, s := range scope.sessions {
		if !scope.unread[s.ID] || scope.archived[s.ID] {
			continue
		}
		row := bellChat{session: s, attention: scope.attention[s.ID]}
		if row.attention {
			attention = append(attention, row)
		} else {
			unread = append(unread, row)
		}
	}
	return attention, unread
}

// pendingAsks is the open chat's approvals in a stable order, so the
// bell lists them the same way every frame.
func (a *app) pendingAsks() []*chat.Permission {
	pending := a.conversation.transcript.Pending()
	keys := make([]chat.PermissionID, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]*chat.Permission, 0, len(keys))
	for _, k := range keys {
		out = append(out, pending[k])
	}
	return out
}

// pendingApprovalPill is the composer's reminder that this chat waits on an
// allow or deny; choosing it brings the request into view. The decision
// itself stays on the card, so a stray Enter cannot approve anything.
func (a *app) pendingApprovalPill(c *ui.Context, p palette) {
	asks := a.pendingAsks()
	if len(asks) == 0 {
		return
	}
	label := "1 approval waiting"
	review := "Review the approval"
	if len(asks) > 1 {
		label = fmt.Sprintf("%d approvals waiting", len(asks))
		review = "Review the approvals"
	}
	ui.Row(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().
		Padding(0, 0, 4, 0).Children(func() {
		b := ui.ButtonBase(c).Label(review).Height(26).Radius(999).Padding(0, 11).
			Gap(6).Background(p.Orange.Alpha(.12)).Border(1, p.Orange.Alpha(.35)).
			Cursor(ui.CursorPointer).Tooltip(review)
		if b.Hovered() {
			b.Background(p.Orange.Alpha(.2))
		}
		b.OnClick(func() { a.revealApproval(asks[0]) })
		b.Children(func() {
			ui.Icon(c, icShield).FontSize(12).TextColor(p.Orange)
			ui.Text(c, label).FontSize(11.5).TextColor(p.Orange)
			ui.Text(c, "· Review").FontSize(11.5).TextColor(p.Orange.Alpha(.8))
		})
	})
}

// bellLabel names the bell for assistive technology: what the count is
// made of, not just how many.
func (a *app) bellLabel() string {
	asks := len(a.conversation.transcript.Pending())
	attention, unread := a.unreadCounts()
	switch {
	case asks > 0 && unread > 0:
		return fmt.Sprintf("Notifications, %d approvals waiting, %d unread chats", asks, unread)
	case asks > 0:
		return fmt.Sprintf("Notifications, %d approvals waiting", asks)
	case attention > 0 && attention == unread:
		return fmt.Sprintf("Notifications, %d unread chats needing attention", unread)
	case attention > 0:
		return fmt.Sprintf("Notifications, %d unread chats, %d needing attention", unread, attention)
	case unread > 0:
		return fmt.Sprintf("Notifications, %d unread chats", unread)
	}
	return "Notifications"
}

// bellPanel is the notification center under the bell: approvals waiting
// in this chat, chats needing attention, unread chats, and the action
// that clears the news the user has seen.
func (a *app) bellPanel(c *ui.Context, p palette, anchor ui.Element) {
	asks := a.pendingAsks()
	attention, unread := a.bellChats()
	components.Popover(c, anchor, &a.layout.bellOpen, func(panel ui.Element) {
		panel.Width(300).MaxHeight(430).Children(func() {
			ui.Row(c).FillWidth().Height(26).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Notifications").FontSize(13).FontWeight(600).TextColor(p.Text)
				ui.Spacer(c)
				if n := len(asks) + len(attention) + len(unread); n > 0 {
					ui.Text(c, fmt.Sprintf("%d", n)).FontSize(11).TextColor(p.Faint)
				}
			})
			ui.Scroll(c).FillWidth().MaxHeight(300).Gap(1).Children(func() {
				if len(asks) > 0 {
					a.bellSection(c, p, "Waiting in this chat")
					for _, perm := range asks {
						a.bellAskRow(c, p, perm)
					}
				}
				if len(attention) > 0 {
					a.bellSection(c, p, "Needs attention")
					for _, row := range attention {
						a.bellChatRow(c, p, row)
					}
				}
				if len(unread) > 0 {
					a.bellSection(c, p, "Unread")
					for _, row := range unread {
						a.bellChatRow(c, p, row)
					}
				}
				if len(asks)+len(attention)+len(unread) == 0 {
					ui.Column(c).FillWidth().Padding(18, 8, 14, 8).Center().Gap(6).Children(func() {
						ui.Icon(c, icBell).FontSize(20).TextColor(p.Faint)
						ui.Text(c, "You're all caught up.").FontSize(12.5).FontWeight(500).
							TextColor(p.Text).SingleLine()
						ui.Text(c, "Chats that finish or need an approval while you are elsewhere gather here.").
							FontSize(11.5).LineHeight(1.4).TextColor(p.Muted)
					})
				}
			})
			if len(unread) > 0 {
				ui.Divider(c)
				if components.Button(c, "Mark all read", components.ButtonGhost).FillWidth().Clicked() {
					a.markAllRead()
				}
			}
		})
	})
}

// bellSection names a group of rows inside the panel.
func (a *app) bellSection(c *ui.Context, p palette, title string) {
	ui.Text(c, title).FontSize(10.5).FontWeight(600).TextColor(p.Faint).
		Padding(7, 8, 3, 8).SingleLine()
}

// bellChatRow opens one marked chat. The dot says why it is marked; a
// shield means its turn is waiting on the user.
func (a *app) bellChatRow(c *ui.Context, p palette, row bellChat) {
	title := row.session.Title
	if title == "" {
		title = "New chat"
	}
	label := "Open " + title
	if row.attention {
		label += ", needs attention"
	}
	c.Key("bell:" + row.session.ID)
	b := ui.ButtonBase(c).Label(label).Height(38).Radius(7).Padding(0, 8).
		FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer).Tooltip(row.session.Directory)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.openBellChat(row.session) })
	b.Children(func() {
		if row.attention {
			ui.Icon(c, icShield).FontSize(12).TextColor(p.Orange).Shrink(0)
		} else {
			unreadDot(c, p, false)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, title).FontSize(12.5).TextColor(p.Text).SingleLine().Ellipsis("…")
			if dir := dirBase(row.session.Directory); dir != "" {
				ui.Text(c, dir).FontSize(10.5).TextColor(p.Faint).SingleLine().Ellipsis("…")
			}
		})
		if row.attention {
			ui.Text(c, "Approval").FontSize(10).TextColor(p.Orange).Shrink(0)
		}
	})
}

// bellAskRow scrolls the open chat to one approval it is waiting on.
func (a *app) bellAskRow(c *ui.Context, p palette, perm *chat.Permission) {
	c.Key("bell-ask:" + string(perm.ID))
	b := ui.ButtonBase(c).Label("Show the approval: "+permAsk(perm.Action)).
		Height(38).Radius(7).Padding(0, 8).FillWidth().Justify(ui.Start).
		AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.revealApproval(perm) })
	b.Children(func() {
		ui.Icon(c, icShield).FontSize(12).TextColor(p.Danger).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, "Approval needed").FontSize(12).TextColor(p.Text).SingleLine()
			ui.Text(c, permAsk(perm.Action)).FontSize(10.5).TextColor(p.Muted).
				SingleLine().Ellipsis("…")
		})
	})
}

// revealApproval closes the panel and brings the approval's card into
// view in the transcript.
func (a *app) revealApproval(perm *chat.Permission) {
	a.layout.bellOpen = false
	for i := 0; i < a.conversation.transcript.Len(); i++ {
		row := a.conversation.transcript.Row(i)
		if row.Kind == chat.KindPermission && row.Request != nil && row.Request.ID == perm.ID {
			a.layout.feed.ScrollTo(i, ui.Center)
			return
		}
	}
	a.layout.feed.ScrollToEnd()
}

// openBellChat closes the panel and brings one marked chat on show; the
// marks clear as its transcript opens.
func (a *app) openBellChat(s agent.Session) {
	a.layout.bellOpen = false
	a.openConversation(s)
}

// markAllRead clears the unread marks, keeping the chats whose turn still
// waits on an approval: that news is not the user's to dismiss while the
// agent is blocked.
func (a *app) markAllRead() {
	scope := a.scope()
	if len(scope.unread) == len(scope.attention) {
		return
	}
	kept := make(map[string]bool, len(scope.attention))
	for id := range scope.attention {
		kept[id] = true
	}
	scope.unread = kept
	a.saveState()
	if a.runtime.clearNotices != nil {
		a.runtime.clearNotices()
	}
	a.layout.bellOpen = false
	a.notify(ui.Toast{Title: "Unread chats marked read"})
}
