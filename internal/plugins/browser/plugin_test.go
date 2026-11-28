package browser

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/browser"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/plugintest"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:3000":      "http://localhost:3000",
		"127.0.0.1:8080/x":    "http://127.0.0.1:8080/x",
		"devbox:5173":         "http://devbox:5173",
		"app.local":           "http://app.local",
		"example.com/docs":    "https://example.com/docs",
		"https://example.com": "https://example.com",
		" http://a.test:1/  ": "http://a.test:1/",
		"":                    "",
		"about:blank":         "about:blank",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTabsLoadAddressesAndFollowThePage(t *testing.T) {
	view := &kittest.WebView{}
	host := &kittest.Host{Data: t.TempDir(), Web: func(int) *kittest.WebView { return view }}
	tb := &plugintest.Tabs{}
	var p *Plugin
	h := kerneltest.New(t, host.New, tb.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))

	p.open("localhost:5173")
	if len(tb.Opened) != 1 || tb.Opened[0] != browser.PaneKind+" http://localhost:5173 " {
		t.Fatalf("opened %q", tb.Opened)
	}
	// Opening shows the tab, which makes its page and loads the address.
	tab := tb.Current
	pg := p.pages[tab.ID]
	if pg == nil || len(view.URLs) != 1 || view.URLs[0] != "http://localhost:5173" || len(view.Loaded) != 0 {
		t.Fatalf("page %+v, urls %q, files %q", pg, view.URLs, view.Loaded)
	}

	// The page follows a link: the address, title and history follow it.
	view.Navigate("http://localhost:5173/settings", "Settings · App")
	h.Flush()
	if pg.address != "http://localhost:5173/settings" || !pg.back || pg.forward {
		t.Fatalf("after the link: %+v", pg)
	}
	if got := p.tabTitle(tab.Ref); got != "Settings · App" {
		t.Fatalf("title %q", got)
	}
	tt := ui.NewTester(func(c *ui.Context) { p.paneView(c, tab) }, 800, 600)
	tt.Frame()
	if !tt.HasText("Web page") || !tt.HasText("Back") {
		t.Fatalf("pane = %q", tt.Texts())
	}

	pg.view.GoBack()
	h.Flush()
	if pg.address != "http://localhost:5173" || pg.back || !pg.forward {
		t.Fatalf("after back: %+v", pg)
	}

	// A typed address loads in the same page.
	p.navigate(pg, "example.com")
	if view.URLs[len(view.URLs)-1] != "https://example.com" {
		t.Fatalf("urls %q", view.URLs)
	}
	view.LoadURLErr = errors.New("bad address")
	p.navigate(pg, "::nope")
	if pg.problem != "bad address" {
		t.Fatalf("problem %q", pg.problem)
	}

	// A closed tab's page closes.
	tb.List = nil
	tb.Current = tabs.Tab{}
	p.prune()
	if !view.Closed || len(p.pages) != 0 {
		t.Fatal("the closed tab's page stayed")
	}
}

func TestNoWebViews(t *testing.T) {
	host := &kittest.Host{Data: t.TempDir()}
	tb := &plugintest.Tabs{}
	var p *Plugin
	kerneltest.New(t, host.New, tb.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	tab := tabs.Tab{ID: "pane:browser.page:1", Kind: tabs.Pane, Pane: browser.PaneKind, Ref: "localhost:1"}
	p.show(tab)
	if pg := p.pages[tab.ID]; pg == nil || !strings.Contains(pg.problem, "can't show web pages") {
		t.Fatalf("page = %+v", pg)
	}
}

func TestAgentPreviewAcceptsOnlyAPathOnTheLocalService(t *testing.T) {
	const origin = "http://127.0.0.1:41000"
	if !localPreviewOrigin(origin) || localPreviewOrigin("http://example.com:41000") || localPreviewOrigin(origin+"/other") {
		t.Fatal("local origin validation failed")
	}
	for _, path := range []string{"https://example.com", "//example.com", "/%2fother", "/\\example.com"} {
		if _, err := previewURL(origin, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if got, err := previewURL(origin, "/settings?tab=1"); err != nil || got != origin+"/settings?tab=1" {
		t.Fatalf("route = %q, %v", got, err)
	}
	for _, action := range []string{"read", "click"} {
		script := previewScript(browser.Request{Origin: origin, Action: action, Value: "Check"})
		if !strings.HasPrefix(script, `if (location.origin !== "http://127.0.0.1:41000")`) {
			t.Fatalf("%s is missing the same-origin check", action)
		}
	}
}
