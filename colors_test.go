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

// TestPluginsSpawnTheirGoroutines keeps the app's plugins to goroutines the
// kernel runs, so a panic fails the plugin instead of ending the app.
func TestPluginsSpawnTheirGoroutines(t *testing.T) {
	kittest.PluginsSpawnTheirGoroutines(t, "internal/plugins")
}
