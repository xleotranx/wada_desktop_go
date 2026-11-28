// Package browser shows web pages as tabs: a workspace's running service, or
// any address, beside the conversation that works on it.
package browser

import (
	"net"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/browser"
	"wada-desktop-go/internal/contract/tabs"
)

// Plugin owns a web page for each browser tab.
type Plugin struct {
	ctx    *kernel.Context
	host   shell.Host
	pages  map[string]*page  // by tab ID
	titles map[string]string // the page title of each tab's address, by Ref

	openOn   bool
	openText string
}

// page is one tab's web page and what its toolbar shows.
type page struct {
	view    shell.WebView
	off     func() // stops following the page's changes
	ref     string
	address string // the address field
	problem string
	back    bool
	forward bool
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "browser", Title: "Browser",
		Description: "Web pages as tabs, such as a workspace's running service.",
		Requires:    []kernel.Named{shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	p.pages = map[string]*page{}
	p.titles = map[string]string{}
	c.OnDispose(func() {
		for _, pg := range p.pages {
			pg.close()
		}
	})
	kernel.Contribute(c, tabs.Panes, browser.PaneKind, 40, tabs.PaneKind{
		Title: p.tabTitle, Icon: lucide.Globe, Render: p.paneView, Show: p.show, Hide: p.prune})
	kernel.Contribute(c, kernel.Commands, "browser.open", 0, kernel.Command{
		Title: "Open Address…", Menu: "File", Keys: []string{"shift+cmd+l"}, Run: func() { p.openOn, p.openText = true, "" },
		Enabled: func() bool { _, ok := kernel.Use(c, tabs.ServiceKey); return ok }})
	return kernel.Contribute(c, shell.Overlays, "browser.dialog", 0, shell.View{Render: p.dialog})
}

// open shows address in a browser tab of the space on show.
func (p *Plugin) open(address string) {
	if t, ok := kernel.Use(p.ctx, tabs.ServiceKey); ok && strings.TrimSpace(address) != "" {
		t.Open(browser.PaneKind, Normalize(address), t.Space())
	}
}

// tabTitle is the page's title once it has one, else the address's host.
func (p *Plugin) tabTitle(ref string) string {
	if t := p.titles[ref]; t != "" {
		return t
	}
	if u, err := url.Parse(Normalize(ref)); err == nil && u.Host != "" {
		return u.Host
	}
	return "Browser"
}

// show makes the tab's page the first time it shows and opens its address.
func (p *Plugin) show(t tabs.Tab) {
	if p.pages[t.ID] != nil {
		return
	}
	pg := &page{ref: t.Ref, address: Normalize(t.Ref)}
	p.pages[t.ID] = pg
	v, err := p.host.NewWebView()
	if err != nil {
		pg.problem = "This window can't show web pages: " + err.Error()
		return
	}
	pg.view = v
	// Changes come from the page's own thread; read them on the UI thread.
	pg.off = v.OnChange(func() { p.ctx.Post(func() { p.changed(pg) }) })
	p.navigate(pg, pg.address)
}

// changed reads where the page went and what it is called.
func (p *Plugin) changed(pg *page) {
	if pg.view == nil {
		return
	}
	if u := pg.view.URL(); u != "" {
		pg.address = u
	}
	p.titles[pg.ref] = pg.view.Title()
	pg.back, pg.forward = pg.view.CanGoBack(), pg.view.CanGoForward()
	p.ctx.Invalidate()
}

// prune closes the pages of tabs that closed.
func (p *Plugin) prune() {
	t, ok := kernel.Use(p.ctx, tabs.ServiceKey)
	if !ok {
		return
	}
	open := map[string]bool{}
	for _, tab := range t.Tabs() {
		open[tab.ID] = true
	}
	for id, pg := range p.pages {
		if !open[id] {
			pg.close()
			delete(p.titles, pg.ref)
			delete(p.pages, id)
		}
	}
}

func (pg *page) close() {
	if pg.off != nil {
		pg.off()
	}
	if pg.view != nil {
		pg.view.Close()
	}
}

// navigate opens address in the tab's page; where it goes after that, as by
// a link or a redirect, comes back through changed.
func (p *Plugin) navigate(pg *page, address string) {
	if pg.view == nil {
		return
	}
	address = Normalize(address)
	pg.address, pg.problem = address, ""
	if err := pg.view.LoadURL(address); err != nil {
		pg.problem = err.Error()
	}
	p.ctx.Invalidate()
}

// Normalize makes what was typed an address: a scheme where it has none,
// http for this machine and local network names, https otherwise.
func Normalize(address string) string {
	a := strings.TrimSpace(address)
	if a == "" || strings.Contains(a, "://") || strings.HasPrefix(a, "about:") {
		return a
	}
	host := a
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	local := host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		net.ParseIP(strings.Trim(host, "[]")) != nil
	if local || !strings.Contains(host, ".") {
		return "http://" + a
	}
	return "https://" + a
}

func (p *Plugin) dialog(c *ui.Context) {
	if !p.openOn {
		return
	}
	c.Key("open-address")
	nativeDialog(c, &p.openOn, "Open address", func() {
		c.Key("address")
		components.TextBox(c, &p.openText, components.TextBoxOptions{}).FillWidth().Focus().
			Placeholder("localhost:3000 or https://…").FontSize(13).OnSubmit(func() {
			p.open(p.openText)
			p.openOn = false
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.openOn = false })
			components.Button(c, "Open", components.ButtonPrimary).Disabled(strings.TrimSpace(p.openText) == "").
				OnClick(func() {
					p.open(p.openText)
					p.openOn = false
				})
		})
	})
}
