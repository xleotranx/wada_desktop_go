package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// cliFinding is one finding of the HyperFrames CLI's check.
type cliFinding struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity"`
	Message    string   `json:"message"`
	Selector   string   `json:"selector"`
	ElementID  string   `json:"elementId"`
	SourceFile string   `json:"sourceFile"`
	Line       int      `json:"line"`
	FixHint    string   `json:"fixHint"`
	Time       *float64 `json:"time"`
}

// cliSection is one part of the CLI's report: the lint, the runtime, the
// layout, the motion, the contrast, the craft and the audio checks.
type cliSection struct {
	OK           bool         `json:"ok"`
	ErrorCount   int          `json:"errorCount"`
	WarningCount int          `json:"warningCount"`
	Findings     []cliFinding `json:"findings"`
}

// cliReport is what `hyperframes check --json` prints.
type cliReport struct {
	OK             bool       `json:"ok"`
	BrowserSkipped bool       `json:"browserSkipped"`
	Lint           cliSection `json:"lint"`
	Runtime        cliSection `json:"runtime"`
	Layout         cliSection `json:"layout"`
	Motion         cliSection `json:"motion"`
	Contrast       cliSection `json:"contrast"`
	Craft          cliSection `json:"craft"`
	Audio          cliSection `json:"audio"`
}

// fullCheck runs the official CLI's check over the project and shows what it
// found: the editor's own check is the fast one, this is the reference.
func (p *Plugin) fullCheck() {
	if p.project == nil || p.dir == "" || p.checking.Running() || p.rendering.Running() {
		return
	}
	npx := findNpx()
	if npx == "" {
		p.host.Toast(ui.Toast{Title: "Node.js is not installed", Description: "The full check runs the HyperFrames CLI, which needs Node.js and npx.",
			Type: components.ToastError})
		return
	}
	dir := p.dir
	project := p.project
	kernel.Go(p.navigation, &p.checking, func(ctx context.Context) (check, error) {
		cmd := exec.CommandContext(ctx, npx, "--yes", "hyperframes", "check", "--json")
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "PATH="+renderPath(npx))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		// A failed check still prints its report: read it before the error.
		if report, perr := parseCLIReport(stdout.Bytes(), dir, project); perr == nil {
			return report, nil
		}
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return check{}, errors.New("the HyperFrames CLI exited with " + exit.String())
			}
			return check{}, err
		}
		return check{}, errors.New("the HyperFrames CLI printed no report")
	}, func(report check, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The full check did not run", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.check = report
		p.cliChecked = true
	})
}

// parseCLIReport reads the CLI's JSON into the editor's findings, keeping the
// file paths relative to the project.
func parseCLIReport(data []byte, dir string, project *Project) (check, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return check{}, errors.New("no JSON report")
	}
	var report cliReport
	if err := json.Unmarshal(trimmed, &report); err != nil {
		return check{}, err
	}
	var out check
	for _, section := range []cliSection{report.Lint, report.Runtime, report.Layout,
		report.Motion, report.Contrast, report.Craft, report.Audio} {
		for _, f := range section.Findings {
			out.add(cliConversion(f, dir, project))
		}
	}
	return out, nil
}

// cliConversion is one CLI finding as the editor shows it.
func cliConversion(f cliFinding, dir string, project *Project) finding {
	file := f.SourceFile
	if rel, err := filepath.Rel(dir, f.SourceFile); err == nil && !strings.HasPrefix(rel, "..") {
		file = filepath.ToSlash(rel)
	}
	clip := f.ElementID
	if clip == "" && strings.HasPrefix(f.Selector, "#") {
		clip = strings.TrimPrefix(f.Selector, "#")
	}
	if clip != "" && project != nil {
		// A selector may carry more than the id; keep the clip only when the
		// editor knows it.
		if _, ok := findClip(project, clip); !ok {
			clip = ""
		}
	}
	severity := sevWarning
	switch strings.ToLower(f.Severity) {
	case "error":
		severity = sevError
	case "warning":
		severity = sevWarning
	case "info":
		severity = sevInfo
	}
	return finding{Code: f.Code, Severity: severity, Message: f.Message, Fix: f.FixHint,
		File: file, Line: f.Line, Clip: clip, Source: "cli"}
}

// findClip finds a clip anywhere in the project by id.
func findClip(p *Project, id string) (*Clip, bool) {
	if p == nil {
		return nil, false
	}
	if c := clipIn(p.Root, id); c != nil {
		return c, true
	}
	for _, s := range p.Scenes {
		if c := clipIn(s.Comp, id); c != nil {
			return c, true
		}
	}
	return nil, false
}
