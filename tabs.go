package main

import (
	"slices"
	"strings"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// chatTabLimit bounds the strip: enough for a working set, few enough that
// every title keeps a readable slice of the row. closedTabLimit bounds the
// history Cmd+Shift+T reopens.
const (
	chatTabLimit   = 8
	closedTabLimit = 12
)

// tabStripHeight is the tab row above the transcript.
const tabStripHeight float32 = 33

// rememberTab puts a conversation in this server's strip, or refreshes the
// title of the one already there. The oldest tab steps aside for a ninth,
// and a conversation opened again leaves the redo history.
func (a *app) rememberTab(s opencode.Session) {
	title := s.Title
	if title == "" {
		title = "New chat"
	}
	scope := a.scope()
	scope.closedTabs = removeTab(scope.closedTabs, s.ID)
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
	a.revealTab(s.ID)
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
			if !a.openConversation(s) {
				return false
			}
			a.revealTab(id)
			return true
		}
	}
	if !a.openConversation(opencode.Session{ID: id, Title: a.tabTitle(id)}) {
		return false
	}
	a.revealTab(id)
	return true
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

// reopenClosedTab brings back the newest tab a cross removed, with its
// title, and opens it.
func (a *app) reopenClosedTab() {
	scope := a.scope()
	if len(scope.closedTabs) == 0 {
		return
	}
	last := scope.closedTabs[len(scope.closedTabs)-1]
	scope.closedTabs = scope.closedTabs[:len(scope.closedTabs)-1]
	a.rememberTab(opencode.Session{ID: last.id, Title: last.title})
	a.openTab(last.id)
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
	a.forgetTab(id)
	if id != string(a.conversation.target.session) {
		a.saveState()
		return
	}
	if len(scope.tabs) > 0 {
		if a.openTab(scope.tabs[min(index, len(scope.tabs)-1)].id) {
			return
		}
	}
	a.startNewChat()
}

// forgetTab takes a tab out of the strip and keeps it for the reopen
// history, without choosing what the view shows next.
func (a *app) forgetTab(id string) {
	scope := a.scope()
	if !slices.ContainsFunc(scope.tabs, func(tab chatTab) bool { return tab.id == id }) {
		return
	}
	a.rememberClosed(chatTab{id: id})
	scope.tabs = removeTab(scope.tabs, id)
	if scope.activeTab == id {
		scope.activeTab = ""
	}
}

// rememberClosed adds one tab to the reopen history, newest last and the
// history's oldest stepped aside past its limit.
func (a *app) rememberClosed(tab chatTab) {
	scope := a.scope()
	scope.closedTabs = append(scope.closedTabs, chatTab{id: tab.id, title: a.tabTitle(tab.id)})
	if len(scope.closedTabs) > closedTabLimit {
		scope.closedTabs = scope.closedTabs[1:]
	}
}

// closeOtherTabs closes every tab but one and shows that conversation.
// The strip is built from new slices rather than shortened in place: a
// context menu's choice runs while the strip's frame is still iterating
// it, and shortening the same array would draw a tab twice.
func (a *app) closeOtherTabs(keep string) {
	scope := a.scope()
	kept := make([]chatTab, 0, 1)
	for _, tab := range slices.Clone(scope.tabs) {
		if tab.id == keep {
			kept = append(kept, tab)
			continue
		}
		a.rememberClosed(tab)
	}
	scope.tabs = kept
	if scope.activeTab != keep {
		scope.activeTab = ""
	}
	if string(a.conversation.target.session) != keep {
		if !a.openTab(keep) {
			a.startNewChat()
		}
	}
	a.saveState()
}

// closeAllTabs puts every tab away, keeping them for the reopen history,
// and starts a fresh chat.
func (a *app) closeAllTabs() {
	scope := a.scope()
	for _, tab := range slices.Clone(scope.tabs) {
		a.rememberClosed(tab)
	}
	scope.tabs = nil
	scope.activeTab = ""
	a.startNewChat()
}

