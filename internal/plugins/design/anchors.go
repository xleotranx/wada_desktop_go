package design

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xleotranx/wada-kit/kernel"
)

// refreshAnchors follows selectors after a responsive reflow. Wait for the native
// WebView to adopt its new viewport before reading DOM bounds.
func (p *Plugin) refreshAnchors(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	b.anchoring.Cancel()
	type target struct {
		ID       int    `json:"id"`
		Selector string `json:"selector"`
	}
	var targets []target
	for _, m := range p.marks {
		if m.Kind == commentMark && m.Path == path && m.Selector != "" {
			m.Unlocated = true
			targets = append(targets, target{m.ID, m.Selector})
		}
	}
	if len(targets) == 0 {
		return
	}
	a, ok := p.current().board(path)
	if !ok {
		return
	}
	data, _ := json.Marshal(targets)
	file, _ := json.Marshal(filepath.Base(path))
	script := fmt.Sprintf("const targets = %s; const file = %s; const width = %g, height = %g;\n", data, file, a.W, a.H) + `
if (document.readyState !== "complete" || !decodeURIComponent(location.pathname).endsWith("/"+file) ||
    Math.abs(innerWidth-width)>3 || Math.abs(innerHeight-height)>3) return null;
return targets.flatMap(t => {
  let el; try { el=document.querySelector(t.selector); } catch { return []; }
  if (!el) return [];
  const r=el.getBoundingClientRect();
  if (r.width<=0 || r.height<=0) return [];
  return [{id:t.id,x:r.x+Math.min(r.width,22)/2,y:r.y+Math.min(r.height,22)/2}];
});`
	type anchor struct {
		ID   int
		X, Y float32
	}
	web := b.web
	kernel.Go(p.navigation, &b.anchoring, func(ctx context.Context) ([]anchor, error) {
		ctx, cancel := context.WithTimeout(ctx, loadWait)
		defer cancel()
		ticker := time.NewTicker(loadCheck)
		defer ticker.Stop()
		for {
			v, err := web.Eval(ctx, script)
			if err != nil {
				return nil, err
			}
			if v != nil {
				data, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				var anchors []anchor
				err = json.Unmarshal(data, &anchors)
				return anchors, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}, func(anchors []anchor, err error) {
		if err != nil {
			return
		}
		for _, a := range anchors {
			if m := p.mark(a.ID); m != nil && m.Path == path {
				m.X, m.Y = a.X, a.Y
				m.Unlocated = false
			}
		}
	})
}
