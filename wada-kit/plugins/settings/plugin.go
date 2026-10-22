// Package settings is the settings page: appearance, the installed plugins,
// and every section other plugins contribute to settings.Sections.
package settings

import (
	"strings"

	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/oneo-me/onevra/source/theme"
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
		kernel.Contribute(c, shell.Pages, settings.PageID, 100, shell.Page{Title: func() string { return "Settings" }, Render: p.view}),
		kernel.Contribute(c, kernel.Commands, "settings.open", 0, kernel.Command{
			Title: "Settings…", Menu: "File", Keys: []string{"cmd+,"}, Run: open}),
		kernel.Contribute(c, settings.Sections, "settings.appearance", 0, settings.Section{
			Title: "Appearance", Description: "Choose how the app looks on this Mac.", Render: p.appearance}),
		kernel.Contribute(c, settings.Sections, "settings.plugins", 1000, settings.Section{
			Title: "Plugins", Description: "Manage the features in your workspace. Disabled plugins keep their settings.", Render: p.plugins}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
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
		ui.Column(c).Width(220).FillHeight().Shrink(0).Padding(20, 12).Children(func() {
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
					Gap(28).Padding(24, 48, 40, 48).Children(func() {
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

// appearance offers System, Light and Dark, each drawn as a small window in
// that theme: the strip with its tabs over the panel.
func (p *Plugin) appearance(c *ui.Context) {
	pal := native.PaletteFor(c)
	current := p.host.Appearance()
	light, dark := native.PaletteOf(theme.Palette(false)), native.PaletteOf(theme.Palette(true))
	ui.Text(c, "Color theme").FontSize(14).FontWeight(500).TextColor(pal.Text)
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		for _, mode := range []struct {
			id, label string
			icon      *ui.SVG
		}{{"system", "System", lucide.Monitor()}, {"light", "Light", lucide.Sun()}, {"dark", "Dark", lucide.Moon()}} {
			c.Key(mode.id)
			chosen := current == mode.id
			button := ui.ButtonBase(c).Label(mode.label).Width(0).Grow(1).Radius(10).Padding(6).Gap(8).
				Border(1, pal.Hairline).Cursor(ui.CursorPointer)
			if chosen {
				button.Border(2, pal.Orange).Padding(5)
			} else if button.Hovered() {
				button.Background(pal.Hover)
			}
			button.OnClick(func() { p.host.SetAppearance(mode.id) })
			button.Children(func() {
				ui.Row(c).FillWidth().Height(92).Radius(6).Clip().Children(func() {
					switch mode.id {
					case "light":
						windowPreview(c, light)
					case "dark":
						windowPreview(c, dark)
					default: // half of each
						windowPreview(c, light)
						windowPreview(c, dark)
					}
				})
				ui.Row(c).FillWidth().Center().Gap(6).Padding(0, 0, 2, 0).Children(func() {
					ui.Icon(c, mode.icon).FontSize(13).TextColor(pal.Muted)
					ui.Text(c, mode.label).FontSize(13).FontWeight(500).TextColor(pal.Text)
					if chosen {
						ui.Icon(c, lucide.Check()).FontSize(13).TextColor(pal.Orange)
					}
				})
			})
		}
	})
	ui.Text(c, "System follows your Mac’s appearance automatically.").FontSize(12).TextColor(pal.Muted)
	ui.Text(c, "Window background").FontSize(14).FontWeight(500).TextColor(pal.Text).Margin(8, 0, 0, 0)
	backdrop := p.host.Backdrop()
	ui.Row(c).Gap(8).Children(func() {
		for _, mode := range []struct{ id, label string }{{"clear", "Clear"}, {"frosted", "Frosted"}, {"solid", "Solid"}} {
			c.Key(mode.id)
			native.Choice(c, mode.label, backdrop == mode.id, func() { p.host.SetBackdrop(mode.id) })
		}
	})
	ui.Text(c, "Clear shows your desktop through the window, Frosted blurs it.").FontSize(12).TextColor(pal.Muted)
}

// windowPreview is a window in miniature: the strip with a project's tabs,
// then the panel with a few lines of a conversation.
func windowPreview(c *ui.Context, pal native.Palette) {
	project := pal.Projects[0]
	ui.Column(c).Width(0).Grow(1).FillHeight().Background(pal.StripBg).Padding(0, 5, 5, 5).Children(func() {
		ui.Row(c).FillWidth().Height(20).AlignItems(ui.End).Gap(3).Children(func() {
			for range 3 {
				ui.Box(c).Size(4, 4).Radius(2).Background(pal.Faint).Margin(0, 0, 8, 0)
			}
			ui.Box(c).Size(14, 6).Radius(2).Background(project.Tint).Margin(0, 0, 7, 3)
			ui.Box(c).Size(26, 12).Radius(3, 3, 0, 0).Background(pal.PanelBg)
			ui.Box(c).Size(22, 12).Radius(3, 3, 0, 0).Children(func() {
				ui.Box(c).FillWidth().Height(1).Background(project.Mark).Absolute().Bottom(0)
			})
		})
		ui.Column(c).FillWidth().Grow(1).Background(pal.PanelBg).Radius(4).Padding(8).Gap(5).Children(func() {
			ui.Box(c).Width(34).Height(5).Radius(3).Background(pal.Raised).AlignSelf(ui.End)
			ui.Box(c).Width(48).Height(3).Radius(2).Background(pal.Faint)
			ui.Box(c).Width(40).Height(3).Radius(2).Background(pal.Faint)
		})
	})
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
