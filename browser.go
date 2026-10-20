package main

import (
	"net/url"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// paneTab is one tab of the right pane: a tool or a page preview.
type paneTab struct {
	kind    string // Files, Changes, Activity, New page, or page
	address string // the page's address, empty for tools
}

// paneTabTitle names a tab in the strip.
func paneTabTitle(tab paneTab) string {
	if tab.kind == "page" {
		return pageTitle(tab.address)
	}
	return tab.kind
}

// pageTitle shortens an address to the host the tab can show.
func pageTitle(address string) string {
	if address == "" {
		return "New page"
	}
	if parsed, err := url.Parse(address); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return address
}

// paneTabActive reports whether a tab is the one the pane shows.
func (a *app) paneTabActive(tab paneTab) bool {
	if tab.kind == "page" {
		return a.layout.browserTool == "" && a.layout.browserPage == tab.address
	}
	return a.layout.browserTool == tab.kind && a.layout.browserPage == ""
}

// activePaneTabIndex is the strip position of the tab on show, -1 at home.
func (a *app) activePaneTabIndex() int {
	for i, tab := range a.layout.paneTabs {
		if a.paneTabActive(tab) {
			return i
		}
	}
	return -1
}

// ensurePaneTab finds or appends a tab for a tool or address.
func (a *app) ensurePaneTab(kind, address string) int {
	for i, tab := range a.layout.paneTabs {
		if tab.kind == kind && (kind != "page" || tab.address == address) {
			return i
		}
	}
	a.layout.paneTabs = append(a.layout.paneTabs, paneTab{kind: kind, address: address})
	return len(a.layout.paneTabs) - 1
}

// activatePaneTab brings one tab back, loading its tool when it has one.
func (a *app) activatePaneTab(tab paneTab) {
	if tab.kind == "page" {
		a.layout.browserTool = ""
		a.layout.browserPage = tab.address
		a.layout.browserAddress = tab.address
		return
	}
	a.openTool(tab.kind)
}

// closePaneTab removes a tab. Closing the active tab shows its neighbour,
// and the last one folds the pane back to the tools home.
func (a *app) closePaneTab(index int) {
	if index < 0 || index >= len(a.layout.paneTabs) {
		return
	}
	active := a.paneTabActive(a.layout.paneTabs[index])
	a.layout.paneTabs = append(a.layout.paneTabs[:index], a.layout.paneTabs[index+1:]...)
	if !active {
		return
	}
	if len(a.layout.paneTabs) == 0 {
		a.layout.browserTool, a.layout.browserPage, a.layout.browserAddress = "", "", ""
		a.layout.browserCollapsed = true
		return
	}
	a.activatePaneTab(a.layout.paneTabs[min(index, len(a.layout.paneTabs)-1)])
}

// showPageAddress points the active page tab at another address, turning
// a fresh New page tab into that page.
func (a *app) showPageAddress(address string) {
	index := a.activePaneTabIndex()
	if index >= 0 {
		tab := &a.layout.paneTabs[index]
		if tab.kind == "page" || tab.kind == "New page" {
			tab.kind, tab.address = "page", address
		} else {
			a.ensurePaneTab("page", address)
		}
	} else {
		a.ensurePaneTab("page", address)
	}
	a.layout.browserTool = ""
	a.layout.browserPage = address
	a.layout.browserAddress = address
}

// closePaneTabsExcept leaves one tab and closes the rest, showing the one
// that stayed.
func (a *app) closePaneTabsExcept(index int) {
	if index < 0 || index >= len(a.layout.paneTabs) {
		return
	}
	kept := a.layout.paneTabs[index]
	a.layout.paneTabs = []paneTab{kept}
	a.activatePaneTab(kept)
}

// closeAllPaneTabs folds the pane as closing its last tab does.
func (a *app) closeAllPaneTabs() {
	a.layout.paneTabs = nil
	a.layout.browserTool, a.layout.browserPage, a.layout.browserAddress = "", "", ""
	a.layout.browserCollapsed = true
}

// browserPane is the native tools and page surface beside the conversation.
func (a *app) browserPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).FillWidth().FillHeight().Background(p.bg).Children(func() {
		a.paneTabsBar(c, p)
		a.browserToolbar(c, p, headerH)
		ui.Box(c).FillWidth().Height(1).Background(p.hairline)
		if a.layout.browserTool == "" && a.layout.browserPage == "" {
			a.browserHome(c, p)
		} else {
			a.browserDestination(c, p)
		}
	})
}

