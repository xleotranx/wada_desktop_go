package markup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// captureFunc lets the user take a screenshot, off the UI thread, and
// returns it as PNG data, or nil when they cancel.
type captureFunc func(ctx context.Context) ([]byte, error)

// screencapture is macOS's interactive capture: the user drags a region or
// presses Space and clicks a window, and Escape cancels. It is nil where
// the platform has none.
func screencapture() captureFunc {
	if runtime.GOOS != "darwin" {
		return nil
	}
	tool, err := exec.LookPath("screencapture")
	if err != nil {
		return nil
	}
	return func(ctx context.Context) ([]byte, error) {
		dir, err := os.MkdirTemp("", "wada-capture-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		file := filepath.Join(dir, "screenshot.png")
		// -i is interactive, -o leaves out a window's shadow and -x the sound.
		output, ran := exec.CommandContext(ctx, tool, "-i", "-o", "-x", "-t", "png", file).CombinedOutput()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Escape leaves no file and no diagnostic, with status 0 or 1.
		// Preserve other failures, including failures that leave no file.
		var exit *exec.ExitError
		cancelled := ran == nil || errors.As(ran, &exit) && exit.ExitCode() == 1
		diagnostic := strings.TrimSpace(string(output))
		data, err := os.ReadFile(file)
		switch {
		case errors.Is(err, fs.ErrNotExist) && cancelled && diagnostic == "":
			return nil, nil
		case ran != nil:
			if diagnostic != "" {
				return nil, fmt.Errorf("screencapture: %s: %w", diagnostic, ran)
			}
			return nil, ran
		case err != nil:
			return nil, err
		}
		return data, nil
	}
}

type captured struct {
	Data    []byte
	Preview *ui.Bitmap
}

// hideDelay lets the window leave the screen before the capture begins.
const hideDelay = 250 * time.Millisecond

// captureScreen hides the window, lets the user take a screenshot, brings
// the window back in front, attaches the screenshot to the next message and
// opens it in the editor. ⌃⇧⌘2 runs it from any app.
func (p *Plugin) captureScreen() {
	if p.capture == nil || p.capturing.Running() {
		return
	}
	p.close()
	p.host.SetHidden(true)
	capture := p.capture
	kernel.Go(p.ctx, &p.capturing, func(ctx context.Context) (captured, error) {
		select {
		case <-time.After(hideDelay):
		case <-ctx.Done():
			return captured{}, ctx.Err()
		}
		data, err := capture(ctx)
		if err != nil || data == nil {
			return captured{}, err
		}
		img, err := decode(data)
		if err != nil {
			return captured{}, err
		}
		return captured{Data: data, Preview: ui.NewBitmap(img)}, nil
	}, func(shot captured, err error) {
		p.host.SetHidden(false)
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The screenshot was not taken", Description: err.Error(), Type: components.ToastError})
			return
		}
		if shot.Data == nil {
			return
		}
		p.shots++
		name := "Screenshot.png"
		if p.shots > 1 {
			name = fmt.Sprintf("Screenshot %d.png", p.shots)
		}
		id, err := p.images.Attach(agent.File{Name: name, MIME: "image/png", Data: shot.Data}, shot.Preview)
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The screenshot was not attached", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.begin(id)
	})
}

// captureButton sits in the composer beside the paperclip.
func (p *Plugin) captureButton(c *ui.Context) {
	pal := native.PaletteFor(c)
	label := "Capture a screenshot to mark up"
	b := ui.ButtonBase(c).Label(label).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer).Tooltip(label + " (⇧⌘2; ⌃⇧⌘2 from any app)").
		Disabled(p.capturing.Running())
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(p.captureScreen)
	b.Children(func() { ui.Icon(c, lucide.ScreenShare()).FontSize(15).TextColor(pal.Muted) })
}
