package video

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
)

//go:embed preview.js
var previewJS string

// previewPayload is what the page needs to show a project: its root, and the
// scenes it mounts, whose files the page cannot read itself.
type previewPayload struct {
	RootID    string         `json:"rootId"`
	FPS       float64        `json:"fps"`
	Duration  float64        `json:"duration"`
	Variables map[string]any `json:"variables,omitempty"`
	Declared  map[string]any `json:"declared,omitempty"`
	Scenes    []previewScene `json:"scenes"`
}

// previewScene is one scene of the payload.
type previewScene struct {
	HostID        string         `json:"hostId"`
	CompID        string         `json:"compId"`
	File          string         `json:"file"`
	HTML          string         `json:"html"`
	Start         float64        `json:"start"`
	Duration      float64        `json:"duration"`
	PlaybackStart float64        `json:"playbackStart"`
	Rate          float64        `json:"rate"`
	Declared      map[string]any `json:"declared,omitempty"`
	Mount         map[string]any `json:"mount,omitempty"`
}

// previewInfo is what the page answers about the composition it shows.
type previewInfo struct {
	Mounted   bool     `json:"mounted"`
	Time      float64  `json:"time"`
	Duration  float64  `json:"duration"`
	FPS       float64  `json:"fps"`
	Errors    []string `json:"errors"`
	Timelines []string `json:"timelines"`
	Scenes    int      `json:"scenes"`
}

// defaultsOf is a declaration's default values by id.
func defaultsOf(variables []variable) map[string]any {
	if len(variables) == 0 {
		return nil
	}
	out := make(map[string]any, len(variables))
	for _, v := range variables {
		out[v.ID] = v.Default
	}
	return out
}

// payload reads what the preview page needs from the project's files.
func payload(p *Project, values map[string]any) previewPayload {
	out := previewPayload{RootID: p.Root.ID, FPS: p.FPS, Duration: p.Duration(),
		Variables: values, Declared: defaultsOf(p.Root.Variables)}
	if out.RootID == "" {
		out.RootID = "main"
	}
	for _, s := range p.Scenes {
		if s.Host == nil || s.Comp == nil {
			continue
		}
		sec := previewScene{
			HostID:        s.Host.ID,
			CompID:        s.Comp.ID,
			File:          s.Comp.File,
			Start:         s.Host.Start,
			Duration:      s.Host.Duration,
			PlaybackStart: s.PlaybackStart,
			Rate:          s.Rate,
			Declared:      defaultsOf(s.Comp.Variables),
			Mount:         s.Host.MountValues,
		}
		if out.RootID == s.Host.ID {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(s.Comp.File))); err == nil {
			sec.HTML = string(data)
		}
		out.Scenes = append(out.Scenes, sec)
	}
	return out
}

// mountScript is the script that installs the preview and shows the first
// frame: the page's own scripts have run by then.
func mountScript(p *Project, values map[string]any) (string, error) {
	data, err := json.Marshal(payload(p, values))
	if err != nil {
		return "", err
	}
	return "if (!window.__video) {" + previewJS + "}\n" +
		"window.__video.mount(" + string(data) + ");\n" +
		"return window.__video.info();", nil
}

// jsonString quotes a value as a JSON string, for a script.
func jsonString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

// variableScript sets one variable in the page.
func variableScript(id string, value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "return null;"
	}
	return "return window.__video ? window.__video.setVariable(" + jsonString(id) + ", " + string(data) + ") : null;"
}

// seekScript shows the frame at t.
func seekScript(t float64) string {
	return fmt.Sprintf("return window.__video ? window.__video.seek(%g) : null;", t)
}

// waitReady polls the page until it shows the composition at path and its
// own scripts have run, or gives up: Eval blocks, so this runs on a worker,
// and a page still loading the last file answers no.
func waitReady(ctx context.Context, web shell.WebView, path string, deadline time.Duration) error {
	until := time.Now().Add(deadline)
	check := fmt.Sprintf("return document.readyState === 'complete' && location.pathname.endsWith(%s) && !!document.querySelector('[data-composition-id]');",
		jsonString(filepath.Base(path)))
	var last error
	for {
		ready, err := web.Eval(ctx, check)
		if err == nil {
			last = nil
			if b, ok := ready.(bool); ok && b {
				return nil
			}
		} else {
			last = err
		}
		if time.Now().After(until) {
			if last != nil {
				return last
			}
			return fmt.Errorf("the composition did not load")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
}