// paneTabsBar is the strip of open tools and pages. One tab already names
// itself in the toolbar, so the strip waits for a second.
func (a *app) paneTabsBar(c *ui.Context, p palette) {
	if len(a.layout.paneTabs) <= 1 {
		return
	}
	ui.Row(c).FillWidth().Height(32).Padding(0, 8).Gap(2).
		AlignItems(ui.Center).Background(p.sidebarBg).Children(func() {
		// A menu choice closes tabs while this strip is drawn, so walk a
		// copy rather than the state it rewrites.
		for i, tab := range slices.Clone(a.layout.paneTabs) {
			a.paneTabButton(c, p, i, tab)
		}
	})
	ui.Box(c).FillWidth().Height(1).Background(p.hairline)
}

func (a *app) paneTabButton(c *ui.Context, p palette, index int, tab paneTab) {
	title := paneTabTitle(tab)
	active := a.paneTabActive(tab)
	c.Key("pane-tab:" + tab.kind + ":" + tab.address)
	row := ui.Row(c).Height(26).MaxWidth(170).Shrink(1).MinWidth(0).
		Radius(6).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.selected)
	} else if row.Hovered() {
		row.Background(p.hover)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Close tab").Chosen() {
			a.closePaneTab(index)
		}
		if m.Item("Close other tabs").Chosen() {
			a.closePaneTabsExcept(index)
		}
		if m.Item("Close all tabs").Chosen() {
			a.closeAllPaneTabs()
		}
	})
	row.Children(func() {
		open := ui.ButtonBase(c).Label("Open "+title).Height(26).Radius(6).
			Padding(0, 8).Justify(ui.Start).Grow(1).Shrink(1).MinWidth(0)
		open.OnClick(func() { a.activatePaneTab(tab) })
		open.Children(func() {
			tint := p.muted
			if active {
				tint = p.text
			}
			ui.Text(c, title).FontSize(12).TextColor(tint).SingleLine().Ellipsis("…")
		})
		close := ui.ButtonBase(c).Label("Close "+title).Size(18, 18).Radius(5).Center().
			Tooltip("Close " + title)
		if close.Hovered() {
			close.Background(p.hover)
		}
		close.OnClick(func() { a.closePaneTab(index) })
		close.Children(func() {
			ui.Icon(c, icClose).FontSize(10).TextColor(p.faint)
		})
	})
}

func (a *app) browserToolbar(c *ui.Context, p palette, headerH float32) {
	toolPage := a.layout.browserTool != "" && a.layout.browserTool != "New page"
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Gap(3).Padding(0, 8).Children(func() {
		if toolPage {
			ui.Text(c, a.layout.browserTool).FontSize(12.5).FontWeight(500).
				TextColor(p.muted).SingleLine()
			ui.Spacer(c)
			a.browserIconButton(c, p, "Reload", icLoad, a.reloadBrowserPage, false)
		} else {
			a.browserIconButton(c, p, "Reload", icLoad, a.reloadBrowserPage, a.layout.browserPage == "")
			components.TextBox(c, &a.layout.browserAddress,
				components.TextBoxOptions{LeadingIcon: icGlobe}).Grow(1).
				Placeholder("Search or enter a URL").Label("Search or enter a URL").FontSize(12.5).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == 0 {
						a.openBrowserAddress()
						return true
					}
					return false
				})
		}
		more := ui.ButtonBase(c).Label("Browser options").Size(28, 28).Radius(8).Center().
			Tooltip("Browser options").Menu(func(m *ui.Menu) {
			if m.Item("New page").Chosen() {
				a.openTool("New page")
			}
			if m.Item("Tools").Chosen() {
				a.openTool("")
			}
		})
		if more.Hovered() {
			more.Background(p.hover)
		}
		more.Children(func() { ui.Icon(c, icEllipsis).FontSize(17).TextColor(p.muted) })
	})
}

