package design

import (
	"context"
	"encoding/json"
	"fmt"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// inspectedElement keeps only page data; its outline is drawn with the native palette.
type inspectedElement struct {
	Path string
	element
}

func probeElement(ctx context.Context, web shell.WebView, x, y float32) (element, error) {
	script := fmt.Sprintf("const x = %g, y = %g;\n%s", x, y, probeScript)
	v, err := web.Eval(ctx, script)
	if err != nil {
		return element{}, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return element{}, err
	}
	var el element
	err = json.Unmarshal(data, &el)
	return el, err
}

func (p *Plugin) clearInspection() {
	p.hovering.Cancel()
	p.inspecting.Cancel()
	p.hover, p.inspected = nil, nil
}

// selectBoard keeps the canvas selection and outgoing element target consistent.
func (p *Plugin) selectBoard(path string) {
	if p.inspected != nil && p.inspected.Path != path {
		p.clearInspection()
	}
	p.selected = path
}

func (p *Plugin) inspect(x, y float32, choose bool) {
	task := &p.hovering
	if choose {
		task = &p.inspecting
	}
	a, ok := p.boardAt(x, y)
	if !ok || p.boards[a.Path] == nil {
		task.Cancel()
		p.hover = nil
		if choose {
			p.inspected = nil
		}
		return
	}
	ax, ay, _, _ := p.vp.screen(a)
	px, py := (x-ax)/p.vp.zoom(), (y-ay)/p.vp.zoom()
	web := p.boards[a.Path].web
	kernel.Go(p.navigation, task, func(ctx context.Context) (element, error) {
		return probeElement(ctx, web, px, py)
	}, func(el element, err error) {
		var hit *inspectedElement
		if err == nil && el.Selector != "" {
			hit = &inspectedElement{Path: a.Path, element: el}
		}
		if choose {
			p.inspected = hit
			if hit != nil {
				p.selectBoard(hit.Path)
			}
		} else {
			p.hover = hit
		}
	})
}

// selectionFile names exactly what the user selected, independently of the brief.
// JSON is carried as text because the agent contract accepts text/* attachments.
func (p *Plugin) selectionFile() agent.File {
	hit := p.inspected
	a, _ := p.current().board(hit.Path)
	data, _ := json.MarshalIndent(struct {
		File     string      `json:"file"`
		Selector string      `json:"selector"`
		Text     string      `json:"text"`
		HTML     string      `json:"html"`
		Viewport previewSize `json:"viewport"`
	}{rel(p.project, hit.Path), hit.Selector, hit.Text, hit.HTML, previewSize{a.W, a.H}}, "", "  ")
	return agent.File{Name: "design-selection.json", MIME: "text/plain", Data: data}
}
