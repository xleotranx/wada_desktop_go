package video

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// severity is how bad a finding is: an error breaks a render, a warning
// makes the preview or the edit harder, an info is a suggestion.
type severity string

const (
	sevError   severity = "error"
	sevWarning severity = "warning"
	sevInfo    severity = "info"
)

// finding is one check result, with the fix the check suggests.
type finding struct {
	Code     string
	Severity severity
	Message  string
	Fix      string
	File     string // relative to the project
	Line     int
	Clip     string // the element id, when the finding has one
}

// check is a project's findings, in file order.
type check struct {
	Findings []finding
	Errors   int
	Warnings int
}

// clean reports that nothing blocks a render.
func (c check) clean() bool { return c.Errors == 0 }

// message is what the header says about the check: "3 errors", "2 warnings"
// or "No problems".
func (c check) message() string {
	switch {
	case c.Errors > 0 && c.Warnings > 0:
		return fmt.Sprintf("%d error%s, %d warning%s", c.Errors, plural(c.Errors), c.Warnings, plural(c.Warnings))
	case c.Errors > 0:
		return fmt.Sprintf("%d error%s", c.Errors, plural(c.Errors))
	case c.Warnings > 0:
		return fmt.Sprintf("%d warning%s", c.Warnings, plural(c.Warnings))
	}
	return "No problems"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// add records a finding.
func (c *check) add(f finding) {
	switch f.Severity {
	case sevError:
		c.Errors++
	case sevWarning:
		c.Warnings++
	case sevInfo:
	}
	c.Findings = append(c.Findings, f)
}

// checkProject runs the editor's checks over a project: the rules of
// HyperFrames' linter that a timeline and a preview depend on, read from the
// composition files as written.
func checkProject(p *Project) check {
	var c check
	checkRoot(&c, p)
	for _, s := range p.Scenes {
		if s.Comp != nil {
			checkScene(&c, p, s)
		}
	}
	sort.SliceStable(c.Findings, func(i, j int) bool {
		a, b := c.Findings[i], c.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return c
}

// checkRoot checks the project's index.html.
func checkRoot(c *check, p *Project) {
	root := p.Root
	if root.root == nil {
		c.add(finding{Code: "root_missing_composition_id", Severity: sevError, File: root.File, Line: 1,
			Message: "The composition has no element with a data-composition-id.",
			Fix:     `Add data-composition-id="main" to the composition's outer element.`})
		return
	}
	line := lineOf(root.src, root.root.Opening.Start)
	if !root.root.has("data-width") || !root.root.has("data-height") {
		c.add(finding{Code: "root_missing_dimensions", Severity: sevError, File: root.File, Line: line,
			Message: "The root element does not declare data-width and data-height.",
			Fix:     `Add data-width="1920" data-height="1080" to the root element.`})
	}
	if !root.HasDuration {
		if len(root.registered) == 0 {
			c.add(finding{Code: "root_composition_missing_duration_source", Severity: sevError, File: root.File, Line: line,
				Message: "The root has no data-duration and no registered timeline, so the renderer cannot tell how long the video is.",
				Fix:     `Add data-duration="<seconds>" to the root element, or register a paused timeline on window.__timelines.`})
		} else {
			c.add(finding{Code: "root_composition_duration_derived", Severity: sevWarning, File: root.File, Line: line,
				Message: fmt.Sprintf("The root has no data-duration: its length comes from the clips, at least %.1fs.", p.Duration()),
				Fix:     `Add data-duration="<seconds>" to the root element to set the length yourself.`})
		}
	}
	if len(root.registered) == 0 && !strings.HasPrefix(strings.TrimSpace(root.src), "<template") {
		c.add(finding{Code: "missing_timeline_registry", Severity: sevError, File: root.File, Line: line,
			Message: "Nothing registers a timeline on window.__timelines.",
			Fix:     `Register the root's paused timeline: window.__timelines["main"] = tl.`})
	}
	checkClips(c, root, p)
	checkScripts(c, root, root.HasDuration)
	checkSource(c, root)
	checkVariables(c, root)
}

// checkScene checks one scene file against the root that mounts it.
func checkScene(c *check, p *Project, s *Scene) {
	host := s.Host
	if host == nil || s.Comp == nil {
		return
	}
	if host.Mount != "" && host.CompositionID == "" {
		c.add(finding{Code: "host_missing_composition_id", Severity: sevError, File: host.File, Line: host.Line, Clip: host.ID,
			Message: "A scene mounts a composition file but its host has no data-composition-id.",
			Fix:     `Add data-composition-id="<the scene's id>" to the host.`})
	}
	comp := s.Comp
	if comp.root == nil {
		c.add(finding{Code: "missing_composition_file", Severity: sevError, File: comp.File, Line: 1,
			Message: "The scene file the root mounts has no composition in it.",
			Fix:     "Write the scene's markup inside its <template>, with data-composition-id on its root element."})
		return
	}
	if id := host.CompositionID; id != "" && comp.ID != "" && id != comp.ID {
		c.add(finding{Code: "timeline_id_mismatch", Severity: sevError, File: comp.File, Line: lineOf(comp.src, comp.root.Opening.Start),
			Message: fmt.Sprintf("The host mounts %q but the scene's root is %q.", id, comp.ID),
			Fix:     fmt.Sprintf(`Make the scene's data-composition-id %q, or the host's.`, comp.ID)})
	}
	if len(comp.registered) == 0 && len(comp.Clips) > 0 && sceneAnimates(comp) {
		c.add(finding{Code: "gsap_timeline_not_registered", Severity: sevError, File: comp.File, Line: lineOf(comp.src, comp.root.Opening.Start),
			Message: "The scene builds a GSAP timeline but never registers it, so nothing in it animates.",
			Fix:     fmt.Sprintf(`Register the scene's paused timeline: window.__timelines[%q] = tl.`, comp.ID)})
	}
	checkClips(c, comp, p)
	checkScripts(c, comp, comp.HasDuration)
	checkSource(c, comp)
	checkVariables(c, comp)
}

// checkClips checks the timed elements of a composition: their classes,
// durations, start references and media attributes.
func checkClips(c *check, comp *Composition, p *Project) {
	if comp.root == nil {
		return
	}
	rows := topLevel(comp)
	for _, el := range rows {
		if !el.has("data-duration") && !el.has("data-end") && !mediaTag(el.Tag) && !isHost(el) {
			c.add(finding{Code: "timeline_element_missing_timing", Severity: sevError, File: comp.File,
				Line: lineOf(comp.src, el.Opening.Start), Clip: el.value("id"),
				Message: fmt.Sprintf("<%s%s> is a timeline element without data-duration, so the timeline cannot draw where it ends.",
					el.Tag, idOf(el)),
				Fix: fmt.Sprintf(`Add data-duration (in seconds) to <%s%s>.`, el.Tag, idOf(el))})
		}
	}
	comp.root.walk(func(n *element) bool {
		if n == comp.root {
			return true
		}
		if n.has("data-composition-id") {
			return false
		}
		if n.has("data-start") || n.has("data-duration") {
			checkTimed(c, comp, n)
		}
		if avTag(n.Tag) {
			checkMedia(c, comp, n)
		}
		return true
	})
	for _, clip := range comp.Clips {
		if unresolvedStart(clip) && clip.HasStart {
			c.add(finding{Code: "unresolved_start_reference", Severity: sevError, File: comp.File, Line: clip.Line, Clip: clip.ID,
				Message: fmt.Sprintf("%q starts when %q ends, but this composition has no such clip.", clip.ID, clip.StartExpr),
				Fix:     `Name a clip of this composition, or write the start in seconds.`})
		} else if clip.HasStart && strings.TrimSpace(clip.StartExpr) != "" {
			if _, _, _, ref := startRef(clip.StartExpr); !ref {
				if _, ok := startValue(clip.StartExpr); !ok {
					c.add(finding{Code: "invalid_data_start", Severity: sevError, File: comp.File, Line: clip.Line, Clip: clip.ID,
						Message: fmt.Sprintf("data-start=%q is not seconds or a clip reference.", clip.StartExpr),
						Fix:     `Write data-start in seconds ("2.5"), or as a clip id ("intro" or "intro + 0.5").`})
				}
			}
		}
	}
}

// checkTimed checks one clip's class and duration.
func checkTimed(c *check, comp *Composition, n *element) {
	line := lineOf(comp.src, n.Opening.Start)
	if !n.hasClass("clip") && !mediaTag(n.Tag) && !isHost(n) {
		c.add(finding{Code: "timed_element_missing_clip_class", Severity: sevWarning, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<%s%s> has timing attributes but no class=\"clip\".", n.Tag, idOf(n)),
			Fix:     `Add class="clip" so the editor recognises it as a clip.`})
	}
	if n.has("data-duration") {
		if v := parseNumber(n.value("data-duration"), -1); v < 0 {
			c.add(finding{Code: "invalid_data_duration", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
				Message: fmt.Sprintf("data-duration=%q is not a number of seconds.", n.value("data-duration")),
				Fix:     `Write data-duration in seconds, as a number.`})
		}
	}
	if n.has("data-composition-src") && n.value("data-composition-id") == "" {
		c.add(finding{Code: "host_missing_composition_id", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
			Message: "A scene host has data-composition-src but no data-composition-id.",
			Fix:     `Add data-composition-id="<the scene's id>" to the host.`})
	}
}

// checkMedia checks one media element's attributes.
func checkMedia(c *check, comp *Composition, n *element) {
	line := lineOf(comp.src, n.Opening.Start)
	src, _ := n.attr("src")
	hasSrc := src.Value != "" || hasSourceChild(n) || n.has("data-var-src")
	if hasSrc && !n.has("data-start") {
		c.add(finding{Code: "media_missing_data_start", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<%s%s> has a source but no data-start, so the runtime cannot own its playback.", n.Tag, idOf(n)),
			Fix:     `Add data-start="0" (or when it should show) and data-duration if it stops before its end.`})
	}
	if n.has("data-start") && n.value("id") == "" {
		c.add(finding{Code: "media_missing_id", Severity: sevError, File: comp.File, Line: line,
			Message: fmt.Sprintf("<%s> has data-start but no id: the renderer cannot find it.", n.Tag),
			Fix:     fmt.Sprintf(`Add a unique id: <%s id="my-%s" ...>.`, n.Tag, n.Tag)})
	}
	if n.has("data-start") && n.value("id") != "" && !hasSrc {
		c.add(finding{Code: "media_missing_src", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<%s%s> has data-start but no src.", n.Tag, idOf(n)),
			Fix:     `Add src on the element, or a <source src="..."> child.`})
	}
	if n.Tag == "video" && n.has("data-start") && !n.has("muted") && !n.has("data-has-audio") {
		c.add(finding{Code: "video_missing_muted", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<video%s> has data-start but declares neither muted nor data-has-audio.", idOf(n)),
			Fix:     `Add data-has-audio="true" when the file has sound, or muted for silent footage.`})
	}
	if n.Tag == "video" && n.has("muted") && n.value("data-has-audio") == "true" {
		c.add(finding{Code: "video_audio_double_source", Severity: sevWarning, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<video%s> is muted and declares data-has-audio=\"true\" as well.", idOf(n)),
			Fix:     `Keep one: muted for a silent clip, data-has-audio="true" for sound.`})
	}
	if n.has("crossorigin") {
		c.add(finding{Code: "media_crossorigin_breaks_preview", Severity: sevError, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("<%s%s> has crossorigin, which makes a CORS-checked fetch the preview cannot always complete.", n.Tag, idOf(n)),
			Fix:     `Remove crossorigin unless the media is read back through canvas or WebAudio.`})
	}
	if strings.HasPrefix(src.Value, "/") && !strings.HasPrefix(src.Value, "//") {
		c.add(finding{Code: "root_path_missing_in_preview", Severity: sevWarning, File: comp.File, Line: line, Clip: n.value("id"),
			Message: fmt.Sprintf("%q starts at the server's root, which the preview does not have.", src.Value),
			Fix:     fmt.Sprintf(`Use a project-relative path: %q.`, strings.TrimPrefix(src.Value, "/"))})
	}
}

// checkScripts checks a file's scripts for the code that makes a composition
// render differently every time, and for the timelines that never arrive.
func checkScripts(c *check, comp *Composition, hasDuration bool) {
	for _, s := range scripts(comp.src) {
		code := stripComments(stripStrings(s.text))
		for _, bad := range nondeterministic {
			if bad.re.MatchString(code) {
				c.add(finding{Code: "non_deterministic_code", Severity: sevError, File: comp.File, Line: s.line,
					Message: bad.label + " makes a frame depend on the wall clock or on chance, so two renders differ.",
					Fix:     bad.fix})
			}
		}
		if requestAnimationFrame.MatchString(code) {
			c.add(finding{Code: "requestanimationframe_in_composition", Severity: sevError, File: comp.File, Line: s.line,
				Message: "requestAnimationFrame runs on wall-clock time, not the timeline, so it will not sync with frames.",
				Fix:     `Use GSAP tweens or onUpdate callbacks instead of requestAnimationFrame.`})
		}
		if infiniteRepeat.MatchString(code) {
			sev, detail := sevError, "without a finite data-duration the timeline never ends."
			if hasDuration {
				sev, detail = sevWarning, "the composition's data-duration still bounds the render."
			}
			c.add(finding{Code: "gsap_infinite_repeat", Severity: sev, File: comp.File, Line: s.line,
				Message: "GSAP tween uses repeat: -1 (infinite), " + detail,
				Fix:     "Use a finite repeat count, or compute one from the composition's duration."})
		}
		if imperativeMedia.MatchString(code) {
			c.add(finding{Code: "imperative_media_control", Severity: sevError, File: comp.File, Line: s.line,
				Message: "The script plays, pauses or seeks media itself; the runtime owns playback and the two will disagree.",
				Fix:     `Remove play/pause/currentTime calls: time the media with data-start and data-duration.`})
		}
	}
}

// checkVariables checks a composition's variable declarations and the
// bindings that name them.
func checkVariables(c *check, comp *Composition) {
	doc, err := scanHTML(comp.src)
	if err != nil {
		return
	}
	holder := variablesHolder(doc)
	hasHTML := false
	doc.walk(func(n *element) bool {
		if n.Tag == "html" {
			hasHTML = true
			return false
		}
		return true
	})
	if holder == nil && !hasHTML {
		// A fragment's values come from the host that mounts it.
		return
	}
	var entries []json.RawMessage
	parsed := holder != nil
	if parsed {
		raw := strings.TrimSpace(holder.value("data-composition-variables"))
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			c.add(finding{Code: "invalid_composition_variables_declaration", Severity: sevError, File: comp.File,
				Line:    lineOf(comp.src, holder.Opening.Start),
				Message: "data-composition-variables is not valid JSON.",
				Fix:     `Give it a JSON array of declarations: [{"id":"title","type":"string","label":"Title","default":"Hello"}].`})
			parsed = false
			entries = nil
		}
	}
	declared := map[string]bool{}
	srcBound := map[string]bool{}
	doc.walk(func(n *element) bool {
		if id := strings.TrimSpace(n.value("data-var-src")); id != "" {
			srcBound[id] = true
		}
		return true
	})
	if parsed {
		line := lineOf(comp.src, holder.Opening.Start)
		for i, raw := range entries {
			var entry map[string]any
			if err := json.Unmarshal(raw, &entry); err != nil {
				c.add(declarationFinding(comp, line, i, "is not an object"))
				continue
			}
			if id, ok := entry["id"].(string); ok && id != "" {
				declared[id] = true
			}
			var missing []string
			if _, ok := entry["id"].(string); !ok {
				missing = append(missing, "id")
			}
			if t, ok := entry["type"].(string); !ok || variableTypes[t] == "" {
				missing = append(missing, "type")
			}
			if _, ok := entry["label"].(string); !ok {
				missing = append(missing, "label")
			}
			if _, ok := entry["default"]; !ok {
				missing = append(missing, "default")
			}
			if len(missing) > 0 {
				c.add(declarationFinding(comp, line, i, "is missing or has invalid: "+strings.Join(missing, ", ")))
				continue
			}
			var v variable
			if err := json.Unmarshal(raw, &v); err != nil || !v.valid() {
				c.add(declarationFinding(comp, line, i, "has a default that does not match its type, or an enum without options"))
				continue
			}
			if url, ok := mediaDefault(v); ok && (v.Type == "image" || srcBound[v.ID]) && url != "" && !safeMediaURL(url) {
				c.add(finding{Code: "unloadable_media_variable_default", Severity: sevError, File: comp.File, Line: line,
					Message: fmt.Sprintf("Variable %q defaults to a URL the runtime refuses to load, so whatever binds it keeps its authored fallback.", v.ID),
					Fix:     `Media URLs must be relative, http(s), blob:, or a data:image/* URI.`})
			}
		}
	}
	doc.walk(func(n *element) bool {
		for _, name := range []string{"data-var-src", "data-var-text"} {
			id := strings.TrimSpace(n.value(name))
			if id == "" || declared[id] {
				continue
			}
			c.add(finding{Code: "unknown_variable_binding", Severity: sevWarning, File: comp.File,
				Line: lineOf(comp.src, n.Opening.Start), Clip: n.value("id"),
				Message: fmt.Sprintf("<%s> binds %s=%q but no variable %q is declared here, so the binding keeps the authored fallback.", n.Tag, name, id, id),
				Fix:     fmt.Sprintf(`Declare %q in data-composition-variables, or fix the binding.`, id)})
		}
		return true
	})
}

