package design

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

// tweaksScript reads the tweaks a page declares, once it has loaded, with
// the values they have now.
//
//go:embed tweaks.js
var tweaksScript string

// tweak is a CSS custom property of a page the user can adjust: a
// "range" of numbers, a "color", a "toggle" between On and Off, or one of
// the Options of a "select". The page declares them in
// <script type="application/json" id="tweaks">.
type tweak struct {
	Var     string   `json:"var"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Step    float64  `json:"step"`
	Unit    string   `json:"unit"`
	On      string   `json:"on"`
	Off     string   `json:"off"`
	Options []string `json:"options"`
	Value   string   `json:"value"` // the value it has now
	Initial string   `json:"-"`     // the value the page gave it

	// What the controls edit.
	num   float64
	on    bool
	color ui.Color
}

// sync sets the controls' values from Value.
func (t *tweak) sync() {
	switch t.Type {
	case "range":
		t.num, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(t.Value), t.Unit), 64)
	case "toggle":
		t.on = t.Value == t.On
	case "color":
		t.color = pageColor(t.Value)
	}
}

// changed sets Value from the controls.
func (t *tweak) changed() {
	switch t.Type {
	case "range":
		t.Value = strconv.FormatFloat(t.num, 'f', -1, 64) + t.Unit
	case "toggle":
		t.Value = t.Off
		if t.on {
			t.Value = t.On
		}
	case "color":
		t.Value = fmt.Sprintf("#%02x%02x%02x", t.color.R, t.color.G, t.color.B)
	}
}

// How long readTweaks waits for a page to load, and how often it asks.
const (
	loadWait  = 15 * time.Second
	loadCheck = 100 * time.Millisecond
)

// readTweaks asks the artboard's page for its tweaks once it shows the
// file and has loaded. Until then the page answers null, and it asks again.
func (p *Plugin) readTweaks(path string, b *board) {
	web := b.web
	name, _ := json.Marshal(filepath.Base(path))
	script := "const file = " + string(name) + ";\n" + tweaksScript
	kernel.Go(p.navigation, &b.reading, func(ctx context.Context) ([]tweak, error) {
		var v any
		for deadline := time.Now().Add(loadWait); ; {
			var err error
			if v, err = web.Eval(ctx, script); err != nil || v != nil {
				if err != nil {
					return nil, err
				}
				break
			}
			if time.Now().After(deadline) {
				return nil, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(loadCheck):
			}
		}
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var list []tweak
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}
		return list, nil
	}, func(list []tweak, err error) {
		if err != nil {
			list = nil // a page whose tweaks can't be read has none to adjust
		}
		for i := range list {
			list[i].Initial = list[i].Value
			list[i].sync()
		}
		b.tweaks = list
		p.renderRaster(path, true)
	})
}

// applyTweaks sets every tweak of the artboard in its page.
func (p *Plugin) applyTweaks(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	var script strings.Builder
	script.WriteString("const s = document.documentElement.style;\n")
	for _, t := range b.tweaks {
		name, _ := json.Marshal(t.Var)
		value, _ := json.Marshal(t.Value)
		fmt.Fprintf(&script, "s.setProperty(%s, %s);\n", name, value)
	}
	web, code := b.web, script.String()
	kernel.Go(p.navigation, &b.applying, func(ctx context.Context) (any, error) {
		return web.Eval(ctx, code)
	}, func(_ any, err error) {
		if err == nil {
			p.renderRaster(path, true)
		}
	})
}

// resetTweaks gives every tweak the value the page gave it.
func (p *Plugin) resetTweaks(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	for i := range b.tweaks {
		b.tweaks[i].Value = b.tweaks[i].Initial
		b.tweaks[i].sync()
	}
	p.applyTweaks(path)
}

// keepTweaks asks the agent, in the composer, to write the tweaks the user
// changed into the artboard.
func (p *Plugin) keepTweaks(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	var lines []string
	for _, t := range b.tweaks {
		if t.Value != t.Initial {
			lines = append(lines, "- "+t.Var+": "+t.Value)
		}
	}
	if len(lines) == 0 {
		return
	}
	p.chat.SetDraft(appendDraft(p.chat.DraftText(),
		"In "+rel(p.project, path)+", make these the values:\n"+strings.Join(lines, "\n")))
}

// tweaked reports that a tweak of the artboard has another value than the
// page gave it.
func (b *board) tweaked() bool {
	for _, t := range b.tweaks {
		if t.Value != t.Initial {
			return true
		}
	}
	return false
}

// pageColor reads a page's "#rgb" or "#rrggbb" value, or black. It is the
// page's own color, which the user adjusts, not one of the app's.
func pageColor(s string) ui.Color {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return ui.RGB(0, 0, 0)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return ui.RGB(0, 0, 0)
	}
	return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v))
}
