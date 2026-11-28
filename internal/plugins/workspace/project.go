package workspace

import (
	"context"
	"strings"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/native"
)

type projectForm struct {
	open bool
	host string
	path string
	err  string
	done func(agent.Workspace)
}

func (p *Plugin) projectDialog(c *ui.Context) {
	f := &p.remoteProject
	if !f.open {
		return
	}
	c.Key("remote-project")
	native.Dialog(c, &f.open, 440, "Add remote project", func() {
		pal := native.PaletteFor(c)
		busy := p.picking.Running()
		ui.Text(c, "Enter the full folder path on "+f.host+".").TextColor(pal.Muted).FontSize(13)
		c.Key("project-path")
		components.TextBox(c, &f.path, components.TextBoxOptions{}).FillWidth().Focus().
			Placeholder("Folder path").Disabled(busy).OnSubmit(p.submitProject)
		if f.err != "" {
			ui.Text(c, f.err).TextColor(pal.DangerText).FontSize(12)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "Cancel", components.ButtonSecondary).Disabled(busy).
				OnClick(func() { p.remoteProject = projectForm{} })
			components.Button(c, "Add project", components.ButtonPrimary).
				Disabled(busy || strings.TrimSpace(f.path) == "").OnClick(p.submitProject)
		})
	})
}

func (p *Plugin) submitProject() {
	f := &p.remoteProject
	path := strings.TrimSpace(f.path)
	if !f.open || path == "" || p.picking.Running() {
		return
	}
	f.err = ""
	done := f.done
	p.addProject(func(context.Context) (string, error) { return path, nil },
		func(w agent.Workspace) {
			p.remoteProject = projectForm{}
			done(w)
		}, func(err error) {
			f.err = err.Error()
			p.ctx.Invalidate()
		})
}
