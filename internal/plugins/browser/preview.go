package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/browser"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
)

const previewTag = "preview:"

func (p *Plugin) setupPreview(c *kernel.Context) {
	kernel.On(c, tabs.Changed, func(struct{}) {
		p.prune()
		p.refreshPreview()
	})
	kernel.On(c, workspace.Changed, func(struct{}) { p.refreshPreview() })
	kernel.On(c, agent.StatusChanged, func(agent.Status) { p.refreshPreview() })
	kernel.Contribute(c, shell.Overlays, "browser.preview", 20, shell.View{Render: p.previewDialog})
	c.Post(p.refreshPreview)
}

func (p *Plugin) currentPreviewSpace() (string, bool) {
	a, aok := kernel.Use(p.ctx, agent.ServiceKey)
	t, tok := kernel.Use(p.ctx, tabs.ServiceKey)
	ws, wok := kernel.Use(p.ctx, workspace.ServiceKey)
	if !aok || !tok || !wok {
		return "", false
	}
	st := a.Status()
	if st.State != agent.Ready || st.RemoteHost != "" {
		return "", false
	}
	w, ok := ws.At(t.Space())
	return string(w.ID), ok
}

func (p *Plugin) refreshPreview() {
	id, _ := p.currentPreviewSpace()
	if id == p.previewSpace {
		return
	}
	if p.previewScope != nil {
		p.previewScope.Dispose()
	}
	p.previewScope, p.previewSpace, p.request, p.previewOpen = nil, id, nil, false
	svc, ok := kernel.Use(p.ctx, browser.PreviewKey)
	if !ok || id == "" {
		return
	}
	scope := p.ctx.NewScope()
	p.previewScope = scope
	scope.Spawn(func(ctx context.Context) {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			if req, err := svc.PendingPreview(ctx, id); err == nil {
				scope.Post(func() { p.receivePreview(req) })
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
}

func (p *Plugin) receivePreview(req *browser.Request) {
	if p.decision.Running() {
		return
	}
	if req == nil {
		p.request, p.previewOpen = nil, false
	} else if req.Workspace == p.previewSpace && (p.request == nil || p.request.ID != req.ID) {
		p.request, p.previewOpen = req, true
	}
}

func (p *Plugin) approvePreview(allow bool) {
	req, scope := p.request, p.previewScope
	svc, ok := kernel.Use(p.ctx, browser.PreviewKey)
	if !ok || req == nil || scope == nil || p.decision.Running() {
		return
	}
	p.previewOpen = false
	kernel.Go(scope, &p.decision, func(ctx context.Context) (*browser.Request, error) {
		return svc.ClaimPreview(ctx, req.ID, allow)
	}, func(claimed *browser.Request, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "Browser request could not be applied", Description: err.Error(), Type: components.ToastError})
			return
		}
		if claimed == nil || !allow {
			return
		}
		p.runPreview(scope, svc, *claimed)
	})
}

func (p *Plugin) runPreview(scope *kernel.Scope, svc browser.Preview, req browser.Request) {
	var view shell.WebView
	var script, message string
	t, ok := kernel.Use(p.ctx, tabs.ServiceKey)
	if !ok || req.Workspace != p.previewSpace || !localPreviewOrigin(req.Origin) {
		message = "the local workspace preview is unavailable"
	} else {
		ref := previewTag + req.Origin
		switch req.Action {
		case "open":
			u, err := previewURL(req.Origin, req.Value)
			if err != nil {
				message = err.Error()
				break
			}
			t.Open(browser.PaneKind, ref, t.Space())
			if pg := p.previewPage(t, ref); pg != nil && pg.view != nil {
				p.navigate(pg, u)
				message = pg.problem
			} else {
				message = "the preview tab could not be opened"
			}
		case "read", "click":
			pg := p.previewPage(t, ref)
			if pg == nil || pg.view == nil {
				message = "open this preview before using it"
			} else {
				view = pg.view
				script = previewScript(req)
			}
		default:
			message = "unknown preview action"
		}
	}
	kernel.Go(scope, &p.decision, func(ctx context.Context) (struct{}, error) {
		out := browser.Reply{ID: req.ID, Error: message}
		if view != nil {
			evalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			value, err := view.Eval(evalCtx, script)
			if err != nil {
				out.Error = "the browser could not complete the approved action"
			} else if text, ok := value.(string); ok && len(text) <= 32768 {
				out.Text = text
			} else {
				out.Error = "the browser returned an invalid result"
			}
		} else if out.Error == "" {
			out.Text = "Navigation started"
		}
		return struct{}{}, svc.ReplyPreview(ctx, out)
	}, func(_ struct{}, _ error) {})
}

func (p *Plugin) previewPage(t tabs.Service, ref string) *page {
	for _, tab := range t.Tabs() {
		if tab.Pane == browser.PaneKind && tab.Folder == t.Space() && tab.Ref == ref {
			return p.pages[tab.ID]
		}
	}
	return nil
}

func localPreviewOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && origin == fmt.Sprintf("http://127.0.0.1:%d", port)
}

func previewURL(origin, route string) (string, error) {
	if route == "" {
		return origin, nil
	}
	u, err := url.Parse(route)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(route, "/") ||
		strings.HasPrefix(u.Path, "//") || strings.Contains(route, "\\") {
		return "", errors.New("the preview can only open a path on its own service")
	}
	return origin + route, nil
}

func previewScript(req browser.Request) string {
	origin, _ := json.Marshal(req.Origin)
	guard := "if (location.origin !== " + string(origin) + ") throw new Error('The preview left its service');\n"
	if req.Action == "read" {
		return guard + "return (document.body?.innerText || '').slice(0, 8000);"
	}
	label, _ := json.Marshal(strings.TrimSpace(req.Value))
	return guard + `
const matches = [...document.querySelectorAll('button[type="button"], button:not([type])')]
  .filter(button => !button.form && !button.disabled && button.getClientRects().length > 0 &&
    getComputedStyle(button).visibility === 'visible' && button.innerText.trim() === ` + string(label) + `);
if (matches.length !== 1) throw new Error('The preview needs one visible button with that label');
matches[0].click();
return 'Clicked the approved button';`
}

func (p *Plugin) previewDialog(c *ui.Context) {
	if !p.previewOpen || p.request == nil {
		return
	}
	req := p.request
	c.Key(req.ID)
	native.Dialog(c, &p.previewOpen, 440, "Agent browser request", func() {
		pal := native.PaletteFor(c)
		ui.Text(c, "Agent browser request").FontSize(15).FontWeight(600).TextColor(pal.Text)
		ui.Text(c, req.Service+" · "+req.Origin).TextColor(pal.Muted)
		if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
			if conv, ok := ws.Lookup(agent.SessionID(req.Session)); ok && conv.Title != "" {
				ui.Text(c, conv.Title).SingleLine().Ellipsis("…").TextColor(pal.Muted)
			}
		}
		action := "Share visible page text with the requesting agent"
		if req.Action == "open" {
			action = "Open " + req.Origin + req.Value
		}
		if req.Action == "click" {
			action = "Click the button labeled “" + req.Value + "”"
		}
		ui.Text(c, action).TextColor(pal.Text)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "Decline", components.ButtonSecondary).OnClick(func() { p.approvePreview(false) })
			components.Button(c, "Allow once", components.ButtonPrimary).OnClick(func() { p.approvePreview(true) })
		})
	})
}
