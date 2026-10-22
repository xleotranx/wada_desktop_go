package native_test

import (
	"testing"

	"github.com/xleotranx/wada-kit/kittest"
)

// TestColorsStayInThePalette keeps the kit's own panes on the palette.
func TestColorsStayInThePalette(t *testing.T) {
	kittest.ColorsStayInThePalette(t, "..")
}
