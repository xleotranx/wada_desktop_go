// Package videokit declares reusable video collections and structured projects.
// Definitions are data: collection plugins contribute them; the video editor
// validates, snapshots and compiles them into HyperFrames compositions.
package videokit

import "github.com/xleotranx/wada-kit/kernel"

var Collections = kernel.NewPoint[Collection]("video.collections")

// Ref pins a definition. Published versions are immutable.
type Ref struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// Field describes a content control. Types use HyperFrames variable names.
type Field struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Default     any      `json:"default"`
	Description string   `json:"description,omitempty"`
	Step        float64  `json:"step,omitempty"`
	MaxLength   int      `json:"maxLength,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// Component uses Go text/template markup with escaped value/binding, bounded list/grid/series and numeric helpers.
// Motion is trusted collection JavaScript, scoped to box, tl and named cues.
// It must use absolute, seek-safe animation and never start its own clock.
type Component struct {
	Ref
	Group           string  `json:"group,omitempty"`
	Layouts         []Ref   `json:"layouts,omitempty"` // empty permits any layout
	DefaultDuration float64 `json:"defaultDuration,omitempty"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Fields          []Field `json:"fields"`
	Markup          string  `json:"markup"`
	CSS             string  `json:"css,omitempty"`
	Motion          string  `json:"motion,omitempty"`
}

type Theme struct {
	Ref
	Title  string            `json:"title"`
	Tokens map[string]string `json:"tokens"`
}

// Layout arranges one to MaxParts components on the supported canvas.
// CSS selectors are scoped under the scene by the compiler.
type Layout struct {
	Ref
	Title    string `json:"title"`
	CSS      string `json:"css"`
	MaxParts int    `json:"maxParts"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type Part struct {
	ID        string         `json:"id"`
	Component Ref            `json:"component"`
	Inputs    map[string]any `json:"inputs,omitempty"`
}

type Scene struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Layout   Ref     `json:"layout"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Track    int     `json:"track"`
	Parts    []Part  `json:"parts"`
}

type Template struct {
	Ref
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Theme       Ref     `json:"theme"`
	Scenes      []Scene `json:"scenes"`
}

// Collection is an installable set of definitions. A project's snapshot makes
// existing videos independent of later collection updates or removal.
type Collection struct {
	Ref
	CSS        string            `json:"css,omitempty"`        // shared visual primitives, snapshotted with the project
	Assets     map[string]string `json:"assets,omitempty"`     // project-relative UTF-8 files, including runtime and notices
	RuntimeURL string            `json:"runtimeUrl,omitempty"` // project-relative pinned animation runtime
	Title      string            `json:"title"`
	Themes     []Theme           `json:"themes"`
	Layouts    []Layout          `json:"layouts"`
	Components []Component       `json:"components"`
	Templates  []Template        `json:"templates"`
}

// Document is video.json, the source of truth for a structured project.
// The snapshot is stored with the project; generated HTML is disposable.
type Document struct {
	Schema    int            `json:"schema"`
	Title     string         `json:"title"`
	Theme     Ref            `json:"theme"`
	Scenes    []Scene        `json:"scenes"`
	Overrides map[string]any `json:"overrides,omitempty"`
	Snapshot  Collection     `json:"snapshot"`
}