// moveTabTo puts a dragged tab where another one sits, the rest keeping
// their order. The target's slot is where the dragged tab lands, so a tab
// dragged right over its neighbour swaps with it.
func (a *app) moveTabTo(id, target string) {
	scope := a.scope()
	from, to := -1, -1
	for i, tab := range scope.tabs {
		if tab.id == id {
			from = i
		}
		if tab.id == target {
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	tabs := slices.Clone(scope.tabs)
	tab := tabs[from]
	tabs = slices.Delete(tabs, from, from+1)
	tabs = slices.Insert(tabs, min(to, len(tabs)), tab)
	scope.tabs = tabs
	a.saveState()
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
	// A tab's context menu acts while this frame still walks the strip, so
	// the walk keeps its own copy: changing the strip must not move or
	// repeat the rows being drawn.
	tabs := slices.Clone(a.scope().tabs)
	if len(tabs) <= 1 {
		return
	}
	ui.Row(c).FillWidth().Height(tabStripHeight).Padding(0, 8).Gap(2).
		AlignItems(ui.Center).Children(func() {
		if a.layout.tabScroll.MaxX > 0 {
			a.tabScrollButton(c, p, "Scroll tabs left", icChevLeft, -240, a.layout.tabScroll.X <= 0)
		}
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Height(tabStripHeight).
			TrackScroll(&a.layout.tabScroll).Children(func() {
			ui.Row(c).Height(tabStripHeight).Padding(0, 2).Gap(2).
				AlignItems(ui.Center).Children(func() {
				for _, tab := range tabs {
					a.chatTab(c, p, tab)
				}
			})
		})
		if a.layout.tabScroll.MaxX > 0 {
			a.tabScrollButton(c, p, "Scroll tabs right", icChevRight, 240,
				a.layout.tabScroll.X >= a.layout.tabScroll.MaxX)
		}
		plus := ui.ButtonBase(c).Label("New chat tab").Size(24, 24).Radius(6).
			Center().MarginStart(2).Tooltip("New chat")
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

// tabScrollButton moves the tab strip a page, disabled at its edge.
func (a *app) tabScrollButton(c *ui.Context, p palette, label string, icon *ui.SVG, step float32, disabled bool) {
	b := ui.ButtonBase(c).Label(label).Size(22, 22).Radius(5).Center().
		Disabled(disabled).Cursor(ui.CursorPointer)
	if b.Hovered() && !disabled {
		b.Background(p.hover)
	}
	b.OnClick(func() {
		x := min(max(a.layout.tabScroll.X+step, 0), a.layout.tabScroll.MaxX)
		a.layout.tabScroll.ScrollTo(x, 0, ui.Smooth)
	})
	b.Children(func() { ui.Icon(c, icon).FontSize(12).TextColor(p.muted) })
}

// revealTab scrolls the strip to the tab with this ID, keeping a tab
// opened by cycling or creating in sight.
func (a *app) revealTab(id string) {
	for i, tab := range a.scope().tabs {
		if tab.id != id {
			continue
		}
		a.layout.tabScroll.ScrollTo(float32(i)*172, 0, ui.Smooth)
		return
	}
}

// chatTab is one tab: the title takes the press, the cross closes, and the
// open tab wears the selected face.
func (a *app) chatTab(c *ui.Context, p palette, tab chatTab) {
	title := a.tabTitle(tab.id)
	active := string(a.conversation.target.session) == tab.id
	c.Key("tab:" + tab.id)
	row := ui.Row(c).Height(28).Width(170).Shrink(0).
		Radius(7).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.selected)
	} else if row.Hovered() {
		row.Background(p.hover)
	}
	row.Drag(tab.id)
	if dragged, over := ui.DragOver[string](row); over && dragged != tab.id {
		row.Background(p.hover).Border(1, p.orange.Alpha(.55))
	}
	if dragged, ok := ui.Drop[string](row); ok {
		a.moveTabTo(dragged, tab.id)
	}
	if row.Dragging() {
		row.Opacity(.55)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Close tab").Chosen() {
			a.closeTab(tab.id)
		}
		if m.Item("Close other tabs").Disabled(len(a.scope().tabs) <= 1).Chosen() {
			a.closeOtherTabs(tab.id)
		}
		if m.Item("Close all tabs").Chosen() {
			a.closeAllTabs()
		}
		m.Separator()
		if m.Item("Reopen closed tab").Disabled(len(a.scope().closedTabs) == 0).Chosen() {
			a.reopenClosedTab()
		}
	})
	row.Children(func() {
		open := ui.ButtonBase(c).Label("Open "+title).Height(28).Radius(7).
			Padding(0, 8).Justify(ui.Start).Grow(1).Shrink(1).MinWidth(0)
		open.OnClick(func() { a.openTab(tab.id) })
		open.Children(func() {
			tint := p.muted
			if active {
				tint = p.text
			}
			if draft := a.draftFor(tab.id); strings.TrimSpace(draft) != "" {
				ui.Icon(c, icPen).FontSize(10).MarginEnd(5).TextColor(p.faint).
					Tooltip("Draft: " + draftSnippet(draft))
			}
			ui.Text(c, title).FontSize(12.5).TextColor(tint).SingleLine().Ellipsis("…")
			if scope := a.scope(); scope.unread[tab.id] {
				unreadDot(c, p, scope.attention[tab.id])
			}
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
