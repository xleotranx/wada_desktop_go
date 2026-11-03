package video

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Composition is one HyperFrames HTML file: the project's root, or a scene it
// mounts. Its clips are the elements the file times; a nested composition's
// own clips belong to that composition, not to this one.
type Composition struct {
	File            string // the file, relative to the project folder
	ID              string // data-composition-id of the root element
	Width, Height   float64
	FPS             float64
	DeclaredSeconds float64 // the root's data-duration, 0 when it has none
	HasDuration     bool
	Clips           []*Clip
	Variables       []variable
	root            *element
	src             string
	registered      []string // composition ids the file registers a timeline for
}

// Clip is one timed element of a composition.
type Clip struct {
	ID            string // id, else data-hf-id, else "tag:line"
	Tag           string
	File          string // the composition file it is written in
	Start         float64
	StartExpr     string // data-start as written, or ""
	HasStart      bool
	Duration      float64
	HasDuration   bool
	Unknown       bool // a media length only its file gives
	Track         int
	TrackKind     string
	Source        string // the src of a media element
	Mount         string // the composition a scene mounts
	Label         string
	Line          int
	CompositionID string         // data-composition-id, on the root and on scene hosts
	MountValues   map[string]any // data-variable-values of a scene host
	at            span
	resolved      bool // the start is known: a number, or a reference to a known clip
}

// End is where the clip stops showing.
func (c *Clip) End() float64 { return c.Start + c.Duration }

// Scene is a composition the root mounts: a window of the root timeline in
// which the scene's own timeline plays.
type Scene struct {
	Host          *Clip        // where the scene sits in the root timeline
	Comp          *Composition // the scene's own file
	PlaybackStart float64      // data-playback-start, the scene's first second
	Rate          float64      // data-playback-rate, 1 when unwritten
}

// Project is a folder with HyperFrames compositions in it.
type Project struct {
	Dir    string
	File   string // the root composition, relative to Dir
	Title  string
	Root   *Composition
	Scenes []*Scene
	FPS    float64
}

// Duration is the length of the whole video: the root's declared duration,
// or the end of its last clip.
func (p *Project) Duration() float64 {
	if p.Root.HasDuration {
		return p.Root.DeclaredSeconds
	}
	end := 0.0
	for _, c := range p.Root.Clips {
		end = math.Max(end, c.End())
	}
	for _, s := range p.Scenes {
		end = math.Max(end, s.Host.End())
	}
	return end
}

// Width and Height are the video's size; the root's, or 1920×1080.
func (p *Project) Size() (w, h float64) {
	if w, h = p.Root.Width, p.Root.Height; w > 0 && h > 0 {
		return w, h
	}
	return 1920, 1080
}

// parseComposition reads one composition file. A file with no timed elements
// still parses: the editor says what it lacks.
func parseComposition(dir, rel, src string) (*Composition, error) {
	doc, err := scanHTML(src)
	if err != nil {
		return nil, err
	}
	c := &Composition{File: rel, Width: 1920, Height: 1080, FPS: 30, src: src}
	c.root, c.ID = compositionRoot(doc)
	if c.root == nil {
		c.registered = registeredTimelines(src)
		return c, nil
	}
	if v := c.root.value("data-width"); v != "" {
		c.Width = parseNumber(v, c.Width)
	}
	if v := c.root.value("data-height"); v != "" {
		c.Height = parseNumber(v, c.Height)
	}
	if v := c.root.value("data-fps"); v != "" {
		c.FPS = parseNumber(v, c.FPS)
	}
	if a, ok := c.root.attr("data-duration"); ok {
		c.DeclaredSeconds = parseNumber(a.Value, 0)
		c.HasDuration = c.DeclaredSeconds > 0
	}
	c.Variables = declaredVariables(doc)
	c.registered = registeredTimelines(src)
	c.readClips()
	return c, nil
}