// variablesHolder is the element that declares the composition's variables:
// its <html>, or its composition root.
func variablesHolder(doc *element) *element {
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
	return holder
}

// declarationFinding is a malformed declaration entry.
func declarationFinding(comp *Composition, line, index int, detail string) finding {
	return finding{Code: "invalid_composition_variables_declaration", Severity: sevError, File: comp.File, Line: line,
		Message: fmt.Sprintf("data-composition-variables entry [%d] %s.", index, detail),
		Fix:     fmt.Sprintf(`Give entry [%d] a string id and label, a supported type, and a default of that type.`, index)}
}

// mediaDefault is a media variable's default URL, for an image or a string.
func mediaDefault(v variable) (string, bool) {
	switch value := v.Default.(type) {
	case string:
		return value, true
	case map[string]any:
		if url, ok := value["url"].(string); ok {
			return url, true
		}
	}
	return "", false
}

// safeMediaURL is the scheme allowlist the runtime applies to a media
// variable: relative, http(s), blob, and image data URIs.
func safeMediaURL(raw string) bool {
	normalized := strings.Map(func(r rune) rune {
		if r <= ' ' {
			return -1
		}
		return r
	}, raw)
	m := urlScheme.FindStringSubmatch(normalized)
	if m == nil {
		return true
	}
	switch strings.ToLower(m[1]) {
	case "https", "http", "blob":
		return true
	case "data":
		return strings.HasPrefix(strings.ToLower(normalized), "data:image/")
	}
	return false
}

