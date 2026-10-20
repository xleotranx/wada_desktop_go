package main

import "github.com/egoist/mygo"

// The appearance choices the app keeps. "system" follows the desktop.
const (
	appearanceSystem = "system"
	appearanceLight  = "light"
	appearanceDark   = "dark"
)

// normalizeAppearance maps a stored value to one this build knows, so a
// hand-edited or older value cannot leave the app in an unnamed state.
func normalizeAppearance(value string) string {
	switch value {
	case appearanceLight, appearanceDark:
		return value
	default:
		return appearanceSystem
	}
}

// appearanceSource turns the preference into MyGo's theme source.
func appearanceSource(value string) mygo.ThemeSource {
	switch normalizeAppearance(value) {
	case appearanceLight:
		return mygo.ThemeLight
	case appearanceDark:
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}

// applyAppearance installs the saved appearance at startup. Without a
// window there is no native chrome to tell yet, and tests keep the global
// theme as the frame's own; the preference still travels with the state.
func (a *app) applyAppearance() {
	if a.runtime.win == nil {
		return
	}
	mygo.Theme.SetSource(appearanceSource(a.preferences.appearance))
}

// setAppearance changes the app's appearance, applies it to the native
// chrome and every page, and saves the choice.
func (a *app) setAppearance(value string) {
	a.preferences.appearance = normalizeAppearance(value)
	a.applyAppearance()
	a.saveState()
}
