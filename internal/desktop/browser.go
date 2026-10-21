package desktop

import (
	"github.com/egoist/mygo/ui"
	"slices"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
)

type paneTab struct{ kind string }

func paneTabTitle(tab paneTab) string         { return tab.kind }
func (a *app) paneTabActive(tab paneTab) bool { return a.layout.browserTool == tab.kind }
func (a *app) ensurePaneTab(kind string) int {
	for i, tab := range a.layout.paneTabs {
		if tab.kind == kind {
			return i
		}
	}
	a.layout.paneTabs = append(a.layout.paneTabs, paneTab{kind})
	return len(a.layout.paneTabs) - 1
}
func (a *app) activatePaneTab(tab paneTab) { a.openTool(tab.kind) }
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
		a.closeAllPaneTabs()
		return
	}
	a.activatePaneTab(a.layout.paneTabs[min(index, len(a.layout.paneTabs)-1)])
}
func (a *app) closePaneTabsExcept(index int) {
	if index < 0 || index >= len(a.layout.paneTabs) {
		return
	}
	kept := a.layout.paneTabs[index]
	a.layout.paneTabs = []paneTab{kept}
	a.activatePaneTab(kept)
}
func (a *app) closeAllPaneTabs() {
	a.layout.paneTabs = nil
	a.layout.browserTool = ""
	a.layout.browserCollapsed = true
}
func (a *app) panels() []feature.Panel {
	panels := []feature.Panel{{ID: "Activity", Title: "Activity", Description: "Watch what the turn and the conversation have run, newest first.", Icon: icActivity, Open: func() {}, Render: func(c *ui.Context) { a.activityBody(c, paletteFor(c)) }, Refresh: func() {}}}
	for _, module := range a.features {
		if provider, ok := module.(feature.Panels); ok {
			panels = append(panels, provider.Panels()...)
		}
	}
	return panels
}
func (a *app) panel(id string) (feature.Panel, bool) {
	for _, panel := range a.panels() {
		if panel.ID == id {
			return panel, true
		}
	}
	return feature.Panel{}, false
}
func (a *app) browserPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Fill().Background(p.Bg).Children(func() {
		a.paneTabsBar(c, p)
		ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 8).Children(func() {
			title := a.layout.browserTool
			if title == "" {
				title = "Workspace tools"
			}
			ui.Text(c, title).FontSize(12.5).TextColor(p.Muted)
			ui.Spacer(c)
			a.browserIconButton(c, p, "Reload", icLoad, a.reloadBrowserPage, a.layout.browserTool == "")
		})
		ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
		if a.layout.browserTool == "" {
			a.browserHome(c, p)
		} else {
			a.browserDestination(c, p)
		}
	})
}
func (a *app) paneTabsBar(c *ui.Context, p palette) {
	if len(a.layout.paneTabs) <= 1 {
		return
	}
	ui.Row(c).FillWidth().Height(32).Padding(0, 8).Gap(2).AlignItems(ui.Center).Background(p.SidebarBg).Children(func() {
		for i, tab := range slices.Clone(a.layout.paneTabs) {
			a.paneTabButton(c, p, i, tab)
		}
	})
	ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
}
func (a *app) paneTabButton(c *ui.Context, p palette, index int, tab paneTab) {
	title := paneTabTitle(tab)
	active := a.paneTabActive(tab)
	c.Key("pane-tab:" + tab.kind)
	row := ui.Row(c).Height(26).MaxWidth(170).Shrink(1).MinWidth(0).
		Radius(6).Clip().AlignItems(ui.Center)
	if active {
		row.Background(p.Selected)
	} else if row.Hovered() {
		row.Background(p.Hover)
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
			tint := p.Muted
			if active {
				tint = p.Text
			}
			ui.Text(c, title).FontSize(12).TextColor(tint).SingleLine().Ellipsis("…")
		})
		close := ui.ButtonBase(c).Label("Close "+title).Size(18, 18).Radius(5).Center().
			Tooltip("Close " + title)
		if close.Hovered() {
			close.Background(p.Hover)
		}
		close.OnClick(func() { a.closePaneTab(index) })
		close.Children(func() {
			ui.Icon(c, icClose).FontSize(10).TextColor(p.Faint)
		})
	})
}

func (a *app) browserIconButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func(), disabled bool) {
	native.IconButton(c, p, label, icon, on, disabled)
}
func (a *app) browserHome(c *ui.Context, p palette) {
	ui.Column(c).Fill().Padding(25, 20).Gap(14).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Workspace tools").FontSize(20).FontWeight(600).TextColor(p.Text).SingleLine()
			ui.Text(c, "Read the workspace and review what the agent changed, beside the conversation.").FontSize(12.5).LineHeight(1.4).TextColor(p.Muted)
		})
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for _, panel := range a.panels() {
				a.browserToolCard(c, p, panel.Title, panel.Description, "", panel.Icon)
			}
		})
	})
}
func (a *app) browserToolCard(c *ui.Context, p palette, name, detail, shortcut string, icon *ui.SVG) {
	c.Key("browser-tool:" + name)
	b := ui.ButtonBase(c).Label(name).Height(58).Radius(10).
		Padding(0, 12).FillWidth().Cursor(ui.CursorPointer).
		Background(c.Theme().Surface).Border(1, p.Hairline).Justify(ui.Start)
	if b.Hovered() {
		b.Background(p.Hover).BorderColor(p.Orange.Alpha(.5))
	}
	b.OnClick(func() { a.openTool(name) })
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(p.Orange).Shrink(0)
		ui.Column(c).MarginStart(9).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, name).FontSize(13).FontWeight(500).TextColor(p.Text).SingleLine()
			ui.Text(c, detail).FontSize(11.5).TextColor(p.Muted).SingleLine().Ellipsis("…")
		})
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(10).TextColor(p.Faint).SingleLine().Shrink(0)
		}
	})
}

func (a *app) browserDestination(c *ui.Context, p palette) {
	panel, ok := a.panel(a.layout.browserTool)
	if !ok {
		return
	}
	ui.Column(c).Fill().Padding(28, 22).Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, panel.Title).FontSize(18).FontWeight(600).TextColor(p.Text).SingleLine()
			ui.Spacer(c)
			a.browserIconButton(c, p, "Close pane", icClose, func() { a.openTool("") }, false)
		})
		ui.Text(c, panel.Description).FontSize(13).LineHeight(1.5).TextColor(p.Muted)
		panel.Render(c)
	})
}
func (a *app) openTool(id string) {
	panel, ok := a.panel(id)
	if id != "" && !ok {
		return
	}
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.layout.browserCollapsed = false
	a.layout.browserTool = id
	if ok {
		a.ensurePaneTab(id)
		if panel.Open != nil {
			panel.Open()
		}
	}
}
func (a *app) reloadBrowserPage() {
	if panel, ok := a.panel(a.layout.browserTool); ok {
		if panel.Refresh != nil {
			panel.Refresh()
		}
	}
}
