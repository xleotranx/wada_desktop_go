// Package settings is the settings page: appearance, the installed plugins,
// and every section other plugins contribute to settings.Sections.
package settings

import (
	"strings"

	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Plugin is the settings page. The zero value is ready to install.
type Plugin struct {
	ctx     *kernel.Context
	host    shell.Host
	section string
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "settings", Title: "Settings", Locked: true,
		Description: "The settings page.",
		Requires:    []kernel.Named{shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	open := func() { p.host.ShowPage(settings.PageID) }
	for _, err := range []error{
		kernel.Contribute(c, shell.Pages, settings.PageID, 100, shell.Page{Title: func() string { return "Settings" }, Render: p.view, HideSidebar: true}),
		kernel.Contribute(c, shell.Sidebar, "settings.button", 100, shell.View{Render: p.sidebarButton}),
		kernel.Contribute(c, kernel.Commands, "settings.open", 0, kernel.Command{
			Title: "Settings…", Menu: "File", Keys: []string{"cmd+,"}, Run: open}),
		kernel.Contribute(c, settings.Sections, "settings.appearance", 0, settings.Section{
			Title: "Appearance", Description: "Choose how Wada looks on this Mac.", Render: p.appearance}),
		kernel.Contribute(c, settings.Sections, "settings.plugins", 1000, settings.Section{
			Title: "Plugins", Description: "Manage the features in your workspace. Disabled plugins keep their settings.", Render: p.plugins}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) sidebarButton(c *ui.Context) {
	pal := native.PaletteFor(c)
	b := ui.ButtonBase(c).Label("Settings").Height(34).Radius(8).Padding(0, 8).FillWidth().
		Justify(ui.Start).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer)
	if p.host.Page() == settings.PageID {
		b.Background(pal.Selected)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.host.ShowPage(settings.PageID) })
	b.Children(func() {
		ui.Icon(c, lucide.Settings()).FontSize(14).TextColor(pal.Text)
		ui.Text(c, "Settings").FontSize(13.5).TextColor(pal.Text)
	})
}

func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	sections := kernel.Items(p.ctx, settings.Sections)
	selected := p.section
	found := false
	for _, e := range sections {
		found = found || e.ID == selected
	}
	if !found && len(sections) > 0 {
		selected = sections[0].ID
	}
	ui.Row(c).Fill().MinHeight(0).Children(func() {
		ui.Column(c).Width(220).FillHeight().Shrink(0).Padding(20, 12).Gap(28).Children(func() {
			back := ui.ButtonBase(c).Label("Back to chat").FillWidth().Height(36).Radius(8).
				Padding(0, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
			if back.Hovered() {
				back.Background(pal.Hover)
			}
			back.OnClick(func() { p.host.ShowPage("") })
			back.Children(func() {
				ui.Icon(c, lucide.ArrowLeft()).FontSize(15).TextColor(pal.Muted)
				ui.Text(c, "Back to chat").FontSize(13).TextColor(pal.Text)
			})
			ui.Scroll(c).Fill().Children(func() {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					for _, e := range sections {
						c.Key("nav:" + e.ID)
						active := selected == e.ID
						button := ui.ButtonBase(c).Label(e.Value.Title).FillWidth().MinHeight(38).
							Padding(10, 12).Radius(8).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
						if active {
							button.Background(pal.Selected)
						} else if button.Hovered() {
							button.Background(pal.Hover)
						}
						button.OnClick(func() { p.section = e.ID })
						button.Children(func() {
							ui.Text(c, e.Value.Title).FontSize(13).FontWeight(500).TextColor(pal.Text)
						})
					}
				})
			})
		})
		ui.Scroll(c).Key(selected).Fill().MinWidth(0).Children(func() {
			for _, e := range sections {
				if e.ID != selected {
					continue
				}
				ui.Column(c).Key(e.ID).FillWidth().MaxWidth(840).MinWidth(0).
					Gap(28).Padding(48, 48, 40, 48).Children(func() {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						ui.Text(c, e.Value.Title).FontSize(24).FontWeight(600).TextColor(pal.Text)
						if e.Value.Description != "" {
							ui.Text(c, e.Value.Description).FontSize(13).TextColor(pal.Muted)
						}
					})
					ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
					ui.Column(c).FillWidth().MinWidth(0).Gap(20).Children(func() {
						kernel.Invoke(p.ctx, e, func(s settings.Section) { s.Render(c) })
					})
				})
			}
		})
	})
}

