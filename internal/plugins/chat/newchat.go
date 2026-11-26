package chat

import (
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// folderGate reports that the page should ask for a folder instead of the
// composer. Asking to work without one opens the composer in the Inbox.
func (p *Plugin) folderGate() bool {
	return p.conv.Session == "" && p.conv.Directory == "" && !p.inbox && p.draft == ""
}

// requirement draws the first setup a new chat needs before it can start,
// such as a connected provider.
func (p *Plugin) requirement(c *ui.Context) bool {
	for _, e := range kernel.Items(p.ctx, chat.Requirements) {
		needed, render := false, false
		kernel.Invoke(p.ctx, e, func(r chat.Requirement) {
			needed = r.Needed == nil || r.Needed()
			render = r.Render != nil
		})
		if !needed || !render {
			continue
		}
		c.Key(e.ID)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			kernel.Invoke(p.ctx, e, func(r chat.Requirement) { r.Render(c) })
		})
		return true
	}
	return false
}

// askWithoutFolder opens the composer for a chat with no folder. The backend
// answers in its Inbox, with no folder of the user's to read or write.
func (p *Plugin) askWithoutFolder() {
	p.inbox = true
	p.focus = true
	p.ctx.Invalidate()
}

// chooseFolder asks for a folder and gives it to the chat that asked, unless
// the user has moved on while the dialog was open.
func (p *Plugin) chooseFolder() {
	ws, ok := kernel.Use(p.ctx, workspace.ServiceKey)
	if !ok {
		p.host.Toast(ui.Toast{Title: "The folder picker is unavailable"})
		return
	}
	draft := p.conv.DraftID
	ws.ChooseFolder(func(path string) {
		if path == "" || p.conv.DraftID != draft {
			return
		}
		p.SetDirectory(path)
		p.focus = true
	})
}

// folderGateView asks for the folder that everything else depends on.
func (p *Plugin) folderGateView(c *ui.Context, pal native.Palette) {
	ui.Column(c).Fill().Center().Padding(32).Children(func() {
		ui.Column(c).AlignItems(ui.Center).MaxWidth(440).Gap(16).Children(func() {
			ui.Box(c).Size(48, 48).Radius(14).Center().Background(pal.Orange.Alpha(.12)).Children(func() {
				ui.Icon(c, lucide.FolderOpen()).FontSize(22).TextColor(pal.Orange)
			})
			ui.Column(c).AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Text(c, "Choose a work folder").FontSize(24).FontWeight(600).TextColor(pal.Text).TextAlign(ui.Center)
				ui.Text(c, "Wada reads and writes in the folder you choose, so the work stays where you can see it.").
					FontSize(13).TextColor(pal.Muted).TextAlign(ui.Center)
			})
			ui.Row(c).Gap(8).Children(func() {
				components.Button(c, "Open a work folder", components.ButtonPrimary).OnClick(p.chooseFolder)
				components.Button(c, "Ask without a folder", components.ButtonGhost).OnClick(p.askWithoutFolder)
			})
			ui.Text(c, "You can change the folder from the composer.").FontSize(11.5).TextColor(pal.Faint).TextAlign(ui.Center)
		})
	})
}

// starterView draws what plugins offer under the composer of an empty new
// chat, then the keys the composer takes.
func (p *Plugin) starterView(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(14).Padding(10, 24, 22, 24).Children(func() {
		for _, e := range kernel.Items(p.ctx, chat.Starters) {
			c.Key(e.ID)
			kernel.Invoke(p.ctx, e, func(v shell.View) { v.Render(c) })
		}
		p.composerHint(c, pal)
	})
}

// composerHint names the keys the composer takes, then each active trigger.
func (p *Plugin) composerHint(c *ui.Context, pal native.Palette) {
	hint := "Enter sends  ·  Shift+Enter adds a line"
	for _, e := range kernel.Items(p.ctx, chat.Completions) {
		if e.Value.Hint != "" {
			hint += "  ·  " + string(e.Value.Trigger) + " " + e.Value.Hint
		}
	}
	ui.Text(c, hint).FontSize(11.5).TextColor(pal.Faint)
}
