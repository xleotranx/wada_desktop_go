// Package inspector contributes a read-only page for plugin diagnostics.
package inspector

import (
	"fmt"
	"strings"

	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

const pageID = "inspector.page"

// Plugin is the plugin diagnostics page.
type Plugin struct {
	inspector kernel.Inspector
	host      shell.Host
	expanded  map[string]bool
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "inspector", Title: "Plugin diagnostics",
		Description: "Inspect plugin dependencies, registrations, scopes, workers and failures.",
		Requires:    []kernel.Named{kernel.InspectorKey, shell.HostKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.inspector = kernel.Require(c, kernel.InspectorKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.expanded = map[string]bool{}
	open := func() { p.host.ShowPage(pageID) }
	kernel.Contribute(c, shell.Pages, pageID, 200, shell.Page{Title: func() string { return "Plugin diagnostics" }, Render: p.view})
	kernel.Contribute(c, kernel.Commands, "inspector.open", 0, kernel.Command{Title: "Plugin diagnostics", Menu: "View", Run: open})
	kernel.Contribute(c, settings.Sections, "inspector.settings", 1100, settings.Section{Title: "Diagnostics", Render: func(c *ui.Context) {
		components.Button(c, "Open plugin diagnostics", components.ButtonSecondary).OnClick(open)
	}})
	return nil
}

func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	all := p.inspector.Inspect()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(900).Margin(0, ui.Auto).Padding(24, 28).Gap(16).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Plugin diagnostics").FontSize(22).FontWeight(600).TextColor(pal.Text)
				ui.Spacer(c)
				components.Button(c, "Done", components.ButtonSecondary).OnClick(func() { p.host.ShowPage(settings.PageID) })
			})
			ui.Text(c, "Live dependencies and resources. Counts include cancelled workers until they exit.").FontSize(12).TextColor(pal.Muted)
			for _, d := range all {
				id := d.Status.Manifest.ID
				c.Key(id)
				ui.Column(c).FillWidth().Padding(14).Gap(8).Radius(12).Background(pal.DetailBg).Border(1, pal.Hairline).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Column(c).Grow(1).Gap(3).Children(func() {
							ui.Text(c, id+" · "+d.Status.State.String()).FontSize(14).FontWeight(600).TextColor(pal.Text)
							ui.Text(c, fmt.Sprintf("%d scopes · %d workers · %d registrations", d.Scopes, d.Workers, len(d.Registrations))).FontSize(12).TextColor(pal.Muted)
						})
						components.Button(c, "Inspect "+id, components.ButtonSecondary).OnClick(func() { p.expanded[id] = !p.expanded[id] })
					})
					if p.expanded[id] {
						p.details(c, pal, d)
					}
				})
			}
		})
	})
}

func (*Plugin) details(c *ui.Context, pal native.Palette, d kernel.Diagnostic) {
	line := func(text string) { ui.Text(c, text).FontSize(12).TextColor(pal.Text) }
	if len(d.Status.Missing) > 0 {
		line("Missing: " + strings.Join(d.Status.Missing, ", "))
	}
	for _, dep := range d.Requires {
		provider := dep.Provider
		if provider == "" {
			provider = "unavailable"
		}
		line(dep.Key + " ← " + provider)
	}
	for _, reg := range d.Registrations {
		text := reg.Kind + " · " + reg.Name
		if reg.ID != "" {
			text += " · " + reg.ID
		}
		line(text)
	}
	if d.Status.Err != nil {
		ui.Text(c, d.Status.Err.Error()).FontSize(12).TextColor(pal.Danger)
	}
	if d.Stack != "" {
		ui.Text(c, d.Stack).Font("monospace").FontSize(11).TextColor(pal.Muted)
	}
}