func (p *Plugin) appearance(c *ui.Context) {
	pal := native.PaletteFor(c)
	current := p.host.Appearance()
	ui.Text(c, "Color theme").FontSize(14).FontWeight(500).TextColor(pal.Text)
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		for _, mode := range []struct {
			id, label string
			icon      *ui.SVG
		}{{"system", "System", lucide.Monitor()}, {"light", "Light", lucide.Sun()}, {"dark", "Dark", lucide.Moon()}} {
			c.Key(mode.id)
			chosen := current == mode.id
			button := ui.ButtonBase(c).Label(mode.label).Width(0).Grow(1).Height(48).Radius(8).
				Border(1, pal.Hairline).Background(pal.Bg).Cursor(ui.CursorPointer)
			if chosen {
				button.BorderColor(pal.Orange).Background(pal.Orange.Alpha(.06))
			} else if button.Hovered() {
				button.Background(pal.Hover)
			}
			button.OnClick(func() { p.host.SetAppearance(mode.id) })
			button.Children(func() {
				ui.Row(c).Fill().Center().Gap(8).Children(func() {
					ui.Icon(c, mode.icon).FontSize(18).TextColor(pal.Text)
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, mode.label).FontSize(13).FontWeight(500).TextColor(pal.Text)
						if chosen {
							ui.Icon(c, lucide.Check()).FontSize(13).TextColor(pal.Orange)
						}
					})
				})
			})
		}
	})
	ui.Text(c, "System follows your Mac’s appearance automatically.").FontSize(12).TextColor(pal.Muted)
}

func (p *Plugin) plugins(c *ui.Context) {
	pal := native.PaletteFor(c)
	for i, st := range p.host.Plugins() {
		if i > 0 {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		}
		c.Key("plugin:" + st.Manifest.ID)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Padding(4, 0).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, st.Manifest.Title).FontSize(13.5).FontWeight(500).TextColor(pal.Text)
				if line := statusLine(st); line != "" {
					tint := pal.Muted
					if st.State == kernel.Failed {
						tint = pal.Danger
					}
					ui.Text(c, line).FontSize(12).TextColor(tint)
				}
			})
			if st.Manifest.Locked {
				ui.Text(c, "Required").FontSize(11).TextColor(pal.Muted).Background(pal.Selected).Padding(4, 8).Radius(6)
				return
			}
			on := st.State != kernel.Disabled
			label := "Turn off"
			if !on {
				label = "Turn on"
			}
			enabled := on
			components.Switch(c, &enabled, label+" "+st.Manifest.Title, components.SwitchOptions{HideLabel: true})
			if enabled != on {
				p.ctx.Post(func() {
					var err error
					if on {
						err = p.host.Disable(st.Manifest.ID)
					} else {
						err = p.host.Enable(st.Manifest.ID)
					}
					if err != nil {
						p.host.Toast(ui.Toast{Title: st.Manifest.Title + " did not change", Description: err.Error(), Type: components.ToastError})
					}
				})
			}
		})
	}
}

func statusLine(st kernel.Status) string {
	switch st.State {
	case kernel.Failed:
		if st.Err != nil {
			return "Stopped: " + st.Err.Error()
		}
		return "Stopped"
	case kernel.Blocked:
		return "Waiting for a plugin that provides " + joinNames(st.Missing)
	case kernel.Disabled:
		return "Off"
	case kernel.Active:
	}
	return st.Manifest.Description
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
