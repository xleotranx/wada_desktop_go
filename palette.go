package main

import "github.com/egoist/mygo/ui"

// palette holds every color the interface paints, read from the frame theme
// so hand-drawn panes and Onevra's controls share one material. Widgets that
// need nothing special keep the theme's own colors.
type palette struct {
	bg, sidebarBg, railBg, hover, selected, hairline ui.Color
	chromeWash                                       ui.Color
	text, muted, faint                               ui.Color
	orange                                           ui.Color
	detailBg                                         ui.Color
	danger, dangerText                               ui.Color
	diffAdd, diffDel                                 ui.Color
	link                                             ui.Color
	greenBadge                                       ui.Color
}

// paletteFor maps the app's semantic colors onto Onevra's warm palettes.
// The content sits on the theme background, the sidebar on its surface, and
// selection, hairlines and accents are the theme's own, so a switch between
// light and dark moves every pane at once.
func paletteFor(c *ui.Context) palette {
	t := c.Theme()
	// Danger is light in the dark theme, so its label follows the background
	// there and the accent's text color in the light one.
	dangerText := t.AccentText
	if t.Dark {
		dangerText = t.Background
	}
	return palette{
		bg:         t.Background,
		sidebarBg:  t.Surface,
		railBg:     t.Background.Mix(t.Text, 0.04),
		chromeWash: t.Background.Alpha(0.72),
		hover:      t.SurfaceHover,
		selected:   t.Text.Alpha(0.10),
		hairline:   t.Border,
		text:       t.Text,
		muted:      t.TextMuted,
		faint:      t.TextMuted.Mix(t.Background, 0.45),
		orange:     t.Accent,
		detailBg:   t.Surface,
		danger:     t.Danger,
		dangerText: dangerText,
		diffAdd:    t.Success,
		diffDel:    t.Danger,
		link:       t.Accent,
		greenBadge: t.Success,
	}
}
