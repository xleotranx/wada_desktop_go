package video

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"unicode/utf8"

	kit "wada-desktop-go/internal/contract/videokit"
)

const documentName = "video.json"

var definitionID = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)*$`)
var instanceID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var colorValue = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var assetPath = regexp.MustCompile(`^assets/[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var tokenValue = regexp.MustCompile(`^[a-zA-Z0-9#., %()\-]+$`)

func readDocument(dir string) (*kit.Document, error) {
	data, err := os.ReadFile(filepath.Join(dir, documentName))
	if err != nil {
		return nil, err
	}
	var d kit.Document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("video.json: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("video.json must contain exactly one document")
	}
	return &d, nil
}

func sameRef(a, b kit.Ref) bool { return a == b }
func componentFor(d *kit.Document, ref kit.Ref) (kit.Component, error) {
	for _, c := range d.Snapshot.Components {
		if sameRef(c.Ref, ref) {
			return c, nil
		}
	}
	return kit.Component{}, fmt.Errorf("component %s v%d is absent from the project snapshot", ref.ID, ref.Version)
}
func layoutFor(d *kit.Document, ref kit.Ref) (kit.Layout, error) {
	for _, l := range d.Snapshot.Layouts {
		if sameRef(l.Ref, ref) {
			return l, nil
		}
	}
	return kit.Layout{}, fmt.Errorf("layout %s v%d is absent from the project snapshot", ref.ID, ref.Version)
}
func variableID(scene, part, field string) string { return scene + "--" + part + "--" + field }

func validField(f kit.Field, value any) error {
	v := variable{ID: f.ID, Label: f.Label, Type: f.Type, Default: value, Options: f.Options}
	if !v.valid() {
		return fmt.Errorf("%s requires a valid %s value", f.Label, f.Type)
	}
	if s, ok := value.(string); ok {
		if f.MaxLength > 0 && utf8.RuneCountInString(s) > f.MaxLength {
			return fmt.Errorf("%s exceeds %d characters", f.Label, f.MaxLength)
		}
		if f.Type == "enum" {
			found := false
			for _, o := range f.Options {
				found = found || s == o
			}
			if !found {
				return fmt.Errorf("%s is not an allowed option", f.Label)
			}
		}
		if f.Type == "color" && !colorValue.MatchString(s) {
			return fmt.Errorf("%s needs a six-digit hex color", f.Label)
		}
	}
	if n, ok := value.(float64); ok {
		if math.IsNaN(n) || math.IsInf(n, 0) || (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return fmt.Errorf("%s is outside its limits", f.Label)
		}
		if f.Step > 0 && math.Abs(n/f.Step-math.Round(n/f.Step)) > 1e-8 {
			return fmt.Errorf("%s must use steps of %g", f.Label, f.Step)
		}
	}
	return nil
}

// compileDocument is pure. It validates the whole document before producing any
// output; all filenames are derived from validated instance IDs.
func compileDocument(d *kit.Document) (map[string]string, error) {
	if d.Schema != 1 {
		return nil, fmt.Errorf("unsupported video schema %d", d.Schema)
	}
	if strings.TrimSpace(d.Title) == "" || len(d.Scenes) == 0 || len(d.Scenes) > 100 {
		return nil, errors.New("a video needs a title and 1–100 scenes")
	}
	seenDefs := map[kit.Ref]bool{}
	checkRef := func(r kit.Ref) error {
		if !definitionID.MatchString(r.ID) || r.Version < 1 || seenDefs[r] {
			return fmt.Errorf("invalid or duplicate definition %s v%d", r.ID, r.Version)
		}
		seenDefs[r] = true
		return nil
	}
	for _, c := range d.Snapshot.Components {
		if err := checkRef(c.Ref); err != nil {
			return nil, err
		}
	}
	for _, l := range d.Snapshot.Layouts {
		if err := checkRef(l.Ref); err != nil {
			return nil, err
		}
	}
	var theme *kit.Theme
	for i, t := range d.Snapshot.Themes {
		if err := checkRef(t.Ref); err != nil {
			return nil, err
		}
		if t.Ref == d.Theme {
			theme = &d.Snapshot.Themes[i]
		}
	}
	if theme == nil {
		return nil, errors.New("the theme is absent from the project snapshot")
	}
	var tokens strings.Builder
	for name, value := range theme.Tokens {
		if !instanceID.MatchString(name) || !tokenValue.MatchString(value) {
			return nil, fmt.Errorf("invalid theme token %q", name)
		}
	}
	// A fixed key order keeps generated output stable across reloads.
	tokenNames := sortedKeys(theme.Tokens)
	for _, name := range tokenNames {
		fmt.Fprintf(&tokens, "--%s:%s;", name, theme.Tokens[name])
	}
	outputs := map[string]string{}
	if _, ok := d.Snapshot.Assets[d.Snapshot.RuntimeURL]; !ok {
		return nil, errors.New("the animation runtime must be included in snapshot.assets")
	}
	for name, data := range d.Snapshot.Assets {
		if !assetPath.MatchString(name) {
			return nil, fmt.Errorf("invalid asset path %q", name)
		}
		outputs[name] = data
	}
	if err := validateCSS(d.Snapshot.CSS); err != nil {
		return nil, err
	}
	for _, l := range d.Snapshot.Layouts {
		if err := validateCSS(l.CSS); err != nil {
			return nil, fmt.Errorf("layout %s: %w", l.ID, err)
		}
	}
	for _, c := range d.Snapshot.Components {
		if err := validateCSS(c.CSS); err != nil {
			return nil, fmt.Errorf("component %s: %w", c.ID, err)
		}
	}
	var hosts strings.Builder
	ids := map[string]bool{}
	knownOverrides := map[string]bool{}
	duration := 0.0
	for _, s := range d.Scenes {
		if !instanceID.MatchString(s.ID) || strings.Contains(s.ID, "--") || ids[s.ID] {
			return nil, fmt.Errorf("invalid or duplicate scene id %q", s.ID)
		}
		ids[s.ID] = true
		if !finite(s.Start) || !finite(s.Duration) || s.Start < 0 || s.Duration < 3 || s.Duration > 120 || s.Track < 0 {
			return nil, fmt.Errorf("%s needs a nonnegative start/track and a duration of 3–120 seconds", s.ID)
		}
		l, err := layoutFor(d, s.Layout)
		if err != nil {
			return nil, err
		}
		if l.Width != 1920 || l.Height != 1080 || len(s.Parts) < 1 || len(s.Parts) > l.MaxParts {
			return nil, fmt.Errorf("%s does not fit layout %s (1920×1080, at most %d parts)", s.ID, l.ID, l.MaxParts)
		}
		var body, motion, css strings.Builder
		declarations := []variable{}
		parts := map[string]bool{}
		for i, p := range s.Parts {
			if !instanceID.MatchString(p.ID) || strings.Contains(p.ID, "--") || parts[p.ID] {
				return nil, fmt.Errorf("invalid or duplicate part id %q", p.ID)
			}
			parts[p.ID] = true
			c, err := componentFor(d, p.Component)
			if err != nil {
				return nil, err
			}
			if !componentAllows(c, l.Ref) {
				return nil, fmt.Errorf("%s needs a compatible full-width layout", c.Title)
			}
			values := map[string]any{}
			fields := map[string]bool{}
			for _, f := range c.Fields {
				if !instanceID.MatchString(f.ID) || strings.Contains(f.ID, "--") || fields[f.ID] {
					return nil, fmt.Errorf("invalid or duplicate field %q", f.ID)
				}
				fields[f.ID] = true
				base := f.Default
				if v, ok := p.Inputs[f.ID]; ok {
					base = v
				}
				if err := validField(f, base); err != nil {
					return nil, fmt.Errorf("%s/%s: %w", s.ID, p.ID, err)
				}
				id := variableID(s.ID, p.ID, f.ID)
				knownOverrides[id] = true
				value := base
				if v, ok := d.Overrides[id]; ok {
					value = v
				}
				if err := validField(f, value); err != nil {
					return nil, fmt.Errorf("%s: %w", id, err)
				}
				values[f.ID] = value
				declarations = append(declarations, variable{ID: id, Label: c.Title + " · " + f.Label, Type: f.Type, Default: base, Options: f.Options, Min: f.Min, Max: f.Max, MaxLength: maxLength(f.MaxLength), Description: f.Description})
			}
			for key := range p.Inputs {
				if !fields[key] {
					return nil, fmt.Errorf("unknown input %s/%s/%s", s.ID, p.ID, key)
				}
			}

			split := func(name, separator string, maxItems, maxChars int) ([]string, error) {
				value, ok := values[name].(string)
				if !ok {
					return nil, fmt.Errorf("%s must be text", name)
				}
				return splitItems(value, separator, maxItems, maxChars)
			}
			funcs := template.FuncMap{
				"value":   func(name string) string { return escapeHTML(fmt.Sprint(values[name])) },
				"binding": func(name string) string { return variableID(s.ID, p.ID, name) },
				"split":   split,
				"inc":     func(i int) int { return i + 1 },
				"number": func(name string, min, max float64) (float64, error) {
					return numericValue(values[name], name, min, max)
				},
				"within": func(first, last string, i int, limit ...int) (bool, error) {
					a, err := numericValue(values[first], first, 1, 100)
					if err != nil {
						return false, err
					}
					b, err := numericValue(values[last], last, 1, 100)
					if err != nil {
						return false, err
					}
					if a > b {
						return false, fmt.Errorf("%s must not exceed %s", first, last)
					}
					if len(limit) > 0 && b > float64(limit[0]) {
						return false, fmt.Errorf("%s exceeds the available data", last)
					}
					return float64(i+1) >= a && float64(i+1) <= b, nil
				},
				"series": func(labels, valuesName, separator string, maxItems int, min, max float64) ([]seriesItem, error) {
					names, err := split(labels, separator, maxItems, 20)
					if err != nil {
						return nil, err
					}
					raw, ok := values[valuesName].(string)
					if !ok {
						return nil, fmt.Errorf("%s must be a list of numbers", valuesName)
					}
					numbers := strings.Split(raw, separator)
					if len(numbers) != len(names) {
						return nil, errors.New("every series label needs exactly one value")
					}
					series := make([]seriesItem, 0, len(names))
					for i, text := range numbers {
						v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
						if err != nil {
							return nil, fmt.Errorf("%s contains an invalid number", valuesName)
						}
						v, err = numericValue(v, valuesName, min, max)
						if err != nil {
							return nil, err
						}
						series = append(series, seriesItem{names[i], v})
					}
					return series, nil
				},
				"grid": func(name, headers, rowSeparator, cellSeparator string, maxRows, maxCols, maxChars int) ([][]string, error) {
					header, err := split(headers, cellSeparator, maxCols, maxChars)
					if err != nil {
						return nil, err
					}
					raw, ok := values[name].(string)
					if !ok {
						return nil, fmt.Errorf("%s must be text", name)
					}
					rows := strings.Split(raw, rowSeparator)
					if len(rows) > maxRows {
						return nil, fmt.Errorf("%s supports at most %d rows", name, maxRows)
					}
					result := make([][]string, 0, len(rows))
					for _, row := range rows {
						cells, err := splitItems(row, cellSeparator, maxCols, maxChars)
						if err != nil {
							return nil, err
						}
						if len(cells) != len(header) {
							return nil, fmt.Errorf("%s: every row must match the headers", name)
						}
						result = append(result, cells)
					}
					return result, nil
				},
				"selected": func(name string, i int) bool { n, _ := values[name].(float64); return n == float64(i+1) },
			}

			tmpl, err := template.New(c.ID).Funcs(funcs).Parse(c.Markup)
			if err != nil {
				return nil, err
			}
			var markup bytes.Buffer
			if err := tmpl.Execute(&markup, nil); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", s.ID, p.ID, err)
			}
			boxID := s.ID + "--" + p.ID
			fmt.Fprintf(&body, `<div id="%s" class="part">%s</div>`, boxID, markup.String())
			css.WriteString(scopeCSS(c.CSS, "#"+boxID))
			fmt.Fprintf(&motion, "{ const box=root.querySelector(%q); const cues={enter:%.3f,action:%.3f,reveal:%.3f,exit:%.3f}; %s }\n", "#"+boxID, 0.12*float64(i), s.Duration*0.18, s.Duration*0.58, s.Duration-0.5, c.Motion)
		}
		decl, _ := json.Marshal(declarations)
		file := "compositions/" + s.ID + ".html"
		outputs[file] = fmt.Sprintf(`<template><section id="%s" data-composition-id="%s" data-width="1920" data-height="1080" data-duration="%s" data-composition-variables='%s' class="video-scene" style="position:relative;width:1920px;height:1080px;overflow:hidden;background:var(--background)"><style>%s %s %s</style><div class="scene-heading">%s</div><div class="parts">%s</div></section><script>(function(){const root=document.getElementById(%q);const tl=gsap.timeline({paused:true});%s tl.to({}, {duration:%s},0);window.__timelines=window.__timelines||{};window.__timelines[%q]=tl;})();</script></template>`, s.ID, s.ID, formatSeconds(s.Duration), attrJSON(string(decl)), scopeCSS(d.Snapshot.CSS, "#"+s.ID), scopeCSS(l.CSS, "#"+s.ID), css.String(), escapeHTML(s.Title), body.String(), s.ID, motion.String(), formatSeconds(s.Duration), s.ID)
		fmt.Fprintf(&hosts, `<div id="%s" class="clip" aria-label="%s" data-composition-id="%s" data-composition-src="%s" data-start="%s" data-duration="%s" data-track-index="%d" style="position:absolute;inset:0"></div>`, s.ID, escapeAttr(s.Title), s.ID, file, formatSeconds(s.Start), formatSeconds(s.Duration), s.Track)
		duration = math.Max(duration, s.Start+s.Duration)
	}
	for id := range d.Overrides {
		if !knownOverrides[id] {
			return nil, fmt.Errorf("override %q names no component field", id)
		}
	}
	outputs["index.html"] = fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%s</title><script src="%s"></script><style>html,body{margin:0;width:1920px;height:1080px;overflow:hidden}#main{%s position:relative;width:1920px;height:1080px;background:var(--background)}</style></head><body><main id="main" data-composition-id="main" data-width="1920" data-height="1080" data-fps="30" data-duration="%s">%s</main><script>window.__timelines=window.__timelines||{};const tl=gsap.timeline({paused:true});tl.to({}, {duration:%s},0);window.__timelines["main"]=tl;</script></body></html>`, escapeHTML(d.Title), escapeAttr(d.Snapshot.RuntimeURL), tokens.String(), formatSeconds(duration), hosts.String(), formatSeconds(duration))
	return outputs, nil
}

