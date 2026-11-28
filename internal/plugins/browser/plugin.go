// Package browser shows web pages as tabs: a workspace's running service, or
// any address, beside the conversation that works on it.
package browser

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	ctx   *kernel.Context
	host  shell.Host
	pages map[string]*page // by tab ID
	blank string           // the local page each tab starts from

	openOn   bool
	openText string
}

// page is one tab's web page and what its toolbar shows.
type page struct {
	view    shell.WebView
	address string // the address field: what was last opened
	problem string
	opened  bool // the tab's own address was asked for
	loaded  bool // the local page holding the frame has loaded
	nav     kernel.Task
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
	c.OnDispose(func() {
		for _, pg := range p.pages {
			if pg.view != nil {
				pg.view.Close()
			}
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

func (p *Plugin) tabTitle(ref string) string {
	if u, err := url.Parse(Normalize(ref)); err == nil && u.Host != "" {
		return u.Host
	}
	return "Browser"
}

// show makes the tab's page the first time it shows and goes to its address.
func (p *Plugin) show(t tabs.Tab) {
	pg := p.pages[t.ID]
	if pg == nil {
		v, err := p.host.NewWebView()
		pg = &page{view: v, address: Normalize(t.Ref)}
		if err != nil {
			pg.problem = "This window can't show web pages: " + err.Error()
		}
		p.pages[t.ID] = pg
	}
	if pg.view == nil || pg.opened {
		return
	}
	pg.opened = true
	p.navigate(pg, pg.address)
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
			pg.nav.Cancel()
			if pg.view != nil {
				pg.view.Close()
			}
			delete(p.pages, id)
		}
	}
}

// navigate shows address. A tab's page is a local page, written once into
// the plugin's data folder, whose frame fills it and shows the address: the
// shell's web pages load local files, and a page from a file may not
// navigate away. A site that refuses to be framed shows nothing; Open in
// your browser shows it there.
func (p *Plugin) navigate(pg *page, address string) {
	address = Normalize(address)
	pg.address, pg.problem = address, ""
	view, dir, blank, loaded := pg.view, p.host.DataDir(), p.blank, pg.loaded
	kernel.Go(p.ctx, &pg.nav, func(ctx context.Context) (string, error) {
		if !loaded {
			if blank == "" {
				var err error
				if blank, err = writeBlank(dir); err != nil {
					return "", err
				}
			}
			if err := view.LoadFile(blank); err != nil {
				return blank, err
			}
		}
		// The frame is there once the local page has loaded.
		set := "const f = document.getElementById('page'); if (!f) return false; f.src = " + strconv.Quote(address) + "; return true"
		for range 50 {
			v, err := view.Eval(ctx, set)
			if ok, _ := v.(bool); ok && err == nil {
				return blank, nil
			}
			select {
			case <-ctx.Done():
				return blank, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return blank, errors.New("the page didn't load")
	}, func(path string, err error) {
		if path != "" {
			p.blank = path
		}
		if err != nil {
			pg.problem = err.Error()
		} else {
			pg.loaded = true
		}
		p.ctx.Invalidate()
	})
}

// script runs a navigation in the page, such as going back.
func (p *Plugin) script(pg *page, code string) {
	if pg.view == nil {
		return
	}
	view := pg.view
	kernel.Go(p.ctx, &pg.nav, func(ctx context.Context) (struct{}, error) {
		_, err := view.Eval(ctx, code)
		return struct{}{}, err
	}, func(struct{}, error) {})
}

// writeBlank writes the page tabs start from and returns its path.
func writeBlank(dataDir string) (string, error) {
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	dir := filepath.Join(dataDir, "browser")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "blank.html")
	return path, os.WriteFile(path, []byte(blankPage), 0o600)
}

// blankPage holds the page a tab shows in a frame that fills it.
const blankPage = `<!doctype html><meta charset=utf-8><title></title>
<style>html,body{margin:0;height:100%;background:#fff}iframe{border:0;width:100%;height:100%;display:block}</style>
<iframe id=page allow="clipboard-read; clipboard-write; fullscreen"></iframe>`

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