// checkSource checks what is written in a file's markup rather than in its
// elements: self-closing media, unbalanced styles, escaped asset paths.
func checkSource(c *check, comp *Composition) {
	for _, m := range selfClosingMedia.FindAllStringSubmatchIndex(comp.src, -1) {
		c.add(finding{Code: "self_closing_media_tag", Severity: sevError, File: comp.File,
			Line:    lineOf(comp.src, m[0]),
			Message: "A self-closing <" + comp.src[m[2]:m[3]] + "/> is invalid HTML: the browser leaves it open and swallows what follows.",
			Fix:     "Write an explicit close tag: <video ...></video>."})
	}
	if opens, closes := strings.Count(strings.ToLower(comp.src), "<style"), strings.Count(strings.ToLower(comp.src), "</style"); opens != closes {
		c.add(finding{Code: "unbalanced_style_tags", Severity: sevError, File: comp.File, Line: 1,
			Message: fmt.Sprintf("%d <style> blocks but %d closers: markup after an unclosed block is read as CSS.", opens, closes),
			Fix:     "Pair every <style> with </style>."})
	}
	comp.root.walk(func(n *element) bool {
		if n.Tag == "style" && n.Inner.End <= len(comp.src) && n.Inner.Start <= n.Inner.End {
			for _, m := range negativeZIndex.FindAllStringSubmatch(comp.src[n.Inner.Start:n.Inner.End], -1) {
				c.add(finding{Code: "negative_z_index", Severity: sevWarning, File: comp.File,
					Line:    lineOf(comp.src, n.Inner.Start),
					Message: "A negative z-index paints the element behind its nearest stacking context's own content, where an opaque background hides it.",
					Fix:     "Give the element's parent a stacking context: isolation: isolate is the cheapest."})
				_ = m
			}
		}
		for _, name := range []string{"src", "href"} {
			v := n.value(name)
			if v == "" || strings.HasPrefix(v, "http") || strings.HasPrefix(v, "data:") || strings.HasPrefix(v, "#") {
				continue
			}
			if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
				c.add(finding{Code: "root_path_missing_in_preview", Severity: sevWarning, File: comp.File,
					Line: lineOf(comp.src, n.Opening.Start), Clip: n.value("id"),
					Message: fmt.Sprintf("%s=%q starts at the server's root, which the preview does not have.", name, v),
					Fix:     fmt.Sprintf(`Use a project-relative path: %q.`, strings.TrimPrefix(v, "/"))})
			}
			if strings.HasPrefix(v, "../") && escapesProject(n, comp) {
				c.add(finding{Code: "invalid_parent_traversal_in_asset_path", Severity: sevError, File: comp.File,
					Line: lineOf(comp.src, n.Opening.Start), Clip: n.value("id"),
					Message: fmt.Sprintf("%s=%q climbs out of the project, so the render cannot find it.", name, v),
					Fix:     `Keep assets inside the project: assets/…, relative to the project root.`})
			}
		}
		return true
	})
}

