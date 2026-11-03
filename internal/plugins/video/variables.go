package video

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// variable is one declaration of a composition's data-composition-variables:
// a value the template exposes and the editor lets the user change.
type variable struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Default     any      `json:"default"`
	Options     []string `json:"options"`
	Min         *float64 `json:"min"`
	Max         *float64 `json:"max"`
	Unit        string   `json:"unit"`
	MaxLength   *int     `json:"maxLength"`
}

// variableTypes are the declarations HyperFrames reads.
var variableTypes = map[string]string{
	"string": "string", "number": "number", "color": "string", "boolean": "boolean",
	"enum": "string", "font": "string", "image": "string",
}

// declaredVariables reads the variables a composition file declares, on its
// <html> or on its composition root, and drops the entries that are malformed.
func declaredVariables(doc *element) []variable {
	if doc == nil {
		return nil
	}
	var holder *element
	doc.walk(func(n *element) bool {
		if holder != nil {
			return false
		}
		if n.has("data-composition-variables") {
			holder = n
			return false
		}
		return true
	})
	if holder == nil {
		return nil
	}
	raw, ok := holder.attr("data-composition-variables")
	if !ok || strings.TrimSpace(raw.Value) == "" {
		return nil
	}
	var parsed []variable
	if err := json.Unmarshal([]byte(raw.Value), &parsed); err != nil {
		return nil
	}
	out := make([]variable, 0, len(parsed))
	for _, v := range parsed {
		if v.valid() {
			out = append(out, v)
		}
	}
	return out
}

// valid reports whether a declaration is one the runtime can use: a known
// type, a default of that type, and options for an enum.
func (v variable) valid() bool {
	if v.ID == "" || v.Label == "" {
		return false
	}
	want, ok := variableTypes[v.Type]
	if !ok {
		return false
	}
	if v.Type == "font" || v.Type == "image" {
		if _, ok := v.Default.(string); ok {
			return true
		}
		if m, ok := v.Default.(map[string]any); ok {
			_, hasName := m["name"]
			_, hasURL := m["url"]
			return hasName || hasURL
		}
		return false
	}
	switch want {
	case "string":
		_, ok := v.Default.(string)
		if !ok {
			return false
		}
		if v.Type == "enum" && len(v.Options) == 0 {
			return false
		}
		return true
	case "number":
		_, ok := v.Default.(float64)
		return ok
	case "boolean":
		_, ok := v.Default.(bool)
		return ok
	}
	return false
}

// label is what the panel calls a variable.
func (v variable) label() string {
	if v.Label != "" {
		return v.Label
	}
	return v.ID
}

// defaultString is a declaration's default as the editor writes it: numbers
// without trailing zeros, text as itself, false and true spelled out.
func (v variable) defaultString() string {
	switch value := v.Default.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	case map[string]any:
		if url, ok := value["url"].(string); ok {
			return url
		}
		if name, ok := value["name"].(string); ok {
			return name
		}
	}
	return ""
}

// sameShape reports whether two declarations are the same variable, so the
// editors keep their state across a reload.
func (v variable) sameShape(other variable) bool {
	return v.ID == other.ID && v.Type == other.Type && v.Label == other.Label
}

// mergedVariables layers a composition's defaults under the user's values,
// with the per-mount values of a scene on top, as the platform merges them.
func mergedVariables(defaults, overrides, mount map[string]any) map[string]any {
	out := make(map[string]any, len(defaults)+len(overrides)+len(mount))
	for id, v := range defaults {
		out[id] = v
	}
	for id, v := range overrides {
		out[id] = v
	}
	for id, v := range mount {
		out[id] = v
	}
	return out
}

// scalarValue is a variable value that becomes a CSS custom property or text.
func scalarValue(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(value), true
	}
	return "", false
}

// variablesJSON is a value map as the render CLI reads it.
func variablesJSON(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

// variableSummary says what a declaration is, for its row.
func variableSummary(v variable) string {
	switch v.Type {
	case "enum":
		return "one of " + strings.Join(v.Options, ", ")
	case "number":
		if v.Min != nil && v.Max != nil {
			return fmt.Sprintf("number %g–%g%s", *v.Min, *v.Max, v.Unit)
		}
		return "number"
	case "boolean":
		return "on or off"
	case "color":
		return "color"
	case "font":
		return "font"
	case "image":
		return "image"
	}
	return v.Type
}
