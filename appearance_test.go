package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAppearanceTabChoosesAndNormalizes drives the Appearance tab: a click
// saves the named choice, and an unknown stored value falls back to the
// desktop's appearance.
func TestAppearanceTabChoosesAndNormalizes(t *testing.T) {
	a := newTestApp(t)
	a.openSettings(tabAppearance)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Appearance") || !tt.HasText("System") || !tt.HasText("Dark") {
		t.Fatalf("the Appearance tab is missing its choices: %v", tt.Texts())
	}
	if err := tt.Click("Dark. Keep the warm dark palette"); err != nil {
		t.Fatal(err)
	}
	if a.preferences.appearance != appearanceDark {
		t.Fatalf("clicking Dark saved %q", a.preferences.appearance)
	}
	a.setAppearance("not-a-theme")
	if a.preferences.appearance != appearanceSystem {
		t.Fatalf("an unknown appearance stayed %q", a.preferences.appearance)
	}
}

// TestAppearanceRestoresFromState keeps the saved choice in the app
// preference when metadata is restored.
func TestAppearanceRestoresFromState(t *testing.T) {
	a := newTestApp(t)
	a.restoreState(appMetadata{appearance: appearanceLight})
	if a.preferences.appearance != appearanceLight {
		t.Fatalf("restored appearance %q", a.preferences.appearance)
	}
	a.restoreState(appMetadata{appearance: "bogus"})
	if a.preferences.appearance != appearanceSystem {
		t.Fatalf("unknown restored appearance %q", a.preferences.appearance)
	}
}
