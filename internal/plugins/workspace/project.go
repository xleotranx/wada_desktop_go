package workspace

import (
	"context"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

type projectForm struct {
	open    bool
	host    string
	path    string
	err     string
	done    func(agent.Workspace)
	folders []string
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
			Placeholder("Folder path").Disabled(busy).OnChange(p.findProjectFolders).OnSubmit(p.submitProject)
		for _, folder := range f.folders {
			c.Key(folder)
			components.Button(c, folder, components.ButtonGhost).FillWidth().Disabled(busy).OnClick(func() {
				p.projectFolders.Cancel()
				f.path, f.err, f.folders = folder, "", nil
			})
		}
		if f.err != "" {
			ui.Text(c, f.err).TextColor(pal.DangerText).FontSize(12)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "Cancel", components.ButtonSecondary).Disabled(busy).
				OnClick(func() {
					p.projectFolders.Cancel()
					p.remoteProject = projectForm{}
				})
			components.Button(c, "Add project", components.ButtonPrimary).
				Disabled(busy || strings.TrimSpace(f.path) == "").OnClick(p.submitProject)
		})
	})
}

func (p *Plugin) submitProject() {
	f := &p.remoteProject
	path := f.path
	if !f.open || strings.TrimSpace(path) == "" || p.picking.Running() {
		return
	}
	p.projectFolders.Cancel()
	f.err, f.folders = "", nil
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

func (p *Plugin) findProjectFolders() {
	f := &p.remoteProject
	p.projectFolders.Cancel()
	if !f.open || p.picking.Running() {
		return
	}
	f.folders, f.err = nil, ""
	prefix := f.path
	ws, ok := kernel.Use(p.ctx, agent.WorkspacesKey)
	if !ok || !strings.ContainsAny(prefix, "/\\") {
		return
	}
	kernel.Go(p.ctx, &p.projectFolders, func(ctx context.Context) ([]string, error) {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return ws.ProjectFolders(ctx, prefix)
		}
	}, func(folders []string, err error) {
		if err == nil && f.open && f.path == prefix {
			f.folders = folders
			p.ctx.Invalidate()
		}
	})
}
