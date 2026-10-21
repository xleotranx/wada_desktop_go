package coding

import (
	"context"
	"fmt"
	"time"

	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// vcsKey names one server's workspace in the branch cache.
func VCSKey(serverID, dir string) string { return serverID + "|" + dir }

// ensureVcs reads the open chat folder's version control in the background
// and caches the answer per server and folder. Rendering only reads the
// cache; a folder is asked for once its server has answered a probe.
func (a *Module) EnsureVcs() {
	dir := a.host.Context().Directory
	if dir == "" || a.closed || a.host.Context().IsDraft {
		return
	}
	snapshot := a.host.Context()
	if !snapshot.Ready {
		return
	}
	key := VCSKey(snapshot.Server, dir)
	if _, ok := a.VCS[key]; ok {
		return
	}
	client := snapshot.Workspace
	feature.Run(a.host, &a.VCSRequest, func(ctx context.Context) (workspace.VCSInfo, error) {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if client == nil {
			return workspace.VCSInfo{}, fmt.Errorf("version control unavailable")
		}
		return client.WorkspaceVcs(ctx, dir)
	}, func(info workspace.VCSInfo, err error) {
		if err != nil {
			return
		}
		if a.VCS == nil {
			a.VCS = map[string]workspace.VCSInfo{}
		}
		a.VCS[key] = info
	})
}

// workspaceChip names the chat's folder and, once read, its branch. It
// opens Files on the workspace.
func (a *Module) WorkspaceChip(c *ui.Context, p native.Palette, dir string) {
	info := a.VCS[VCSKey(a.host.Context().Server, dir)]
	name := dirBase(dir)
	label := "Workspace " + name
	tooltip := dir
	if info.Branch != "" {
		label += " on " + info.Branch
		tooltip += "\n" + info.Branch
	}
	c.Key("workspace:" + dir)
	b := ui.ButtonBase(c).Label(label).Height(24).Radius(999).Padding(0, 9).
		MarginStart(8).Shrink(0).MaxWidth(300).Cursor(ui.CursorPointer).
		Background(p.DetailBg).Tooltip(tooltip)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.host.OpenPanel("Files") })
	b.ContextMenu(func(m *ui.Menu) {
		if m.Item("Copy workspace path").Chosen() {
			a.host.Copy(dir)
		}
		if a.host.Context().Local {
			if m.Item("Show in Finder").Chosen() {
				native.OpenLocalPath(c, dir)
			}
		}
		if m.Item("Review changes").Chosen() {
			a.host.OpenPanel("Changes")
		}
	})
	b.Children(func() {
		ui.Icon(c, lucide.Folder()).FontSize(11).TextColor(p.Faint).Shrink(0)
		ui.Text(c, name).FontSize(11.5).TextColor(p.Muted).SingleLine().
			Ellipsis("…").MaxWidth(130)
		if info.Branch != "" {
			ui.Icon(c, lucide.GitBranch()).FontSize(11).MarginStart(3).TextColor(p.Faint).Shrink(0)
			ui.Text(c, info.Branch).FontSize(11.5).TextColor(p.Faint).SingleLine().
				Ellipsis("…").MaxWidth(130)
		}
	})
}