func (a *app) browserIconButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func(), disabled bool) {
	b := ui.ButtonBase(c).Label(label).Size(28, 28).Radius(8).Center().
		Tooltip(label).Disabled(disabled)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(on)
	tint := p.text
	if disabled {
		// On the window's material the faint tone washes out.
		tint = p.muted
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(tint) })
}

func (a *app) browserHome(c *ui.Context, p palette) {
	ui.Column(c).Fill().Padding(25, 20).Gap(14).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Workspace tools").FontSize(20).FontWeight(600).TextColor(p.text).SingleLine()
			ui.Text(c, "Read the workspace and review what the agent changed, beside the conversation.").
				FontSize(12.5).LineHeight(1.4).TextColor(p.muted)
		})
		tools := []struct {
			name, detail, shortcut string
			icon                   *ui.SVG
		}{
			{"Activity", "See what the agent has run", a.shortcutKeys("activity"), icActivity},
			{"Changes", "Review the working tree or a branch", a.shortcutKeys("changes"), icGitBranch},
			{"Files", "Browse and read the project's files", a.shortcutKeys("quick-open"), icFolder},
			{"New page", "Open an address beside the chat", "", icPlus},
		}
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for _, tool := range tools {
				a.browserToolCard(c, p, tool.name, tool.detail, tool.shortcut, tool.icon)
			}
		})
	})
}

func (a *app) browserToolCard(c *ui.Context, p palette, name, detail, shortcut string, icon *ui.SVG) {
	c.Key("browser-tool:" + name)
	b := ui.ButtonBase(c).Label(name).Height(58).Radius(10).
		Padding(0, 12).FillWidth().Cursor(ui.CursorPointer).
		Background(c.Theme().Surface).Border(1, p.hairline).Justify(ui.Start)
	if b.Hovered() {
		b.Background(p.hover).BorderColor(p.orange.Alpha(.5))
	}
	b.OnClick(func() { a.openTool(name) })
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(p.orange).Shrink(0)
		ui.Column(c).MarginStart(9).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, name).FontSize(13).FontWeight(500).TextColor(p.text).SingleLine()
			ui.Text(c, detail).FontSize(11.5).TextColor(p.muted).SingleLine().Ellipsis("…")
		})
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(10).TextColor(p.faint).SingleLine().Shrink(0)
		}
	})
}

func (a *app) browserDestination(c *ui.Context, p palette) {
	title := a.layout.browserTool
	if title == "" {
		title = a.layout.browserPage
	}
	ui.Column(c).Fill().Padding(28, 22).Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(18).FontWeight(600).TextColor(p.text).SingleLine()
			ui.Spacer(c)
			close := ui.ButtonBase(c).Label("Close pane").Size(28, 28).Radius(8).Center().
				Tooltip("Return to tools")
			if close.Hovered() {
				close.Background(p.hover)
			}
			close.OnClick(func() { a.openTool("") })
			close.Children(func() { ui.Icon(c, icClose).FontSize(14).TextColor(p.muted) })
		})

		if a.layout.browserTool != "" {
			ui.Text(c, a.toolDescription()).FontSize(13).LineHeight(1.5).TextColor(p.muted)
			if a.layout.browserTool == "Changes" {
				a.changesBody(c, p)
				return
			}
			if a.layout.browserTool == "Files" {
				a.filesBody(c, p)
				return
			}
			if a.layout.browserTool == "Activity" {
				a.activityBody(c, p)
				return
			}
		} else {
			ui.Icon(c, icGlobe).FontSize(28).TextColor(p.faint).Margin(18, 0, 0, 0)
			ui.Text(c, "Page preview").FontSize(14).FontWeight(500).TextColor(p.text)
			ui.Text(c, a.layout.browserPage).FontSize(12).TextColor(p.muted).SingleLine()
			ui.Text(c, "This native pane keeps the address and navigation local.").
				FontSize(12).TextColor(p.faint).Margin(4, 0, 0, 0)
			if ui.SafeToOpen(a.layout.browserPage) {
				services := c.Services()
				address := a.layout.browserPage
				open := ui.ButtonBase(c).Label("Open in browser").Height(30).Radius(9).
					PaddingX(11).Cursor(ui.CursorPointer).Margin(12, 0, 0, 0)
				if open.Hovered() {
					open.Background(p.hover)
				}
				open.OnClick(func() { services.OpenURL(address) })
				open.Children(func() {
					ui.Text(c, "Open in browser").FontSize(12.5).TextColor(p.text)
				})
			}
		}
	})
}

