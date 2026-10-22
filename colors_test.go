package main

import (
	"testing"

	"github.com/xleotranx/wada-kit/kittest"
)

// TestColorsStayInThePalette keeps every color of the app's panes in
// native.PaletteFor.
func TestColorsStayInThePalette(t *testing.T) {
	kittest.ColorsStayInThePalette(t, ".")
}
