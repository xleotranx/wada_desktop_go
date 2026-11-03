package video

import (
	"context"
	"errors"
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

// tailWriter keeps the last lines a command wrote, so a failure says why.
type tailWriter struct {
	mu    sync.Mutex
	lines []string
}

// Write adds what a command wrote, keeping the tail.
func (w *tailWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		w.lines = append(w.lines, line)
	}
	if len(w.lines) > tailLines {
		w.lines = w.lines[len(w.lines)-tailLines:]
	}
	return len(b), nil
}

// tail returns a copy of the lines kept so far.
func (w *tailWriter) tail() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

// render runs the HyperFrames CLI over the project and writes an MP4 beside
// it. The CLI owns the render engine: the editor keeps the project, the
// timeline and the preview.
func (p *Plugin) render() {
	if p.project == nil || p.dir == "" || p.rendering.Running() {
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
	out := filepath.Join(dir, "renders", renderName(p.project.Title)+".mp4")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		p.host.Toast(ui.Toast{Title: "The render folder was not made", Description: err.Error(), Type: components.ToastError})
		return
	}
	p.renderLog, p.renderErr, p.renderOut = nil, "", ""
	dest := out
	kernel.Go(p.navigation, &p.rendering, func(ctx context.Context) ([]string, error) {
		cmd := exec.CommandContext(ctx, npx, "--yes", "hyperframes", "render", "--output", dest)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+renderPath(npx))
		tail := &tailWriter{}
		cmd.Stdout, cmd.Stderr = tail, tail
		err := cmd.Run()
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
		return tail.tail(), nil
	}, func(lines []string, err error) {
		p.renderLog = lines
		if err != nil {
			p.renderErr = err.Error()
			p.host.Toast(ui.Toast{Title: "The video did not render", Description: err.Error(), Type: components.ToastError})
			return
		}
		if _, statErr := os.Stat(out); statErr != nil {
			p.renderErr = "The CLI finished but wrote no file at " + out
			return
		}
		p.renderOut = out
		p.host.Toast(ui.Toast{Title: "Rendered", Description: rel(p.dir, out), Type: components.ToastSuccess})
		p.host.OpenURL("file://" + out)
	})
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
