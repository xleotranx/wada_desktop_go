package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAboutKeepsItsVersion keeps the settings nav's About foot naming the
// build: the version line is there, and one serve of the app always says
// some version, "dev" or better.
func TestAboutKeepsItsVersion(t *testing.T) {
	a := newTestApp(t)
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Wada Desktop") {
		t.Fatalf("the About foot is missing: %v", tt.Texts())
	}
	version := "Version " + appVersion()
	shown := false
	for _, text := range tt.Texts() {
		if strings.Contains(text, version) {
			shown = true
		}
	}
	if !shown {
		t.Fatalf("the About foot says no %q: %v", version, tt.Texts())
	}
	if v := appVersion(); v != "dev" && !strings.Contains(v, ".") {
		t.Fatalf("the app's version %q is neither dev nor a version", v)
	}
}
