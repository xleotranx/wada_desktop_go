package main

import "github.com/egoist/mygo/ui"

// palette holds every color the interface paints, read from the frame theme
// so hand-drawn panes and Onevra's controls share one material. Widgets that
// need nothing special keep the theme's own colors.
type palette struct {
	bg, sidebarBg, railBg, hover, selected, hairline ui.Color
	chromeWash                                       ui.Color
	text, muted, faint                               ui.Color
	orange, accent, accentText                       ui.Color
	composerBg, composerBorder, composerBorderFocus  ui.Color
	bubble                                           ui.Color
	detailBg                                         ui.Color
	danger, dangerText                               ui.Color
	diffAdd, diffDel                                 ui.Color
	scrim, shadow, cardShadow                        ui.Color
	link                                             ui.Color
	thumbLine                                        ui.Color
	greenBadge                                       ui.Color
}

// paletteFor maps the app's semantic colors onto Onevra's warm palettes.
// The content sits on the theme background, the sidebar on its surface, and
// selection, hairlines and accents are the theme's own, so a switch between
// light and dark moves every pane at once.
func paletteFor(c *ui.Context) palette {
	t := c.Theme()
	black := ui.RGB(0, 0, 0)
	scrim := black.Alpha(0.38)
	shadow := black.Alpha(0.18)
	cardShadow := black.Alpha(0.06)
	if t.Dark {
		scrim = black.Alpha(0.5)
		shadow = black.Alpha(0.45)
		cardShadow = black.Alpha(0.22)
	}
	// Danger is light in the dark theme, so its label follows the background
	// there and the accent's text color in the light one.
	dangerText := t.AccentText
	if t.Dark {
		dangerText = t.Background
	}
	return palette{
		bg:                  t.Background,
		sidebarBg:           t.Surface,
		railBg:              t.Background.Mix(t.Text, 0.04),
		chromeWash:          t.Background.Alpha(0.72),
		hover:               t.SurfaceHover,
		selected:            t.Text.Alpha(0.10),
		hairline:            t.Border,
		text:                t.Text,
		muted:               t.TextMuted,
		faint:               t.TextMuted.Mix(t.Background, 0.45),
		orange:              t.Accent,
		accent:              t.Accent,
		accentText:          t.AccentText,
		composerBg:          t.Surface,
		composerBorder:      t.Border,
		composerBorderFocus: t.Accent,
		bubble:              t.SurfaceHover,
		detailBg:            t.Surface,
		danger:              t.Danger,
		dangerText:          dangerText,
		diffAdd:             t.Success,
		diffDel:             t.Danger,
		scrim:               scrim,
		shadow:              shadow,
		cardShadow:          cardShadow,
		link:                t.Accent,
		thumbLine:           t.Text,
		greenBadge:          t.Success,
	}
}
