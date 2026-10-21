package desktop

import (
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
)

type palette = native.Palette

func paletteFor(c *ui.Context) palette { return native.PaletteFor(c) }
