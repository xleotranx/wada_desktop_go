package native

import "github.com/egoist/mygo/ui"

// palette holds every color the interface paints, read from the frame theme
// so hand-drawn panes and Onevra's controls share one material. Widgets that
// need nothing special keep the theme's own colors.
type Palette struct {
	Bg, CanvasBg, SidebarBg, RailBg, Hover, Selected, Hairline ui.Color
	ChromeWash                                                 ui.Color
	Text, Muted, Faint                                         ui.Color
	Orange                                                     ui.Color
	DetailBg                                                   ui.Color
	Danger, DangerText                                         ui.Color
	DiffAdd, DiffDel                                           ui.Color
	Link                                                       ui.Color
	GreenBadge                                                 ui.Color
}

// paletteFor maps the app's semantic colors onto Onevra's warm palettes.
// The content sits on the theme background, the sidebar on its surface, and
// selection, hairlines and accents are the theme's own, so a switch between
// light and dark moves every pane at once.
func PaletteFor(c *ui.Context) Palette {
	t := c.Theme()
	// Danger is light in the dark theme, so its label follows the background
	// there and the accent's Text color in the light one.
	DangerText := t.AccentText
	if t.Dark {
		DangerText = t.Background
	}
	return Palette{
		Bg:         t.Background,
		CanvasBg:   t.Background.Mix(t.Surface, 0.75),
		SidebarBg:  t.Surface,
		RailBg:     t.Background.Mix(t.Text, 0.04),
		ChromeWash: t.Background.Alpha(0.72),
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
		DiffAdd:    t.Success,
		DiffDel:    t.Danger,
		Link:       t.Accent,
		GreenBadge: t.Success,
	}
}
