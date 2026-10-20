package main

import (
	"context"
	"time"

	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// vcsKey names one server's workspace in the branch cache.
func vcsKey(serverID, dir string) string { return serverID + "|" + dir }

// ensureVcs reads the open chat folder's version control in the background
// and caches the answer per server and folder. Rendering only reads the
// cache; a folder is asked for once its server has answered a probe.
func (a *app) ensureVcs() {
	dir := a.chatDir()
	if dir == "" || a.runtime.closed || a.conversation.target.isDraft() {
		return
	}
	srv := a.activeServerConn()
	if a.connections.serverHealth[srv.id].status != healthReady {
		return
	}
	key := vcsKey(srv.id, dir)
	if _, ok := a.workspace.vcs[key]; ok {
		return
	}
	cfg := srv
	runOwned(a, &a.workspace.vcsRequest, a.runtime.workCtx, func(ctx context.Context) (opencode.VCSInfo, error) {
		client, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return opencode.VCSInfo{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return client.WorkspaceVcs(ctx, dir)
	}, func(info opencode.VCSInfo, err error) {
		if err != nil {
			return
		}
		if a.workspace.vcs == nil {
			a.workspace.vcs = map[string]opencode.VCSInfo{}
		}
		a.workspace.vcs[key] = info
	})
}

// workspaceChip names the chat's folder and, once read, its branch. It
// opens Files on the workspace.
func (a *app) workspaceChip(c *ui.Context, p palette, dir string) {
	info := a.workspace.vcs[vcsKey(a.connections.activeServer, dir)]
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
		Background(p.detailBg).Tooltip(tooltip)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.openTool("Files") })
	b.Children(func() {
		ui.Icon(c, icFolder).FontSize(11).TextColor(p.faint).Shrink(0)
		ui.Text(c, name).FontSize(11.5).TextColor(p.muted).SingleLine().
			Ellipsis("…").MaxWidth(130)
		if info.Branch != "" {
			ui.Icon(c, icGitBranch).FontSize(11).MarginStart(3).TextColor(p.faint).Shrink(0)
			ui.Text(c, info.Branch).FontSize(11.5).TextColor(p.faint).SingleLine().
				Ellipsis("…").MaxWidth(130)
		}
	})
}