// escapesProject reports whether a "../" path from a composition leaves the
// project folder.
func escapesProject(n *element, comp *Composition) bool {
	from := path.Dir(comp.File)
	for _, name := range []string{"src", "href"} {
		v := n.value(name)
		if !strings.HasPrefix(v, "../") {
			continue
		}
		if strings.HasPrefix(path.Clean(path.Join(from, v)), "..") {
			return true
		}
	}
	return false
}

// sceneAnimates reports whether a composition's file has scripts that build
// animation, rather than a file with none that needs no timeline.
func sceneAnimates(comp *Composition) bool {
	for _, s := range scripts(comp.src) {
		if strings.Contains(s.text, "gsap") || strings.Contains(s.text, "animate(") {
			return true
		}
	}
	return false
}

// nondeterministic are the calls that make a frame depend on when or where
// it is rendered.
var nondeterministic = []struct {
	re    *regexp.Regexp
	label string
	fix   string
}{
	{regexp.MustCompile(`\bDate\.now\s*\(`), "Date.now()", "Remove time-dependent code; use the GSAP timeline position instead."},
	{regexp.MustCompile(`\bperformance\.now\s*\(`), "performance.now()", "Remove time-dependent code; use the GSAP timeline position instead."},
	{regexp.MustCompile(`\bnew\s+Date\s*\(`), "new Date()", "Remove time-dependent code; use fixed values."},
	{regexp.MustCompile(`\bMath\.random\s*\(`), "Math.random()", "Use a seeded PRNG so every frame is the same."},
	{regexp.MustCompile(`\bcrypto\.getRandomValues\s*\(`), "crypto.getRandomValues()", "Use a seeded PRNG so every frame is the same."},
	{regexp.MustCompile(`\bgsap\.utils\.random\s*\(`), "gsap.utils.random()", "Use a seeded PRNG or fixed values: render workers initialize independently."},
	{regexp.MustCompile(`\bsetTimeout\s*\(`), "setTimeout()", "Time the change on the GSAP timeline instead of a timer."},
	{regexp.MustCompile(`\bsetInterval\s*\(`), "setInterval()", "Time the change on the GSAP timeline instead of a timer."},
}

