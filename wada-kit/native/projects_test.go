package native

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/theme"
)

// TestProjectLabelsAreReadable keeps every project's label legible on its
// tint wherever a label sits: on the strip and on the panel, in both themes.
func TestProjectLabelsAreReadable(t *testing.T) {
	for _, dark := range []bool{false, true} {
		var p Palette
		ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme.Palette(dark))
			p = PaletteFor(c)
		}, 100, 100).Frame()
		want := projectsLight
		if dark {
			want = projectsDark
		}
		if p.Projects[0].Mark != want[0] {
			t.Fatalf("dark=%v: the palette did not follow the theme", dark)
		}
		for i, pc := range p.Projects {
			for name, ground := range map[string]ui.Color{"strip": p.StripBg, "panel": p.PanelBg} {
				if r := contrast(pc.Text, pc.Tint.Over(ground)); r < 4.5 {
					t.Errorf("dark=%v project %d label on the %s: contrast %.2f, want 4.5", dark, i, name, r)
				}
			}
		}
	}
}

func contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func luminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// TestGlassLetsTheMaterialThrough clears the strip and veils the panel only
// while the window shows its material; cards stay solid either way.
func TestGlassLetsTheMaterialThrough(t *testing.T) {
	for _, shows := range []bool{false, true} {
		var p Palette
		tr := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme.Palette(false))
			p = PaletteFor(c)
		}, 100, 100)
		tr.SetVibrancy(shows)
		tr.Frame()
		if clear := p.StripBg.A == 0; clear != shows {
			t.Errorf("material shows=%v: strip clear=%v", shows, clear)
		}
		if veiled := p.PanelBg.A < 255; veiled != shows {
			t.Errorf("material shows=%v: panel veiled=%v", shows, veiled)
		}
		if p.Bg.A != 255 {
			t.Errorf("material shows=%v: Bg alpha %d, want solid", shows, p.Bg.A)
		}
	}
}

// TestClearWindowTintsTheDesktop veils the strip and the panel without a
// material once the shell says the window is clear.
func TestClearWindowTintsTheDesktop(t *testing.T) {
	SetClearWindow(true)
	defer SetClearWindow(false)
	var p Palette
	ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme.Palette(true))
		p = PaletteFor(c)
	}, 100, 100).Frame()
	if p.StripBg.A == 0 || p.StripBg.A == 255 || p.PanelBg.A == 255 {
		t.Errorf("strip alpha %d, panel alpha %d: want both veiled", p.StripBg.A, p.PanelBg.A)
	}
	if p.Bg.A != 255 {
		t.Errorf("Bg alpha %d, want solid", p.Bg.A)
	}
}