func (a *app) toolDescription() string {
	switch a.layout.browserTool {
	case "Activity":
		return "Watch what the turn and the conversation have run, newest first."
	case "Changes":
		return "Review the files changed in this workspace."
	case "Files":
		return "Browse the files in the current project."
	default:
		return "Start a new page beside your conversation."
	}
}

func (a *app) openTool(name string) {
	// Opening a tool is asking to see it, so the pane comes back and the
	// settings page steps aside; from a full view it is the only pane that
	// returns.
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.layout.browserCollapsed = false
	if name == "" {
		a.layout.browserTool = ""
		a.layout.browserPage = ""
		a.layout.browserAddress = ""
		return
	}
	a.ensurePaneTab(name, "")
	a.layout.browserTool = name
	a.layout.browserPage = ""
	a.layout.browserAddress = ""
	// The pane's first look at the workspace it is open over.
	if name == "Changes" {
		a.openChanges(changesWorking)
	}
	if name == "Files" {
		a.openFiles()
	}
}

func (a *app) openBrowserAddress() {
	a.navigateToAddress(a.layout.browserAddress)
}

func (a *app) navigateToAddress(address string) {
	address = strings.TrimSpace(address)
	if address == "" {
		return
	}
	if !strings.Contains(address, "://") {
		if strings.Contains(address, ".") || strings.Contains(address, "localhost") {
			address = "https://" + address
		} else {
			address = "https://www.google.com/search?q=" + url.QueryEscape(address)
		}
	}
	if a.layout.browserHistoryAt+1 < len(a.layout.browserHistory) {
		a.layout.browserHistory = a.layout.browserHistory[:a.layout.browserHistoryAt+1]
	}
	a.layout.browserHistory = append(a.layout.browserHistory, address)
	a.layout.browserHistoryAt = len(a.layout.browserHistory) - 1
	a.showPageAddress(address)
}

func (a *app) browserBack() {
	if a.layout.browserHistoryAt <= 0 {
		return
	}
	a.layout.browserHistoryAt--
	a.showPageAddress(a.layout.browserHistory[a.layout.browserHistoryAt])
}

func (a *app) browserForward() {
	if a.layout.browserHistoryAt < 0 || a.layout.browserHistoryAt >= len(a.layout.browserHistory)-1 {
		return
	}
	a.layout.browserHistoryAt++
	a.showPageAddress(a.layout.browserHistory[a.layout.browserHistoryAt])
}

// reloadBrowserPage reads a page again, or the workspace the tools pane is
// open over when one of its tools is the page on show.
func (a *app) reloadBrowserPage() {
	if a.layout.browserTool == "Changes" {
		a.openChanges(a.changesMode())
		return
	}
	if a.layout.browserTool == "Files" {
		a.filesRefresh()
		return
	}
	if a.layout.browserAddress != "" {
		a.layout.browserPage = a.layout.browserAddress
	}
}
