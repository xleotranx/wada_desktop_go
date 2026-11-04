package video

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

// variableEditor is one declaration's control: what the panel edits and the
// value the composition shows.
type variableEditor struct {
	variable
	value any // effective saved value when this control was last synchronized
	text  string
	on    bool
	num   float64
	color ui.Color
}

// buildEditors makes the controls for the focused composition's declarations,
// keeping the ones already there for the same variables so an input keeps its
// focus and its words.
func (p *Plugin) buildEditors() {
	var declarations []variable
	if comp := p.comp(); comp != nil {
		declarations = comp.Variables
	}
	existing := make(map[string]*variableEditor, len(p.editors))
	for _, e := range p.editors {
		existing[e.ID] = e
	}
	next := make([]*variableEditor, 0, len(declarations))
	for _, v := range declarations {
		if e, ok := existing[v.ID]; ok && e.sameShape(v) {
			e.variable = v
			if value := p.valueFor(v); !reflect.DeepEqual(e.value, value) {
				e.sync(value)
			}
			next = append(next, e)
			continue
		}
		e := &variableEditor{variable: v}
		e.sync(p.valueFor(v))
		next = append(next, e)
	}
	p.editors = next
}

// valueFor is the value a variable has now: the user's, or its default.
func (p *Plugin) valueFor(v variable) any {
	if p.scene != nil && p.scene.Host != nil {
		if value, ok := p.scene.Host.MountValues[v.ID]; ok {
			return value
		}
	}
	if value, ok := p.values[v.ID]; ok {
		return value
	}
	return v.Default
}

// sync sets an editor's control from a value.
func (e *variableEditor) sync(value any) {
	e.value = value
	e.text = e.defaultString()
	if value != nil {
		if s, ok := scalarValue(value); ok {
			e.text = s
		} else if m, ok := value.(map[string]any); ok {
			if url, ok := m["url"].(string); ok {
				e.text = url
			} else if name, ok := m["name"].(string); ok {
				e.text = name
			}
		}
	}
	e.on, _ = value.(bool)
	if n, ok := value.(float64); ok {
		e.num = n
		// Text inputs of numbers start from the number, not its default.
		e.text = strconv.FormatFloat(n, 'f', -1, 64)
	} else {
		e.num, _ = strconv.ParseFloat(strings.TrimSpace(e.text), 64)
	}
	e.color = hexColor(e.text)
}

// changed is the value the editor's control now holds, or nil when the text is
// not a value of the declared type.
func (e *variableEditor) changed() any {
	switch e.Type {
	case "boolean":
		return e.on
	case "color":
		return hexString(e.color)
	case "number":
		n, err := strconv.ParseFloat(strings.TrimSpace(e.text), 64)
		if err != nil || !finite(n) {
			return nil
		}
		if e.Min != nil && n < *e.Min {
			n = *e.Min
			e.text = strconv.FormatFloat(n, 'f', -1, 64)
		}
		if e.Max != nil && n > *e.Max {
			n = *e.Max
			e.text = strconv.FormatFloat(n, 'f', -1, 64)
		}
		return n
	}
	return e.text
}

// setVariable keeps a value for the open project, shows it in the page and
// remembers it for the next time.
func (p *Plugin) setVariable(id string, value any) {
	if p.project != nil && p.project.Document != nil {
		p.saveStructuredField(id, value)
		return
	}
	if p.dir == "" {
		return
	}
	if value == nil {
		p.clearVariable(id)
		p.applyVariables()
		return
	}
	if p.values == nil {
		p.values = map[string]any{}
	}
	p.values[id] = value
	p.persistValues()
	p.applyVariables()
}

// clearVariable forgets the user's value for a variable.
func (p *Plugin) clearVariable(id string) {
	if _, ok := p.values[id]; !ok {
		return
	}
	delete(p.values, id)
	p.persistValues()
}

// persistValues keeps the values of the open project in the settings.
func (p *Plugin) persistValues() {
	if p.saved == nil {
		p.saved = map[string]map[string]any{}
	}
	if len(p.values) == 0 {
		delete(p.saved, p.dir)
	} else {
		p.saved[p.dir] = maps.Clone(p.values)
	}
	p.ctx.Settings().Set(valuesKey, p.saved)
}

// resetVariable puts a variable back to its declared default.
func (p *Plugin) resetVariable(e *variableEditor) {
	if e != nil && p.project != nil && p.project.Document != nil {
		p.saveStructuredField(e.ID, nil)
		return
	}
	if e == nil {
		return
	}
	p.clearVariable(e.ID)
	e.sync(p.valueFor(e.variable))
	p.applyVariables()
}

// overridden reports that the user gave a variable a value.
func (p *Plugin) overridden(id string) bool {
	_, ok := p.values[id]
	return ok
}

// pinned reports that the focused scene's host fixes a variable's value.
func (p *Plugin) pinned(id string) bool {
	if p.scene == nil || p.scene.Host == nil {
		return false
	}
	_, ok := p.scene.Host.MountValues[id]
	return ok
}

// applyVariables shows the saved override snapshot in the preview page.
func (p *Plugin) applyVariables() {
	if p.web == nil || p.webErr != "" || p.mounting.Running() {
		if p.mounting.Running() {
			p.reloadAgain = true
		}
		return
	}
	web := p.web
	// Send the full snapshot: replacing this job must not lose a different
	// field's preceding change before the browser applied it.
	script := variablesScript(p.values)
	kernel.Go(p.navigation, &p.apply, func(ctx context.Context) (struct{}, error) {
		_, err := web.Eval(ctx, script)
		return struct{}{}, err
	}, func(_ struct{}, err error) {
		if err != nil {
			p.webErr = err.Error()
		}
	})
}

// variablesJSONFor is the project's values as the render CLI takes them.
func (p *Plugin) variablesJSONFor() string {
	if p.values == nil {
		return ""
	}
	return variablesJSON(p.values)
}

// hexColor reads "#rgb" or "#rrggbb".
func hexColor(s string) ui.Color {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return ui.Color{}
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return ui.Color{}
	}
	return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v))
}

// hexString writes a color as "#rrggbb".
func hexString(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
