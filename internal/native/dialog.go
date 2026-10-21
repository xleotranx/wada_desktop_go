package native

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

func Dialog(c *ui.Context, p Palette, open *bool, width float32, label string, fn func()) {
	components.Dialog(c, open, components.DialogOptions{Label: label}, func(panel ui.Element) {
		panel.Width(width).MaxWidth(width)
		ui.Column(c).Gap(12).Children(fn)
	})
}

// textField is the field the dialogs and the model picker share: Onevra's
// bordered single-line field, with the theme's focus ring.