// selfClosingMedia matches <video ... /> and <audio ... />.
var selfClosingMedia = regexp.MustCompile(`<(video|audio)\b[^>]*?/>`)

// urlScheme matches a URL's scheme.
var urlScheme = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):`)

// negativeZIndex matches a negative level in a stylesheet.
var negativeZIndex = regexp.MustCompile(`z-index\s*:\s*-\d+`)

// requestAnimationFrame is wall-clock animation the timeline does not drive.
var requestAnimationFrame = regexp.MustCompile(`requestAnimationFrame\s*\(`)

// infiniteRepeat is a GSAP repeat that never ends.
var infiniteRepeat = regexp.MustCompile(`repeat\s*:\s*-1(?:[^0-9]|$)`)

// imperativeMedia plays, pauses or seeks a media element from script.
var imperativeMedia = regexp.MustCompile(`\.(?:play|pause)\s*\(\s*\)|\.currentTime\s*=`)

// avTag reports whether a tag is timed media the runtime plays.
func avTag(tag string) bool { return tag == "video" || tag == "audio" }

// mediaTag reports whether a tag is media: played or still.
func mediaTag(tag string) bool { return avTag(tag) || tag == "img" }

// isHost reports whether the element mounts another composition.
func isHost(n *element) bool { return n.value("data-composition-src") != "" }

// hasSourceChild reports whether a media element names a source in a child.
func hasSourceChild(n *element) bool {
	for _, ch := range n.Children {
		if ch.Tag == "source" && ch.value("src") != "" {
			return true
		}
	}
	return false
}

// topLevel returns the composition's top-level timeline rows.
func topLevel(comp *Composition) []*element {
	if comp.root == nil {
		return nil
	}
	var rows []*element
	for _, ch := range comp.root.Children {
		switch ch.Tag {
		case "style", "script", "template", "noscript":
			continue
		}
		rows = append(rows, ch)
	}
	return rows
}

// idOf is the element's id as it shows in a message.
func idOf(n *element) string {
	if id := n.value("id"); id != "" {
		return ` id="` + id + `"`
	}
	return ""
}
