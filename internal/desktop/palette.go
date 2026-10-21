package desktop

import (
	"github.com/egoist/mygo/ui"
	"wada-desktop-go/internal/native"
)

type palette = native.Palette

func paletteFor(c *ui.Context) palette { return native.PaletteFor(c) }
