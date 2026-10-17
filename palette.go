package main

import "github.com/egoist/mygo/ui"

// palette holds every color the interface uses, for the light and the dark
// the desktop asks for. Widgets that need nothing special keep the theme's
// own colors.
type palette struct {
	bg, sidebarBg, railBg, hover, selected, hairline ui.Color
	chromeWash                                       ui.Color
	text, muted, faint                               ui.Color
	orange                                           ui.Color
	composerBg, composerBorder, composerBorderFocus  ui.Color
	bubble                                           ui.Color
	detailBg                                         ui.Color
	danger, dangerText                               ui.Color
	link                                             ui.Color
	thumbLine                                        ui.Color
	voiceBg                                          ui.Color
	greenBadge                                       ui.Color
}

func paletteFor(c *ui.Context) palette {
	if c.Theme().Dark {
		return palette{
			bg:                  ui.Hex("#212121"),
			sidebarBg:           ui.Hex("#171717"),
			railBg:              ui.Hex("#1b1b1b"),
			chromeWash:          ui.RGBA(0, 0, 0, 0.4),
			hover:               ui.RGBA(255, 255, 255, 0.06),
			selected:            ui.RGBA(255, 255, 255, 0.09),
			hairline:            ui.RGBA(255, 255, 255, 0.09),
			text:                ui.Hex("#ececec"),
			muted:               ui.Hex("#8c8c8c"),
			faint:               ui.Hex("#5c5c5c"),
			orange:              ui.Hex("#ff7a45"),
			composerBg:          ui.Hex("#262626"),
			composerBorder:      ui.RGBA(255, 255, 255, 0.08),
			composerBorderFocus: ui.RGBA(255, 255, 255, 0.34),
			bubble:              ui.Hex("#333333"),
			detailBg:            ui.Hex("#262626"),
			thumbLine:           ui.Hex("#d0d0d0"),
			voiceBg:             ui.Hex("#f2f2f2"),
			link:                ui.Hex("#5aa2ff"),
			greenBadge:          ui.Hex("#4ed463"),
			danger:              ui.Hex("#e0442f"),
			dangerText:          ui.Hex("#ffffff"),
		}
	}
	return palette{
		bg:                  ui.Hex("#ffffff"),
		sidebarBg:           ui.Hex("#f9f9f9"),
		railBg:              ui.Hex("#eef0ef"),
		chromeWash:          ui.RGBA(255, 255, 255, 0.7),
		hover:               ui.RGBA(0, 0, 0, 0.048),
		selected:            ui.RGBA(0, 0, 0, 0.082),
		hairline:            ui.RGBA(0, 0, 0, 0.09),
		text:                ui.Hex("#0d0d0d"),
		muted:               ui.Hex("#8e8e8e"),
		faint:               ui.Hex("#c8c8c8"),
		orange:              ui.Hex("#e0601e"),
		composerBg:          ui.Hex("#f7f7f7"),
		composerBorder:      ui.RGBA(0, 0, 0, 0.06),
		composerBorderFocus: ui.RGBA(0, 0, 0, 0.34),
		bubble:              ui.Hex("#efefef"),
		detailBg:            ui.Hex("#fafafa"),
		thumbLine:           ui.Hex("#3a3a3a"),
		voiceBg:             ui.Hex("#171717"),
		link:                ui.Hex("#3b82f6"),
		greenBadge:          ui.Hex("#0e7a34"),
		danger:              ui.Hex("#e0442f"),
		dangerText:          ui.Hex("#ffffff"),
	}
}
