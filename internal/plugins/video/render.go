package video

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// tailLines is how much of the CLI's output the editor keeps.
const tailLines = 80

// commandOutput keeps the last lines a command wrote, so a failure says why,
// and reads the progress lines the HyperFrames CLI prints.
type commandOutput struct {
	mu      sync.Mutex
	lines   []string
	partial string
	onLine  func(string) // called on the worker, once per complete line
}

// Write splits what a command wrote into lines and keeps their tail.
func (w *commandOutput) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial += string(b)
	for {
		i := strings.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(w.partial[:i], "\r")
		w.partial = w.partial[i+1:]
		w.add(line)
	}
	return len(b), nil
}

// add keeps one line.
func (w *commandOutput) add(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	w.lines = append(w.lines, line)
	if len(w.lines) > tailLines {
		w.lines = w.lines[len(w.lines)-tailLines:]
	}
	if w.onLine != nil {
		w.onLine(line)
	}
}

// tail returns a copy of the lines kept so far, with what a command wrote
// without a closing newline.
func (w *commandOutput) tail() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.lines...)
	if strings.TrimSpace(w.partial) != "" {
		out = append(out, strings.TrimRight(w.partial, "\r"))
	}
	return out
}

// progressLine is what the CLI prints while it renders:
// "@hf-progress {\"code\":\"encode\",\"done\":52,\"total\":240,\"pct\":78}".
type progressLine struct {
	Code string `json:"code"`
	Pct  int    `json:"pct"`
}

// progressOf reads a CLI progress line's percent, or -1 when the line is
// not one.
func progressOf(line string) int {
	const prefix = "@hf-progress "
	if !strings.HasPrefix(line, prefix) {
		return -1
	}
	var p progressLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, prefix))), &p); err != nil {
		return -1
	}
	if p.Pct < 0 || p.Pct > 100 {
		return -1
	}
	return p.Pct
}

// render runs the HyperFrames CLI over the project and writes an MP4 beside
// it. The CLI owns the render engine: the editor keeps the project, the
// timeline and the preview.
func (p *Plugin) render() {
	if p.project == nil || p.dir == "" || p.rendering.Running() || p.loading.Running() || p.editing.Running() || p.adding.Running() {
		return
	}
	if p.check.Errors > 0 {
		p.host.Toast(ui.Toast{Title: "Fix the errors first", Description: p.check.message(), Type: components.ToastError})
		return
	}
	npx := findNpx()
	if npx == "" {
		p.host.Toast(ui.Toast{Title: "Node.js is not installed", Description: "Rendering runs the HyperFrames CLI, which needs Node.js and npx.",
			Type: components.ToastError})
		return
	}
	dir := p.dir
	opts := p.renderOpts
	out := filepath.Join(dir, "renders", renderName(p.project.Title)+opts.extension())
	p.renderLog, p.renderErr, p.renderOut, p.renderPct = nil, "", "", 0
	dest := out
	vars := p.variablesJSONFor()
	values := maps.Clone(p.values)
	navigation := p.navigation
	kernel.Go(p.navigation, &p.rendering, func(ctx context.Context) ([]string, error) {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		stage, cleanup, err := renderDir(dir, values)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		cmd := exec.CommandContext(ctx, npx, renderArgs(dest, vars, opts)...)
		cmd.Dir = stage
		cmd.Env = append(os.Environ(), "PATH="+renderPath(npx))
		tail := &commandOutput{onLine: func(line string) {
			if pct := progressOf(line); pct >= 0 {
				navigation.Post(func() {
					if ctx.Err() == nil {
						p.renderPct = pct
					}
				})
			}
		}}
		cmd.Stdout, cmd.Stderr = tail, tail
		err = cmd.Run()
		if err != nil {
			if ctx.Err() != nil {
				return tail.tail(), ctx.Err()
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return tail.tail(), errors.New("the HyperFrames CLI exited with " + exit.String())
			}
			return tail.tail(), err
		}
		if _, err := os.Stat(out); err != nil {
			return tail.tail(), errors.New("the CLI finished but wrote no file at " + out)
		}
		return tail.tail(), nil
	}, func(lines []string, err error) {
		p.renderPct = 0
		p.renderLog = lines
		if err != nil {
			p.renderErr = err.Error()
			p.host.Toast(ui.Toast{Title: "The video did not render", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.renderOut = out
		p.host.Toast(ui.Toast{Title: "Rendered", Description: rel(p.dir, out), Type: components.ToastSuccess})
		p.host.OpenURL((&url.URL{Scheme: "file", Path: out}).String())
	})
}

// lastLines are the last n lines of a command's output that say something,
// without the CLI's progress lines.
func lastLines(lines []string, n int) []string {
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "@hf-progress ") {
			continue
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// renderOptions is how the user last asked to render: the format and the
// quality, kept in the settings.
type renderOptions struct {
	Format  string `json:"format"`
	Quality string `json:"quality"`
}

// renderFormats are the formats the editor offers, with their extension.
var renderFormats = []struct{ Name, Ext string }{
	{"mp4", ".mp4"}, {"webm", ".webm"}, {"mov", ".mov"}, {"gif", ".gif"},
}

// renderQualities are the CLI's quality levels, best last.
var renderQualities = []string{"draft", "standard", "high"}

// renderFormatLabel names a format the way the menu does.
func renderFormatLabel(name string) string {
	switch name {
	case "mp4":
		return "MP4"
	case "webm":
		return "WebM"
	case "mov":
		return "MOV"
	case "gif":
		return "GIF"
	}
	return name
}

// renderQualityLabel names a quality the way the menu does.
func renderQualityLabel(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// extension is a format's file extension.
func (o renderOptions) extension() string {
	for _, f := range renderFormats {
		if f.Name == o.Format {
			return f.Ext
		}
	}
	return ".mp4"
}

// renderArgs is the CLI command line: the output, the format and quality when
// they are not the default, and the project's values when the user changed
// any.
func renderArgs(dest, vars string, opts renderOptions) []string {
	args := []string{"--yes", "hyperframes", "render", "--output", dest}
	if opts.Format != "" && opts.Format != "mp4" {
		args = append(args, "--format", opts.Format)
	}
	if opts.Quality != "" && opts.Quality != "standard" {
		args = append(args, "--quality", opts.Quality)
	}
	if vars != "" {
		args = append(args, "--variables", vars)
	}
	return args
}

// findNpx finds npx, since a window started from the Dock has a short PATH.
func findNpx() string {
	if path, err := exec.LookPath("npx"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		"/opt/homebrew/bin/npx",
		"/usr/local/bin/npx",
		filepath.Join(home, ".volta", "bin", "npx"),
		filepath.Join(home, ".local", "share", "mise", "shims", "npx"),
		filepath.Join(home, ".npm-global", "bin", "npx"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// renderPath is the PATH a render runs with: npx's folder first, then the
// usual places a window does not inherit.
func renderPath(npx string) string {
	dirs := []string{filepath.Dir(npx)}
	for _, dir := range strings.Split(os.Getenv("PATH"), ":") {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	return strings.Join(dirs, ":")
}

// renderName is a file name for the video: its title, safely.
func renderName(title string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "video"
	}
	return name
}
