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
	ctx  *kernel.Context
	host shell.Host
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
		kernel.Contribute(c, shell.Sidebar, "settings.button", 100, shell.View{Render: p.sidebarButton}),
		kernel.Contribute(c, kernel.Commands, "settings.open", 0, kernel.Command{
			Title: "Settings…", Category: "App", Menu: "File", Keys: []string{"cmd+,"}, Run: open}),
		kernel.Contribute(c, settings.Sections, "settings.appearance", 0, settings.Section{
			Title: "Appearance", Description: "How the app looks.", Render: p.appearance}),
		kernel.Contribute(c, settings.Sections, "settings.plugins", 1000, settings.Section{
			Title: "Plugins", Description: "What this build installs. Turned-off plugins keep their settings.", Render: p.plugins}),
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
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(720).Margin(0, ui.Auto).Padding(24, 28, 40, 28).Gap(28).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Settings").FontSize(22).FontWeight(600).TextColor(pal.Text)
				ui.Spacer(c)
				components.Button(c, "Done", components.ButtonSecondary).OnClick(func() { p.host.ShowPage("") })
			})
			for _, e := range kernel.Items(p.ctx, settings.Sections) {
				c.Key(e.ID)
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					ui.Text(c, e.Value.Title).FontSize(15).FontWeight(600).TextColor(pal.Text)
					if e.Value.Description != "" {
						ui.Text(c, e.Value.Description).FontSize(12.5).TextColor(pal.Muted)
					}
					ui.Column(c).FillWidth().Radius(12).Padding(14).Gap(10).Background(pal.DetailBg).
						Border(1, pal.Hairline).Children(func() {
						kernel.Invoke(p.ctx, e, func(s settings.Section) { s.Render(c) })
					})
				})
			}
		})
	})
}

func (p *Plugin) appearance(c *ui.Context) {
	current := p.host.Appearance()
	ui.Row(c).Gap(8).Children(func() {
		for _, mode := range []struct{ id, label string }{{"system", "System"}, {"light", "Light"}, {"dark", "Dark"}} {
			native.Choice(c, mode.label, current == mode.id, func() { p.host.SetAppearance(mode.id) })
		}
	})
}

func (p *Plugin) plugins(c *ui.Context) {
	pal := native.PaletteFor(c)
	for _, st := range p.host.Plugins() {
		c.Key("plugin:" + st.Manifest.ID)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
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
				ui.Text(c, "Required").FontSize(12).TextColor(pal.Faint)
				return
			}
			on := st.State != kernel.Disabled
			label := "Turn off"
			if !on {
				label = "Turn on"
			}
			components.Button(c, label, components.ButtonSecondary).Label(label + " " + st.Manifest.Title).OnClick(func() {
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
