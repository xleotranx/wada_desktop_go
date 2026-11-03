package video

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// storyFrames is how many frames a storyboard captures.
const storyFrames = 9

// shot is one captured frame, at the film's time.
type shot struct {
	Time  float64
	Image *ui.Bitmap
}

// takeStoryboard captures frames across what the timeline draws, evenly
// spaced, from the page the editor already shows: the preview's own frames.
func (p *Plugin) takeStoryboard() {
	if p.project == nil || p.web == nil || p.webErr != "" || p.shooting.Running() {
		return
	}
	span := p.span()
	if span <= 0 {
		return
	}
	web := p.web
	times := make([]float64, storyFrames)
	for i := range times {
		times[i] = p.rootAt(span * float64(i) / float64(storyFrames-1))
	}
	at := p.playhead
	kernel.Go(p.navigation, &p.shooting, func(ctx context.Context) ([]shot, error) {
		out := make([]shot, 0, len(times))
		for _, t := range times {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, err := web.Eval(ctx, seekScript(t)); err != nil {
				return nil, err
			}
			png, err := web.Capture()
			if err != nil {
				return nil, err
			}
			img, _, err := image.Decode(bytes.NewReader(png))
			if err != nil {
				return nil, err
			}
			out = append(out, shot{Time: t, Image: ui.NewBitmap(img)})
		}
		return out, nil
	}, func(shots []shot, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The storyboard was not captured", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.shots = shots
		p.story = true
		p.seek(at)
	})
}

// closeStoryboard puts the frames away and stops a capture in flight.
func (p *Plugin) closeStoryboard() {
	p.story = false
	p.shooting.Cancel()
}

// storyView is the storyboard over the window: one frame per moment, each a
// way to the frame it shows.
func (p *Plugin) storyView(c *ui.Context) {
	if !p.story {
		return
	}
	pal := native.PaletteFor(c)
	c.OnShortcut(0, ui.KeyEscape, func() { p.ctx.Post(p.closeStoryboard) })
	ui.Overlay(c, func() {
		ui.Column(c.Key("storyboard")).Absolute().Top(24).Left(24).Right(24).Bottom(24).
			Background(pal.Bg).Radius(12).Border(1, pal.Hairline).Modal().Role(ui.RoleDialog).Label("Storyboard").
			Children(func() {
				ui.Row(c).FillWidth().Height(46).Shrink(0).Padding(0, 16).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "Storyboard").FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Shrink(0)
					if p.project != nil {
						ui.Text(c, p.project.Title).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…").MinWidth(0)
					}
					ui.Box(c).Grow(1)
					b := ui.ButtonBase(c.Key("story-close")).Label("Close the storyboard").Height(26).Padding(0, 10).
						Radius(6).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
					if b.Hovered() {
						b.Background(pal.Hover)
					}
					b.OnClick(func() { p.ctx.Post(p.closeStoryboard) })
					b.Children(func() { ui.Text(c, "Close").FontSize(11.5).TextColor(pal.Text).SingleLine() })
				})
				ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
				ui.Scroll(c.Key("story-frames")).Fill().Children(func() {
					ui.Row(c).Wrap().Gap(12).Padding(14).Children(func() {
						for i, s := range p.shots {
							p.shotView(c, pal, i, s)
						}
					})
				})
			})
	})
}

// shotView is one frame of the storyboard.
func (p *Plugin) shotView(c *ui.Context, pal native.Palette, i int, s shot) {
	b := ui.ButtonBase(c.Key("shot-"+strconv.Itoa(i))).Label("Frame at "+timecode(s.Time)).Width(300).
		Radius(8).Background(pal.Raised).Border(1, pal.Hairline).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Border(1, pal.Link)
	}
	b.OnClick(func() {
		at := s.Time
		p.ctx.Post(func() {
			p.story = false
			p.seek(at)
		})
	})
	b.Children(func() {
		ui.Column(c).FillWidth().Gap(5).Padding(6).Children(func() {
			ui.Box(c).FillWidth().AspectRatio(16.0 / 9.0).Clip().Radius(4).Background(pal.Bg).Children(func() {
				ui.Image(c, s.Image).Fill()
			})
			ui.Text(c, timecode(s.Time)).FontSize(10.5).TextColor(pal.Muted).FontFeatures("tnum").SingleLine()
		})
	})
}
