package tabs

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
)

func isChat(t tabs.Tab) bool { return t.Kind == tabs.Conversation || t.Kind == tabs.Draft }

func (p *Plugin) chatFor(folder string) string {
	for _, id := range []string{p.active, p.chatTab} {
		if i := p.find(id); i >= 0 && isChat(p.tabs[i]) && p.tabs[i].Folder == folder {
			return id
		}
	}
	for _, t := range p.tabs {
		if isChat(t) && t.Folder == folder {
			return t.ID
		}
	}
	return ""
}

func (p *Plugin) showBeside(t tabs.Tab) {
	if _, ok := p.paneTab(t.ID); !ok {
		return
	}
	id := p.chatFor(t.Folder)
	if id == "" {
		return
	}
	p.beside[t.Folder] = t.ID
	p.Select(id)
	p.syncLayout()
	p.ctx.Invalidate()
}

func (p *Plugin) closeSplit(folder string) {
	delete(p.beside, folder)
	p.syncLayout()
	p.ctx.Invalidate()
}

func (p *Plugin) splitTab() tabs.Tab {
	i := p.find(p.active)
	if i < 0 || !isChat(p.tabs[i]) {
		return tabs.Tab{}
	}
	t, _ := p.paneTab(p.beside[p.tabs[i].Folder])
	return t
}

func (p *Plugin) syncLayout() {
	p.syncPane()
	i := p.find(p.active)
	if i < 0 || !isChat(p.tabs[i]) {
		return
	}
	want := chat.PageID
	if p.splitTab().ID != "" {
		want = panePage
	}
	if p.host.Page() != want {
		wasSelecting := p.selecting
		p.selecting = true
		p.host.ShowPage(want)
		p.selecting = wasSelecting
	}
}

func (p *Plugin) renderSplit(c *ui.Context, t tabs.Tab) {
	pal := native.PaletteFor(c)
	ui.Row(c.Key("split")).Fill().Children(func() {
		ui.Column(c.Key("chat")).Grow(1).Width(0).MinWidth(0).FillHeight().Children(func() {
			for _, e := range kernel.Items(p.ctx, shell.Pages) {
				if e.ID == chat.PageID {
					kernel.Invoke(p.ctx, e, func(pg shell.Page) {
						if pg.Render != nil {
							pg.Render(c)
						}
					})
					break
				}
			}
		})
		ui.Column(c.Key(t.ID)).Grow(1).Width(0).MinWidth(0).FillHeight().
			BorderWidth(0, 0, 0, 1).BorderColor(pal.Hairline).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Padding(4, 8).Children(func() {
				ui.Text(c, p.titled(t).Title).SingleLine().Ellipsis("…").TextColor(pal.Text)
				components.Button(c, "Close split", components.ButtonGhost).OnClick(func() { p.closeSplit(t.Folder) })
			})
			ui.Column(c.Key("companion")).Grow(1).MinHeight(0).FillWidth().Children(func() {
				p.invokePane(t.Pane, func(k tabs.PaneKind) { k.Render(c, t) })
			})
		})
	})
}
