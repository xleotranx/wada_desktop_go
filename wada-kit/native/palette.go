package native

import "github.com/egoist/mygo/ui"

// palette holds every color the interface paints, read from the frame theme
// so hand-drawn panes and Onevra's controls share one material. Widgets that
// need nothing special keep the theme's own colors.
type Palette struct {
	Bg, Hover, Selected, Hairline ui.Color
	Text, Muted, Faint            ui.Color
	Orange                        ui.Color
	DetailBg                      ui.Color
	Danger, DangerText, Warning   ui.Color
	DiffAdd, DiffDel              ui.Color
	Link                          ui.Color
	GreenBadge                    ui.Color

	// StripBg is behind the window's title strip; PanelBg is the rounded
	// panel the page sits in, and DrawerBg a column beside the page in it.
	StripBg, PanelBg, DrawerBg ui.Color
	// Raised lifts a bubble, chip or control off the panel.
	Raised ui.Color
	// ActiveTab fills the tab on show, lifted off the strip.
	ActiveTab ui.Color
	// Waiting marks what waits for the user, such as an approval.
	Waiting ui.Color
	// DiffAddBg and DiffDelBg are the backgrounds of added and removed lines.
	DiffAddBg, DiffDelBg ui.Color
	// Projects are the colors a project can take, in the order new projects
	// take them.
	Projects [5]ProjectColor
}

// ProjectColor is one project's color: Mark for dots and lines, Tint behind
// its label, and Text for the label on that tint.
type ProjectColor struct {
	Mark, Tint, Text ui.Color
}

// Project colors: blue, violet, teal, rose and gray, each in a shade for the
// light theme and one for the dark.
var (
	projectsLight = [5]ui.Color{ui.Hex("#2f5fd0"), ui.Hex("#7445c8"), ui.Hex("#0b7f70"), ui.Hex("#c2416b"), ui.Hex("#72726a")}
	projectsDark  = [5]ui.Color{ui.Hex("#8aabf5"), ui.Hex("#b99af2"), ui.Hex("#6fcfbf"), ui.Hex("#ee91ae"), ui.Hex("#bdb6aa")}
)

// PaletteFor maps the app's semantic colors onto Onevra's warm palettes.
// The tabs sit on the strip, the page in a panel on the surface, and
// selection, hairlines and accents are the theme's own, so a switch between
// light and dark moves every pane at once. While the window shows its
// material, the strip is clear and the panel lets it through.
func PaletteFor(c *ui.Context) Palette {
	p := PaletteOf(c.Theme())
	switch {
	case clearWindow:
		p.clear(c.Theme())
	case c.Vibrancy():
		p.glass(c.Theme())
	}
	return p
}

// clearWindow is set by the shell while the window shows the desktop with no
// material; only the shell knows, as the window reports no material then.
var clearWindow bool

// SetClearWindow tells the palette that the window is transparent with no
// material behind it. It is called on the UI thread.
func SetClearWindow(on bool) { clearWindow = on }

// clear tints the desktop behind the window just enough to read on: the
// strip and the panel are one veil of the background, darker in the dark
// theme and lighter in the light one, and hover and the active tab are
// tints over it. Cards, bubbles and the composer keep Bg and stay solid.
func (p *Palette) clear(t *ui.Theme) {
	strip, panel := float32(0.55), float32(0.62)
	if t.Dark {
		strip, panel = 0.45, 0.55
	}
	p.StripBg = t.Background.Alpha(strip)
	p.PanelBg = t.Background.Alpha(panel)
	p.DrawerBg = t.Text.Alpha(0.03)
	p.ActiveTab = t.Text.Alpha(0.12)
	p.Hover = t.Text.Alpha(0.08)
}

// glass lets the window's blurred material through: the strip draws nothing,
// the panel and the columns in it are tinted veils over it, and hover and the
// active tab are tints too, so they read on any wallpaper. Cards, bubbles and
// the composer keep Bg and stay solid.
func (p *Palette) glass(t *ui.Theme) {
	veil, tab := float32(0.80), float32(0.90)
	if t.Dark {
		veil, tab = 0.74, 0.85
	}
	p.StripBg = ui.Transparent
	p.PanelBg = p.PanelBg.Alpha(veil)
	p.DrawerBg = t.Text.Alpha(0.03)
	p.ActiveTab = p.ActiveTab.Alpha(tab)
	p.Hover = t.Text.Alpha(0.07)
}

// PaletteOf is the palette of a theme, such as one drawn as a preview while
// another is in use.
func PaletteOf(t *ui.Theme) Palette {
	// Danger is light in the dark theme, so its label follows the background
	// there and the accent's Text color in the light one.
	DangerText := t.AccentText
	if t.Dark {
		DangerText = t.Background
	}
	// The strip sits a step darker than the panel in both themes: below the
	// background in the light one, at the background in the dark one, where
	// the surface is already the lighter step.
	strip := t.Background
	marks, labels, tint := projectsDark, ui.RGB(255, 255, 255), float32(0.18)
	if !t.Dark {
		strip = t.Background.Mix(t.SurfaceHover, 0.7)
		marks, labels, tint = projectsLight, ui.RGB(0, 0, 0), 0.11
	}
	// The panel sits between the background and the surface, as the page
	// always has.
	panel := t.Background.Mix(t.Surface, 0.75)
	// The strip is darker than the panel in the light theme, so the panel's
	// color lifts the active tab there; in the dark one the raised step does.
	activeTab := panel
	if t.Dark {
		activeTab = t.Surface.Mix(t.SurfaceHover, 0.75)
	}
	var projects [5]ProjectColor
	for i, m := range marks {
		projects[i] = ProjectColor{Mark: m, Tint: m.Alpha(tint), Text: m.Mix(labels, 0.12)}
	}
	return Palette{
		Bg:         t.Background,
		Hover:      t.SurfaceHover,
		Selected:   t.Text.Alpha(0.10),
		Hairline:   t.Border,
		Text:       t.Text,
		Muted:      t.TextMuted,
		Faint:      t.TextMuted.Mix(t.Background, 0.45),
		Orange:     t.Accent,
		DetailBg:   t.Surface,
		Danger:     t.Danger,
		DangerText: DangerText,
		Warning:    t.Warning,
		DiffAdd:    t.Success,
		DiffDel:    t.Danger,
		Link:       t.Accent,
		GreenBadge: t.Success,

		StripBg:   strip,
		PanelBg:   panel,
		DrawerBg:  panel.Mix(strip, 0.4),
		Raised:    t.Surface.Mix(t.SurfaceHover, 0.75),
		ActiveTab: activeTab,
		Waiting:   t.Warning,
		DiffAddBg: t.Success.Alpha(0.12),
		DiffDelBg: t.Danger.Alpha(0.12),
		Projects:  projects,
	}
}
