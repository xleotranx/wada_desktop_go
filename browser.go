package main

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
)

// browserPane is the native tools and page surface beside the conversation.
func (a *app) browserPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Grow(1.12).FillHeight().Background(p.bg).Children(func() {
		a.browserToolbar(c, p, headerH)
		ui.Box(c).FillWidth().Height(1).Background(p.hairline)
		if a.layout.browserTool == "" && a.layout.browserPage == "" {
			a.browserHome(c, p)
		} else {
			a.browserDestination(c, p)
		}
	})
}

func (a *app) browserToolbar(c *ui.Context, p palette, headerH float32) {
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Gap(3).Padding(0, 8).Children(func() {
		a.browserIconButton(c, p, "Reload", icLoad, a.reloadBrowserPage, a.layout.browserPage == "")

		address := ui.Box(c).Grow(1).Height(32).Radius(999).Padding(0, 10).
			Background(p.composerBg).Border(1, p.composerBorder).Justify(ui.Center)
		if address.FocusWithin() {
			address.Border(1, p.composerBorderFocus)
		}
		address.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Icon(c, icGlobe).FontSize(13).TextColor(p.muted)
				ui.TextInputBase(c, &a.layout.browserAddress).FontSize(12.5).
					Placeholder("Search or enter a URL").FillWidth().Label("Search or enter a URL").
					OnTextKey(func(k ui.TextKey) bool {
						if k.Key == ui.KeyEnter && k.Mods == 0 {
							a.openBrowserAddress()
							return true
						}
						return false
					})
			})
		})
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
	ui.Column(c).Fill().Padding(25, 20).Gap(8).Children(func() {
		ui.Text(c, "Tools").FontSize(13).FontWeight(600).TextColor(p.text)
		tools := []struct {
			name, shortcut string
			icon           *ui.SVG
		}{
			{"Changes", "⌃⇧G", icGitBranch},
			{"Files", "⌘P", icFolder},
			{"New page", "", icPlus},
		}
		for i := 0; i < len(tools); i += 2 {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				for j := i; j < i+2 && j < len(tools); j++ {
					tool := tools[j]
					a.browserToolCard(c, p, tool.name, tool.shortcut, tool.icon)
				}
				if i+1 >= len(tools) {
					ui.Box(c).Grow(1)
				}
			})
		}
	})
}

func (a *app) browserToolCard(c *ui.Context, p palette, name, shortcut string, icon *ui.SVG) {
	c.Key("browser-tool:" + name)
	b := ui.ButtonBase(c).Label(name).Height(42).Grow(1).Radius(11).
		Padding(0, 11).FillWidth().Cursor(ui.CursorPointer).
		Background(p.bg).Border(1, p.hairline).Justify(ui.Start)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.openTool(name) })
	b.Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(p.muted)
		ui.Text(c, name).MarginStart(8).FontSize(12.5).TextColor(p.text).Grow(1).SingleLine()
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(10).TextColor(p.faint).SingleLine()
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
	// settings page steps aside.
	a.layout.settingsOpen = false
	a.layout.browserCollapsed = false
	if name == "" {
		a.layout.browserTool = ""
		a.layout.browserPage = ""
		a.layout.browserAddress = ""
		return
	}
	a.layout.browserTool = name
	a.layout.browserPage = ""
	if name == "New page" {
		a.layout.browserAddress = ""
	} else {
		a.layout.browserAddress = "codex://tools/" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
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
	a.layout.browserAddress = address
	a.layout.browserPage = address
	a.layout.browserTool = ""
}

func (a *app) browserBack() {
	if a.layout.browserHistoryAt <= 0 {
		return
	}
	a.layout.browserHistoryAt--
	a.layout.browserAddress = a.layout.browserHistory[a.layout.browserHistoryAt]
	a.layout.browserPage = a.layout.browserAddress
	a.layout.browserTool = ""
}

func (a *app) browserForward() {
	if a.layout.browserHistoryAt < 0 || a.layout.browserHistoryAt >= len(a.layout.browserHistory)-1 {
		return
	}
	a.layout.browserHistoryAt++
	a.layout.browserAddress = a.layout.browserHistory[a.layout.browserHistoryAt]
	a.layout.browserPage = a.layout.browserAddress
	a.layout.browserTool = ""
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
