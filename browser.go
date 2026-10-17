package main

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
)

type browserSuggestion struct {
	title   string
	address string
	color   ui.Color
	icon    *ui.SVG
}

// browserPane is the native tools and page surface beside the conversation.
func (a *app) browserPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Grow(1.12).FillHeight().Background(p.bg).Children(func() {
		a.browserToolbar(c, p, headerH)
		ui.Box(c).FillWidth().Height(1).Background(p.hairline)
		if a.browserTool == "" && a.browserPage == "" {
			a.browserHome(c, p)
		} else {
			a.browserDestination(c, p)
		}
	})
}

func (a *app) browserToolbar(c *ui.Context, p palette, headerH float32) {
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Gap(3).Padding(0, 8).Children(func() {
		a.browserIconButton(c, p, "Back", icBack, a.browserBack, a.browserHistoryAt <= 0)
		a.browserIconButton(c, p, "Forward", icForward, a.browserForward,
			a.browserHistoryAt < 0 || a.browserHistoryAt >= len(a.browserHistory)-1)
		a.browserIconButton(c, p, "Reload", icLoad, a.reloadBrowserPage, a.browserPage == "")

		address := ui.Box(c).Grow(1).Height(32).Radius(999).Padding(0, 10).
			Background(p.composerBg).Border(1, p.composerBorder).Justify(ui.Center)
		if address.FocusWithin() {
			address.Border(1, p.composerBorderFocus)
		}
		address.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Icon(c, icGlobe).FontSize(13).TextColor(p.muted)
				ui.TextInputBase(c, &a.browserAddress).FontSize(12.5).
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
		a.browserIconButton(c, p, "Side chat", icMessage, func() { a.openTool("Side chat") }, false)
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
		tint = p.faint
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
			{"Terminal", "⌃`", icTerminal},
			{"Files", "⌘P", icFolder},
			{"Side chat", "⌥⌘S", icMessage},
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

		ui.Text(c, "Suggested").FontSize(13).FontWeight(600).TextColor(p.text).Margin(30, 0, 4, 0)
		suggestions := []browserSuggestion{
			{"127.0.0.1", "http://127.0.0.1", p.danger, nil},
			{"localhost", "http://localhost", p.detailBg, icGlobe},
			{"Sign in — Tiny App", "http://localhost:3000", p.danger, nil},
			{"Wada — Private d…", "http://localhost:5173", p.text, icFileDoc},
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			for i, suggestion := range suggestions {
				i, suggestion := i, suggestion
				a.suggestionCard(c, p, i, suggestion)
			}
		})
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

func (a *app) suggestionCard(c *ui.Context, p palette, index int, suggestion browserSuggestion) {
	c.Key("suggestion:" + suggestion.address)
	b := ui.ButtonBase(c).Label("Open "+suggestion.title).Grow(1).Height(88).
		Radius(10).Padding(5, 2).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.navigateToAddress(suggestion.address) })
	b.Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			badge := ui.Box(c).Size(32, 32).Center()
			if index == 3 {
				badge.Radius(9)
			} else {
				badge.Radius(999)
			}
			badge.Background(suggestion.color)
			badge.Children(func() {
				if suggestion.icon != nil {
					color := p.bg
					if index == 1 {
						color = p.muted
					}
					ui.Icon(c, suggestion.icon).FontSize(17).TextColor(color)
				}
			})
			ui.Text(c, suggestion.title).FontSize(10.5).TextColor(p.text).
				TextAlign(ui.Center).SingleLine().Ellipsis("…")
		})
	})
}

func (a *app) browserDestination(c *ui.Context, p palette) {
	title := a.browserTool
	if title == "" {
		title = a.browserPage
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

		if a.browserTool != "" {
			ui.Text(c, a.toolDescription()).FontSize(13).LineHeight(1.5).TextColor(p.muted)
			if a.browserTool == "Files" {
				dir := a.chatDir()
				if dir == "" {
					dir = a.home
				}
				ui.Text(c, "Workspace  ·  "+dirBase(dir)).FontSize(12).TextColor(p.faint)
			}
		} else {
			ui.Icon(c, icGlobe).FontSize(28).TextColor(p.faint).Margin(18, 0, 0, 0)
			ui.Text(c, "Page preview").FontSize(14).FontWeight(500).TextColor(p.text)
			ui.Text(c, a.browserPage).FontSize(12).TextColor(p.muted).SingleLine()
			ui.Text(c, "This native pane keeps the address and navigation local.").
				FontSize(12).TextColor(p.faint).Margin(4, 0, 0, 0)
			if ui.SafeToOpen(a.browserPage) {
				services := c.Services()
				address := a.browserPage
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
	switch a.browserTool {
	case "Changes":
		return "Review the files changed in this workspace."
	case "Terminal":
		return "Open a terminal alongside your coding session."
	case "Files":
		return "Browse the files in the current project."
	case "Side chat":
		return "Keep a focused follow-up beside the main conversation."
	default:
		return "Start a new page beside your conversation."
	}
}

func (a *app) openTool(name string) {
	if name == "" {
		a.browserTool = ""
		a.browserPage = ""
		a.browserAddress = ""
		return
	}
	a.browserTool = name
	a.browserPage = ""
	if name == "New page" {
		a.browserAddress = ""
	} else {
		a.browserAddress = "codex://tools/" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
}

func (a *app) openBrowserAddress() {
	a.navigateToAddress(a.browserAddress)
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
	if a.browserHistoryAt+1 < len(a.browserHistory) {
		a.browserHistory = a.browserHistory[:a.browserHistoryAt+1]
	}
	a.browserHistory = append(a.browserHistory, address)
	a.browserHistoryAt = len(a.browserHistory) - 1
	a.browserAddress = address
	a.browserPage = address
	a.browserTool = ""
}

func (a *app) browserBack() {
	if a.browserHistoryAt <= 0 {
		return
	}
	a.browserHistoryAt--
	a.browserAddress = a.browserHistory[a.browserHistoryAt]
	a.browserPage = a.browserAddress
	a.browserTool = ""
}

func (a *app) browserForward() {
	if a.browserHistoryAt < 0 || a.browserHistoryAt >= len(a.browserHistory)-1 {
		return
	}
	a.browserHistoryAt++
	a.browserAddress = a.browserHistory[a.browserHistoryAt]
	a.browserPage = a.browserAddress
	a.browserTool = ""
}

func (a *app) reloadBrowserPage() {
	if a.browserAddress != "" {
		a.browserPage = a.browserAddress
	}
}
