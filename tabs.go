package main

import (
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// chatTabLimit bounds the strip: enough for a working set, few enough that
// every title keeps a readable slice of the row.
const chatTabLimit = 8

// tabStripHeight is the tab row above the transcript.
const tabStripHeight float32 = 33

// rememberTab puts a conversation in this server's strip, or refreshes the
// title of the one already there. The oldest tab steps aside for a ninth.
func (a *app) rememberTab(s opencode.Session) {
	title := s.Title
	if title == "" {
		title = "New chat"
	}
	scope := a.scope()
	for i := range scope.tabs {
		if scope.tabs[i].id == s.ID {
			scope.tabs[i].title = title
			return
		}
	}
	scope.tabs = append(scope.tabs, chatTab{id: s.ID, title: title})
	if len(scope.tabs) > chatTabLimit {
		scope.tabs = scope.tabs[1:]
	}
}

// tabTitle reads a tab's name from the freshest list the server sent, so a
// rename lands in the strip without a second copy of the title.
func (a *app) tabTitle(id string) string {
	scope := a.scope()
	for _, s := range scope.sessions {
		if s.ID == id {
			if s.Title == "" {
				return "New chat"
			}
			return s.Title
		}
	}
	for _, tab := range scope.tabs {
		if tab.id == id {
			return tab.title
		}
	}
	return "Chat"
}

// openTab brings one tab's conversation on show, by its row or by the ID
// the store still answers for. It reports whether the conversation was
// admitted, so a close can fall back to a draft rather than leave a
// removed tab's transcript on screen.
func (a *app) openTab(id string) bool {
	if id == "" {
		return false
	}
	if id == string(a.conversation.target.session) {
		return true
	}
	for _, s := range a.scope().sessions {
		if s.ID == id {
			return a.openConversation(s)
		}
	}
	return a.openConversation(opencode.Session{ID: id, Title: a.tabTitle(id)})
}

// cycleTab moves the view through the strip, wrapping at either end. With
// no open tab it lands on the first one.
func (a *app) cycleTab(step int) {
	tabs := a.scope().tabs
	if len(tabs) < 2 {
		return
	}
	at := -1
	for i, tab := range tabs {
		if tab.id == string(a.conversation.target.session) {
			at = i
			break
		}
	}
	if at < 0 {
		a.openTab(tabs[0].id)
		return
	}
	a.openTab(tabs[(at+step+len(tabs))%len(tabs)].id)
}

// closeTab takes a tab out of the strip. Closing the open tab hands the
// view to its neighbour, or to a fresh draft when it was the last.
func (a *app) closeTab(id string) {
	scope := a.scope()
	index := -1
	for i, tab := range scope.tabs {
		if tab.id == id {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	scope.tabs = removeTab(scope.tabs, id)
	if id != string(a.conversation.target.session) {
		return
	}
	if len(scope.tabs) > 0 {
		if a.openTab(scope.tabs[min(index, len(scope.tabs)-1)].id) {
			return
		}
	}
	a.startNewChat()
}

// removeTab drops one ID from a strip without disturbing the rest.
func removeTab(tabs []chatTab, id string) []chatTab {
	for i, tab := range tabs {
		if tab.id == id {
			return append(tabs[:i], tabs[i+1:]...)
		}
	}
	return tabs
}

// chatTabs is the strip of open conversations above the transcript: each
// tab opens its chat, its cross closes it, and the plus starts a draft.
// One open conversation already names itself in the title bar, so the
// strip waits for a second and keeps the single-chat page chrome-free.
func (a *app) chatTabs(c *ui.Context, p palette) {
	tabs := a.scope().tabs
	if len(tabs) <= 1 {
		return
	}
	ui.Row(c).FillWidth().Height(tabStripHeight).Padding(0, 8).Gap(2).
		AlignItems(ui.Center).Children(func() {
		for _, tab := range tabs {
			a.chatTab(c, p, tab)
		}
		ui.Spacer(c)
		plus := ui.ButtonBase(c).Label("New chat tab").Size(24, 24).Radius(6).
			Center().Tooltip("New chat")
		if plus.Hovered() {
			plus.Background(p.hover)
		}
		plus.OnClick(a.startNewChat)
		plus.Children(func() {
			ui.Icon(c, icPlus).FontSize(13).TextColor(p.muted)
		})
	})
	ui.Box(c).FillWidth().Height(1).Background(p.hairline)
}

// chatTab is one tab: the title takes the press, the cross closes, and the
// open tab wears the selected face.
func (a *app) chatTab(c *ui.Context, p palette, tab chatTab) {
	title := a.tabTitle(tab.id)
	active := string(a.conversation.target.session) == tab.id
	c.Key("tab:" + tab.id)
	row := ui.Row(c).Height(28).MaxWidth(200).Shrink(1).MinWidth(0).
		Radius(7).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.selected)
	} else if row.Hovered() {
		row.Background(p.hover)
	}
	row.Children(func() {
		open := ui.ButtonBase(c).Label("Open "+title).Height(28).Radius(7).
			Padding(0, 8).Justify(ui.Start).Grow(1).Shrink(1).MinWidth(0)
		open.OnClick(func() { a.openTab(tab.id) })
		open.Children(func() {
			tint := p.muted
			if active {
				tint = p.text
			}
			ui.Text(c, title).FontSize(12.5).TextColor(tint).SingleLine().Ellipsis("…")
		})
		close := ui.ButtonBase(c).Label("Close "+title).Size(20, 20).Radius(5).Center().
			Tooltip("Close " + title)
		if close.Hovered() {
			close.Background(p.hover)
		}
		close.OnClick(func() { a.closeTab(tab.id) })
		close.Children(func() {
			ui.Icon(c, icClose).FontSize(10.5).TextColor(p.faint)
		})
	})
}
