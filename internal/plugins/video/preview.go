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
	RootID   string         `json:"rootId"`
	FPS      float64        `json:"fps"`
	Duration float64        `json:"duration"`
	Scenes   []previewScene `json:"scenes"`
}

// previewScene is one scene of the payload.
type previewScene struct {
	HostID        string  `json:"hostId"`
	CompID        string  `json:"compId"`
	File          string  `json:"file"`
	HTML          string  `json:"html"`
	Start         float64 `json:"start"`
	Duration      float64 `json:"duration"`
	PlaybackStart float64 `json:"playbackStart"`
	Rate          float64 `json:"rate"`
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

// payload reads what the preview page needs from the project's files.
func payload(p *Project) previewPayload {
	out := previewPayload{RootID: p.Root.ID, FPS: p.FPS, Duration: p.Duration()}
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
func mountScript(p *Project) (string, error) {
	data, err := json.Marshal(payload(p))
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
