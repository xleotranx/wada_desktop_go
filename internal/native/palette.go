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
// light and dark moves every pane at once.
func PaletteFor(c *ui.Context) Palette { return PaletteOf(c.Theme()) }

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
	// always has; the active tab is the same color, so the two read as one.
	panel := t.Background.Mix(t.Surface, 0.75)
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
		Waiting:   t.Warning,
		DiffAddBg: t.Success.Alpha(0.12),
		DiffDelBg: t.Danger.Alpha(0.12),
		Projects:  projects,
	}
}