func finite(n float64) bool      { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func escapeAttr(s string) string { return strings.ReplaceAll(escapeHTML(s), `"`, "&quot;") }
func attrJSON(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&", "&amp;"), "'", "&#39;")
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Collection styles use simple rules; selectors are always scoped to their
// scene or component. No global styles or @ rules are accepted.
func scopeCSS(css, scope string) string {
	var out strings.Builder
	for _, rule := range strings.Split(css, "}") {
		a, b, ok := strings.Cut(rule, "{")
		if !ok {
			continue
		}
		selectors := strings.Split(a, ",")
		for i := range selectors {
			selectors[i] = scope + " " + strings.TrimSpace(selectors[i])
		}
		out.WriteString(strings.Join(selectors, ","))
		out.WriteString("{")
		out.WriteString(b)
		out.WriteString("}")
	}
	return out.String()
}

func writeOutputs(dir string, outputs map[string]string) error {
	for _, name := range sortedKeys(outputs) {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		data := []byte(outputs[name])
		old, err := os.ReadFile(file)
		if err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := writeFileAtomic(file, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
func saveDocument(dir string, d *kit.Document) error {
	outputs, err := compileDocument(d)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, documentName), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return writeOutputs(dir, outputs)
}
func syncDocument(dir string) (*kit.Document, error) {
	d, err := readDocument(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	outputs, err := compileDocument(d)
	if err != nil {
		return nil, err
	}
	return d, writeOutputs(dir, outputs)
}

// newStructuredProject copies definitions by JSON round trip so a project's
// data never aliases the live contribution registry.
func newStructuredProject(dir string, c kit.Collection, t kit.Template) error {
	data, err := json.Marshal(kit.Document{Schema: 1, Title: t.Title, Theme: t.Theme, Scenes: t.Scenes, Snapshot: c})
	if err != nil {
		return err
	}
	var d kit.Document
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	return saveDocument(dir, &d)
}

// changeDocument applies an edit only after the resulting project validates.
func changeDocument(dir string, change func(*kit.Document) error) error {
	d, err := readDocument(dir)
	if err != nil {
		return err
	}
	if err := change(d); err != nil {
		return err
	}
	return saveDocument(dir, d)
}

func structuredTiming(dir, file, id string, start, duration float64, track int) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, documentName))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if file != "index.html" {
		return true, errors.New("component motion is fixed; edit the scene window on the main timeline")
	}
	return true, changeDocument(dir, func(d *kit.Document) error {
		for i := range d.Scenes {
			if d.Scenes[i].ID == id {
				d.Scenes[i].Start = start
				d.Scenes[i].Duration = duration
				if track >= 0 {
					d.Scenes[i].Track = track
				}
				return nil
			}
		}
		return fmt.Errorf("scene %q is missing", id)
	})
}

func structuredField(dir, id string, value any) error {
	return changeDocument(dir, func(d *kit.Document) error {
		if d.Overrides == nil {
			d.Overrides = map[string]any{}
		}
		if value == nil {
			delete(d.Overrides, id)
		} else {
			d.Overrides[id] = value
		}
		return nil
	})
}

// strconv is also used by scene IDs when appending from the collection.
func nextSceneID(d *kit.Document) string {
	for n := 1; ; n++ {
		id := "scene-" + strconv.Itoa(n)
		found := false
		for _, s := range d.Scenes {
			found = found || s.ID == id
		}
		if !found {
			return id
		}
	}
}

func maxLength(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func validateCSS(css string) error {
	if strings.Contains(css, "@") || strings.Count(css, "{") != strings.Count(css, "}") {
		return errors.New("collection CSS requires flat rules without @ directives")
	}
	for _, rule := range strings.Split(css, "}") {
		if strings.Count(rule, "{") > 1 {
			return errors.New("nested collection CSS is unsupported")
		}
	}
	return nil
}

// splitItems is a generic bounded list primitive used by keycaps and grids.
// It escapes every value; collection markup remains separate from user data.
func splitItems(value, separator string, maxItems, maxChars int) ([]string, error) {
	if separator == "" || maxItems < 1 || maxChars < 1 {
		return nil, errors.New("invalid list primitive limits")
	}
	items := strings.Split(value, separator)
	if len(items) > maxItems {
		return nil, fmt.Errorf("at most %d items are supported", maxItems)
	}
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
		if utf8.RuneCountInString(items[i]) > maxChars {
			return nil, fmt.Errorf("items support at most %d characters", maxChars)
		}
		items[i] = escapeHTML(items[i])
	}
	return items, nil
}

type seriesItem struct {
	Label string
	Value float64
}

func numericValue(value any, name string, min, max float64) (float64, error) {
	n, ok := value.(float64)
	if !ok || !finite(n) || n < min || n > max {
		return 0, fmt.Errorf("%s needs a number from %g to %g", name, min, max)
	}
	return n, nil
}
func componentAllows(c kit.Component, l kit.Ref) bool {
	if len(c.Layouts) == 0 {
		return true
	}
	for _, ref := range c.Layouts {
		if ref == l {
			return true
		}
	}
	return false
}
func layoutFits(d *kit.Document, l kit.Layout, parts []kit.Part) bool {
	if len(parts) > l.MaxParts {
		return false
	}
	for _, p := range parts {
		c, err := componentFor(d, p.Component)
		if err != nil || !componentAllows(c, l.Ref) {
			return false
		}
	}
	return true
}
func layoutForParts(d *kit.Document, parts []kit.Part) (kit.Ref, error) {
	for _, l := range d.Snapshot.Layouts {
		if layoutFits(d, l, parts) {
			return l.Ref, nil
		}
	}
	return kit.Ref{}, errors.New("no layout fits these components; add a new scene instead")
}
