package shell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// accelerator is one parsed command shortcut such as "cmd+shift+k". "cmd" is
// the platform's command key (Ctrl outside macOS).
type accelerator struct {
	mods ui.Modifiers
	key  ui.Key
	menu string // MyGo's menu spelling, e.g. "CmdOrCtrl+Shift+K"
}

var keyNames = map[string]ui.Key{
	"enter": ui.KeyEnter, "escape": ui.KeyEscape, "esc": ui.KeyEscape, "backspace": ui.KeyBackspace,
	"tab": ui.KeyTab, "space": ui.KeySpace, "delete": ui.KeyDelete, "home": ui.KeyHome, "end": ui.KeyEnd,
	"pageup": ui.KeyPageUp, "pagedown": ui.KeyPageDown,
	"left": ui.KeyLeft, "right": ui.KeyRight, "up": ui.KeyUp, "down": ui.KeyDown,
	"-": ui.KeyMinus, "=": ui.KeyEqual, ",": ui.KeyComma, ".": ui.KeyPeriod, "/": ui.KeySlash,
	";": ui.KeySemicolon, "'": ui.KeyQuote, "[": ui.KeyBracketLeft, "]": ui.KeyBracketRight,
	"\\": ui.KeyBackslash, "`": ui.KeyBackquote,
	"0": ui.Key0, "1": ui.Key1, "2": ui.Key2, "3": ui.Key3, "4": ui.Key4,
	"5": ui.Key5, "6": ui.Key6, "7": ui.Key7, "8": ui.Key8, "9": ui.Key9,
	"a": ui.KeyA, "b": ui.KeyB, "c": ui.KeyC, "d": ui.KeyD, "e": ui.KeyE, "f": ui.KeyF, "g": ui.KeyG,
	"h": ui.KeyH, "i": ui.KeyI, "j": ui.KeyJ, "k": ui.KeyK, "l": ui.KeyL, "m": ui.KeyM, "n": ui.KeyN,
	"o": ui.KeyO, "p": ui.KeyP, "q": ui.KeyQ, "r": ui.KeyR, "s": ui.KeyS, "t": ui.KeyT, "u": ui.KeyU,
	"v": ui.KeyV, "w": ui.KeyW, "x": ui.KeyX, "y": ui.KeyY, "z": ui.KeyZ,
	"f1": ui.KeyF1, "f2": ui.KeyF2, "f3": ui.KeyF3, "f4": ui.KeyF4, "f5": ui.KeyF5, "f6": ui.KeyF6,
	"f7": ui.KeyF7, "f8": ui.KeyF8, "f9": ui.KeyF9, "f10": ui.KeyF10, "f11": ui.KeyF11, "f12": ui.KeyF12,
}

var menuKeyNames = map[string]string{
	"enter": "Enter", "escape": "Escape", "esc": "Escape", "backspace": "Backspace", "tab": "Tab",
	"space": "Space", "delete": "Delete", "home": "Home", "end": "End", "pageup": "PageUp",
	"pagedown": "PageDown", "left": "Left", "right": "Right", "up": "Up", "down": "Down",
}

func parseAccelerator(s string) (accelerator, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return accelerator{}, fmt.Errorf("shortcut %q has no key", s)
	}
	var a accelerator
	var menu []string
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "cmd", "cmdorctrl":
			a.mods |= ui.Cmd
			menu = append(menu, "CmdOrCtrl")
		case "ctrl":
			a.mods |= ui.Ctrl
			menu = append(menu, "Ctrl")
		case "alt", "option":
			a.mods |= ui.Alt
			menu = append(menu, "Alt")
		case "shift":
			a.mods |= ui.Shift
			menu = append(menu, "Shift")
		default:
			return accelerator{}, fmt.Errorf("shortcut %q has an unknown modifier %q", s, mod)
		}
	}
	name := parts[len(parts)-1]
	key, ok := keyNames[name]
	if !ok {
		return accelerator{}, fmt.Errorf("shortcut %q has an unknown key %q", s, name)
	}
	a.key = key
	if m, ok := menuKeyNames[name]; ok {
		name = m
	} else {
		name = strings.ToUpper(name)
	}
	a.menu = strings.Join(append(menu, name), "+")
	return a, nil
}
