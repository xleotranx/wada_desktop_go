package design

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// present shows the artboard over the whole window at 100%.
func (p *Plugin) present(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	p.closeCard()
	if p.presenting != "" && p.presenting != path {
		p.stopPresenting()
	}
	p.presenting = path
	p.selectBoard(path)
	b.rastering.Cancel()
	b.raster = nil
	b.web.SetZoom(1)
}

func (p *Plugin) stopPresenting() {
	if p.presenting == "" {
		return
	}
	p.presenting = ""
	p.setZoom()
}

// step presents the artboard before (-1) or after (1) the one on show.
func (p *Plugin) step(by int) {
	boards := p.shown()
	for i, a := range boards {
		if a.Path == p.presenting {
			if j := i + by; j >= 0 && j < len(boards) {
				p.stopPresenting()
				p.present(boards[j].Path)
			}
			return
		}
	}
}

// presentView covers the window with the artboard presented.
func (p *Plugin) presentView(c *ui.Context) {
	b := p.boards[p.presenting]
	if p.presenting == "" || b == nil || !p.open {
		return
	}
	pal := native.PaletteFor(c)
	boards := p.shown()
	i := 0
	for j, a := range boards {
		if a.Path == p.presenting {
			i = j
		}
	}
	ui.Overlay(c, func() {
		ui.Column(c.Key("design-present")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(pal.Bg).Modal().Role(ui.RoleDialog).Label("Presenting").Children(func() {
			c.OnShortcut(0, ui.KeyEscape, p.stopPresenting)
			ui.Row(c).FillWidth().Height(44).Shrink(0).Padding(0, 12, 0, 84).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, boards[i].name()).FontSize(13).FontWeight(600).TextColor(pal.Text).SingleLine()
				ui.Text(c, strconv.Itoa(i+1)+" of "+strconv.Itoa(len(boards))).FontSize(12).TextColor(pal.Muted).FontFeatures("tnum")
				ui.Box(c).Grow(1)
				p.iconButton(c, pal, "previous", "Previous artboard", lucide.ChevronLeft, false, i == 0, func() { p.step(-1) })
				p.iconButton(c, pal, "next", "Next artboard", lucide.ChevronRight, false, i == len(boards)-1, func() { p.step(1) })
				p.iconButton(c, pal, "stop", "Stop presenting  Esc", lucide.X, false, false, p.stopPresenting)
			})
			b.web.Show(c.Key(p.presenting)).FillWidth().Grow(1)
		})
	})
}

// artboardTemplate is a new artboard, for the agent to fill.
const artboardTemplate = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="artboard" content="1280x800">
<title>%s</title>
<style>
  :root { --bg: #ffffff; --ink: #1d1d1f; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--ink);
    font: 15px/1.5 -apple-system, system-ui, sans-serif; }
</style>
</head>
<body>
<p>Describe this artboard in the chat.</p>
</body>
</html>
`

func fmtTemplate(name string) string { return fmt.Sprintf(artboardTemplate, name) }

// newArtboard writes an empty artboard on the page on show, or on a new
// design page, and asks the user, in the composer, what it should be.
func (p *Plugin) newArtboard() {
	if p.creating.Running() {
		return
	}
	dir := p.page
	if dir == "" {
		dir = "."
	}
	folder, project := p.folder, p.project
	kernel.Go(p.navigation, &p.creating, func(ctx context.Context) (string, error) {
		base := filepath.Join(folder, filepath.FromSlash(dir))
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		for n := 1; n < 1000; n++ {
			name := "artboard-" + strconv.Itoa(n)
			path := filepath.Join(base, name+".html")
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				return "", err
			}
			_, err = f.WriteString(fmtTemplate(name))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return path, err
		}
		return "", errors.New("the page has too many artboards")
	}, func(path string, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The artboard was not made", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.selectBoard(path)
		p.chosen = true
		p.page = filepath.ToSlash(filepath.Dir(rel(folder, path)))
		p.chat.SetDraft(appendDraft(p.chat.DraftText(), "Design "+rel(project, path)+": "))
		p.scan()
	})
}