// compositionRoot finds the element a file's video is: the outermost one
// declaring a composition id. A scene keeps it inside its template.
func compositionRoot(doc *element) (*element, string) {
	var found *element
	doc.walk(func(n *element) bool {
		if found != nil || n == doc {
			return found == nil
		}
		if id := n.value("data-composition-id"); id != "" {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		return nil, ""
	}
	return found, found.value("data-composition-id")
}

// readClips collects the composition's timed elements: everything with a
// data-start, outside a nested composition, which keeps its own local time.
func (c *Composition) readClips() {
	c.root.walk(func(n *element) bool {
		if n == c.root {
			return true
		}
		if n.has("data-composition-id") && !n.has("data-composition-src") {
			return false
		}
		if !n.has("data-start") && !n.has("data-duration") && !n.has("data-composition-src") {
			return true
		}
		c.Clips = append(c.Clips, c.clip(n))
		return !n.has("data-composition-src")
	})
	resolveStarts(c.Clips)
}

// clip reads one timed element.
func (c *Composition) clip(n *element) *Clip {
	cl := &Clip{
		Tag:           n.Tag,
		File:          c.File,
		ID:            n.value("id"),
		StartExpr:     strings.TrimSpace(n.value("data-start")),
		HasStart:      n.has("data-start"),
		Source:        n.value("src"),
		Track:         int(parseNumber(n.value("data-track-index"), 0)),
		TrackKind:     n.value("data-track-kind"),
		Line:          lineOf(c.src, n.Opening.Start),
		at:            n.Opening,
		HasDuration:   n.has("data-duration"),
		CompositionID: n.value("data-composition-id"),
	}
	if raw := strings.TrimSpace(n.value("data-variable-values")); raw != "" {
		var values map[string]any
		if err := json.Unmarshal([]byte(raw), &values); err == nil {
			cl.MountValues = values
		}
	}
	if cl.ID == "" {
		cl.ID = n.value("data-hf-id")
	}
	if cl.ID == "" {
		cl.ID = fmt.Sprintf("%s:%d", n.Tag, cl.Line)
	}
	if cl.HasStart {
		if v, ok := startValue(cl.StartExpr); ok {
			cl.Start = v
		}
	}
	if cl.HasDuration {
		cl.Duration = parseNumber(n.value("data-duration"), 0)
	} else if end := n.value("data-end"); end != "" {
		// data-end is the legacy way to write where a clip stops.
		cl.Duration = math.Max(0, parseNumber(end, 0)-cl.Start)
		cl.HasDuration = true
	} else {
		cl.Unknown = true
	}
	if cl.TrackKind == "" {
		cl.TrackKind = trackKindOf(n)
	}
	if src := n.value("data-composition-src"); src != "" {
		cl.Mount = src
		cl.Label = strings.TrimSuffix(path.Base(src), path.Ext(src))
	}
	cl.Label = clipLabel(n, cl, c.src)
	return cl
}

// trackKindOf is the lane a clip shows in: its own declaration, or the kind
// its tag implies.
func trackKindOf(n *element) string {
	switch n.Tag {
	case "video":
		return "video"
	case "audio":
		return "audio"
	}
	if n.has("data-composition-src") {
		return "graphics"
	}
	return "graphics"
}

// clipLabel names a clip for the timeline: its scene's name, its words, or
// the media's file name.
func clipLabel(n *element, cl *Clip, src string) string {
	if cl.Mount != "" && cl.Label != "" {
		return cl.Label
	}
	if text := n.text(src); text != "" && n.Tag != "video" && n.Tag != "audio" {
		if len(text) > 40 {
			text = strings.TrimSpace(text[:40]) + "…"
		}
		return text
	}
	if cl.Source != "" {
		return strings.TrimSuffix(path.Base(cl.Source), path.Ext(cl.Source))
	}
	if id := n.value("id"); id != "" {
		return id
	}
	return n.Tag
}

// startValue reads a numeric data-start.
func startValue(expr string) (float64, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(expr, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// idPattern is the shape of a clip id a data-start can name: "intro",
// "scene-01", "shot_2". The whole string, so an id with a dash is one id.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// startRef reads a data-start that names another clip: "intro", "intro + 0.5",
// "intro-0.5". It follows HyperFrames' grammar exactly: a whole string that
// looks like an id is a reference, so "scene-01" is an id, not "scene" minus 1.
func startRef(expr string) (id string, sign int, offset float64, ok bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", 1, 0, false
	}
	if _, err := strconv.ParseFloat(expr, 64); err == nil {
		return "", 1, 0, false
	}
	if idPattern.MatchString(expr) {
		return expr, 1, 0, true
	}
	i := len(expr)
	if i == 0 || !isDigit(expr[i-1]) {
		return "", 1, 0, false
	}
	j := i
	for j > 0 && isDigit(expr[j-1]) {
		j--
	}
	if j > 0 && expr[j-1] == '.' {
		j--
		for j > 0 && isDigit(expr[j-1]) {
			j--
		}
	}
	magStart := j
	k := j
	for k > 0 && isSpace(expr[k-1]) {
		k--
	}
	if k == 0 || (expr[k-1] != '+' && expr[k-1] != '-') {
		return "", 1, 0, false
	}
	sign = 1
	if expr[k-1] == '-' {
		sign = -1
	}
	name := strings.TrimSpace(expr[:k-1])
	if !idPattern.MatchString(name) {
		return "", 1, 0, false
	}
	mag, err := strconv.ParseFloat(expr[magStart:i], 64)
	if err != nil {
		return "", 1, 0, false
	}
	return name, sign, mag, true
}

// resolveStarts turns "intro - 0.5" into seconds, once the clip it names has
// a start. A reference to a clip this composition does not have, or a chain
// that loops, leaves the clip at 0 and unresolved: the check reports both.
func resolveStarts(clips []*Clip) {
	byID := make(map[string]*Clip, len(clips))
	for _, c := range clips {
		if _, seen := byID[c.ID]; !seen {
			byID[c.ID] = c
		}
	}
	done := make(map[*Clip]bool, len(clips))
	for range clips {
		progressed := false
		for _, c := range clips {
			if done[c] {
				continue
			}
			if id, sign, offset, ref := startRef(c.StartExpr); ref {
				target, ok := byID[id]
				if !ok || !done[target] {
					continue
				}
				c.Start = target.Start + target.Duration + float64(sign)*offset
				c.resolved = true
				done[c] = true
				progressed = true
				continue
			}
			if v, ok := startValue(c.StartExpr); ok {
				c.Start = v
			}
			c.resolved = true
			done[c] = true
			progressed = true
		}
		if !progressed {
			break
		}
	}
	for _, c := range clips {
		if c.Start < 0 {
			c.Start = 0
		}
	}
}

// unresolvedStart reports whether a clip's start still names another after
// resolution.
func unresolvedStart(c *Clip) bool {
	id, _, _, ref := startRef(c.StartExpr)
	return ref && id != "" && !c.resolved
}

// parseNumber reads a number, or returns fallback.
func parseNumber(s string, fallback float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

// lineOf is 1-based.
func lineOf(src string, at int) int {
	if at > len(src) {
		at = len(src)
	}
	return strings.Count(src[:at], "\n") + 1
}
