package browser

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

func waitCond(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func TestTabsShowPagesInAFrame(t *testing.T) {
	var mu sync.Mutex
	var evals []string
	frameReady := false
	view := &kittest.WebView{EvalFunc: func(_ context.Context, code string) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		evals = append(evals, code)
		// The frame is there from the second look on, as after a load.
		ok := frameReady
		frameReady = true
		return ok, nil
	}}
	host := &kittest.Host{Data: t.TempDir(), Web: func(int) *kittest.WebView { return view }}
	tb := &plugintest.Tabs{}
	var p *Plugin
	h := kerneltest.New(t, host.New, tb.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))

	p.open("localhost:5173")
	if len(tb.Opened) != 1 || tb.Opened[0] != browser.PaneKind+" http://localhost:5173 " {
		t.Fatalf("opened %q", tb.Opened)
	}
	// Opening shows the tab, which makes its page.
	tab := tb.Current
	waitCond(t, h, "the frame gets the address", func() bool { return p.pages[tab.ID].loaded })
	if len(view.Loaded) != 1 || !strings.HasSuffix(view.Loaded[0], "browser/blank.html") {
		t.Fatalf("loaded %q", view.Loaded)
	}
	mu.Lock()
	last := evals[len(evals)-1]
	mu.Unlock()
	if !strings.Contains(last, `f.src = "http://localhost:5173"`) {
		t.Fatalf("last eval %q", last)
	}
	if got := p.tabTitle("http://localhost:5173"); got != "localhost:5173" {
		t.Fatalf("title %q", got)
	}

	tt := ui.NewTester(func(c *ui.Context) { p.paneView(c, tab) }, 800, 600)
	tt.Frame()
	if !tt.HasText("Web page") || !tt.HasText("Back") || p.pages[tab.ID].address != "http://localhost:5173" {
		t.Fatalf("pane = %q", tt.Texts())
	}

	// Typing another address shows it without loading the page again.
	p.navigate(p.pages[tab.ID], "example.com")
	waitCond(t, h, "the second address", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(evals[len(evals)-1], "https://example.com")
	})
	if len(view.Loaded) != 1 {
		t.Fatalf("the page loaded again: %q", view.Loaded)
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
