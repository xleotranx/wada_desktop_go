package main

import "github.com/egoist/mygo/ui"

// palette holds every color the interface uses, for the light and the dark
// the desktop asks for. Widgets that need nothing special keep the theme's
// own colors.
type palette struct {
	bg, sidebarBg, hover, selected, hairline ui.Color
	text, muted, faint                       ui.Color
	orange                                   ui.Color
	composerBg, composerBorder               ui.Color
	bubble                                   ui.Color
	detailBg                                 ui.Color
	thumbLine                                ui.Color
	voiceBg                                  ui.Color
	green, greenText                         ui.Color
}

func paletteFor(c *ui.Context) palette {
	if c.Theme().Dark {
		return palette{
			bg:             ui.Hex("#212121"),
			sidebarBg:      ui.Hex("#171717"),
			hover:          ui.RGBA(255, 255, 255, 0.06),
			selected:       ui.RGBA(255, 255, 255, 0.09),
			hairline:       ui.RGBA(255, 255, 255, 0.09),
			text:           ui.Hex("#ececec"),
			muted:          ui.Hex("#8c8c8c"),
			faint:          ui.Hex("#5c5c5c"),
			orange:         ui.Hex("#ff7a45"),
			composerBg:     ui.Hex("#262626"),
			composerBorder: ui.RGBA(255, 255, 255, 0.08),
			bubble:         ui.Hex("#333333"),
			detailBg:       ui.Hex("#262626"),
			thumbLine:      ui.Hex("#d0d0d0"),
			voiceBg:        ui.Hex("#f2f2f2"),
			green:          ui.Hex("#3ecf5e"),
			greenText:      ui.Hex("#093a18"),
		}
	}
	return palette{
		bg:             ui.Hex("#ffffff"),
		sidebarBg:      ui.Hex("#f9f9f9"),
		hover:          ui.RGBA(0, 0, 0, 0.048),
		selected:       ui.RGBA(0, 0, 0, 0.082),
		hairline:       ui.RGBA(0, 0, 0, 0.09),
		text:           ui.Hex("#0d0d0d"),
		muted:          ui.Hex("#8e8e8e"),
		faint:          ui.Hex("#c8c8c8"),
		orange:         ui.Hex("#e0601e"),
		composerBg:     ui.Hex("#f7f7f7"),
		composerBorder: ui.RGBA(0, 0, 0, 0.06),
		bubble:         ui.Hex("#efefef"),
		detailBg:       ui.Hex("#fafafa"),
		thumbLine:      ui.Hex("#3a3a3a"),
		voiceBg:        ui.Hex("#171717"),
		green:          ui.Hex("#4ade5b"),
		greenText:      ui.Hex("#0b3b1d"),
	}
}
