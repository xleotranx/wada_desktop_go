package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// chord is one shortcut's keys, stored as numbers so a preference reads
// back exactly what the recorder caught.
type chord struct {
	Mods ui.Modifiers `json:"mods"`
	Key  ui.Key       `json:"key"`
}

// shortcutSpec is one command whose key can be changed, with its default.
type shortcutSpec struct {
	id, label string
	mods      ui.Modifiers
	key       ui.Key
}

var shortcutSpecs = []shortcutSpec{
	{"palette", "Command palette", ui.Cmd, ui.KeyK},
	{"new-chat", "New chat", ui.Cmd, ui.KeyN},
	{"find", "Find in chat", ui.Cmd, ui.KeyF},
	{"search", "Search chats", ui.Cmd | ui.Alt, ui.KeyF},
	{"quick-open", "Quick open file", ui.Cmd, ui.KeyP},
	{"sidebar", "Toggle sidebar", ui.Cmd, ui.KeyB},
	{"terminal", "Toggle terminal", ui.Cmd, ui.KeyJ},
	{"tools", "Toggle tools", ui.Cmd | ui.Shift, ui.KeyJ},
	{"full-view", "Toggle full view", ui.Cmd | ui.Shift, ui.KeyF},
	{"activity", "Activity view", ui.Cmd | ui.Alt, ui.KeyU},
	{"unread", "Mark chat unread or read", ui.Cmd | ui.Shift, ui.KeyU},
	{"next-marked", "Next chat needing attention", ui.Cmd | ui.Alt, ui.KeyA},
	{"files", "Toggle file pane", ui.Cmd | ui.Shift, ui.KeyE},
	{"changes", "Open Changes", ui.Ctrl | ui.Shift, ui.KeyG},
	{"model", "Choose a model", ui.Ctrl | ui.Shift, ui.KeyM},
	{"settings", "Settings", ui.Cmd, ui.KeyComma},
}

func shortcutSpecByID(id string) (shortcutSpec, bool) {
	for _, spec := range shortcutSpecs {
		if spec.id == id {
			return spec, true
		}
	}
	return shortcutSpec{}, false
}

// shortcutChord is the binding in force for a command: the user's choice
// or the spec's default.
func (a *app) shortcutChord(id string) chord {
	if c, ok := a.preferences.keymap[id]; ok {
		return c
	}
	if spec, ok := shortcutSpecByID(id); ok {
		return chord{Mods: spec.mods, Key: spec.key}
	}
	return chord{}
}

// shortcutKeys writes a chord the way the keyboard page and palette do.
func (a *app) shortcutKeys(id string) string {
	return chordLabel(a.shortcutChord(id))
}

// setShortcut stores one choice and saves it.
func (a *app) setShortcut(id string, c chord) {
	if a.preferences.keymap == nil {
		a.preferences.keymap = map[string]chord{}
	}
	a.preferences.keymap[id] = c
	a.saveState()
}

// resetShortcut returns one command to its default.
func (a *app) resetShortcut(id string) {
	delete(a.preferences.keymap, id)
	a.saveState()
}

// shortcutConflict names the command already using c, skipping the command
// being changed.
func (a *app) shortcutConflict(id string, c chord) (string, bool) {
	for _, spec := range shortcutSpecs {
		if spec.id != id && a.shortcutChord(spec.id) == c {
			return spec.label, true
		}
	}
	for _, fixed := range fixedShortcuts {
		if fixed.chord == c {
			return fixed.label, true
		}
	}
	return "", false
}

// fixedShortcuts are the keys that stay put; the recorder refuses to take
// one from them.
var fixedShortcuts = []struct {
	label string
	chord chord
}{
	{"Open a recent chat", chord{ui.Cmd, ui.Key1}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key2}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key3}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key4}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key5}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key6}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key7}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key8}},
	{"Open a recent chat", chord{ui.Cmd, ui.Key9}},
	{"Next chat tab", chord{ui.Ctrl, ui.KeyTab}},
	{"Previous chat tab", chord{ui.Ctrl | ui.Shift, ui.KeyTab}},
	{"Reopen closed chat tab", chord{ui.Cmd | ui.Shift, ui.KeyT}},
	{"Open a folder", chord{ui.Cmd, ui.KeyO}},
	{"Keyboard shortcuts", chord{ui.Cmd, ui.KeySlash}},
	{"Conversation text larger", chord{ui.Cmd, ui.KeyEqual}},
	{"Conversation text smaller", chord{ui.Cmd, ui.KeyMinus}},
	{"Reset conversation text", chord{ui.Cmd, ui.Key0}},
	{"Copy", chord{ui.Cmd, ui.KeyC}},
	{"Paste", chord{ui.Cmd, ui.KeyV}},
	{"Cut", chord{ui.Cmd, ui.KeyX}},
	{"Select all", chord{ui.Cmd, ui.KeyA}},
	{"Undo", chord{ui.Cmd, ui.KeyZ}},
}

// keyLabels names the keys a recorder can catch in a readable way.
var keyLabels = map[ui.Key]string{
	ui.KeyEnter: "Enter", ui.KeyEscape: "Esc", ui.KeyBackspace: "Backspace",
	ui.KeyTab: "Tab", ui.KeySpace: "Space", ui.KeyDelete: "Delete",
	ui.KeyInsert: "Insert", ui.KeyHome: "Home", ui.KeyEnd: "End",
	ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
	ui.KeyLeft: "←", ui.KeyRight: "→", ui.KeyUp: "↑", ui.KeyDown: "↓",
	ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".",
	ui.KeySlash: "/", ui.KeySemicolon: ";", ui.KeyQuote: "'",
	ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\",
	ui.KeyBackquote: "`",
	ui.Key0:         "0", ui.Key1: "1", ui.Key2: "2", ui.Key3: "3", ui.Key4: "4",
	ui.Key5: "5", ui.Key6: "6", ui.Key7: "7", ui.Key8: "8", ui.Key9: "9",
	ui.KeyA: "A", ui.KeyB: "B", ui.KeyC: "C", ui.KeyD: "D", ui.KeyE: "E",
	ui.KeyF: "F", ui.KeyG: "G", ui.KeyH: "H", ui.KeyI: "I", ui.KeyJ: "J",
	ui.KeyK: "K", ui.KeyL: "L", ui.KeyM: "M", ui.KeyN: "N", ui.KeyO: "O",
	ui.KeyP: "P", ui.KeyQ: "Q", ui.KeyR: "R", ui.KeyS: "S", ui.KeyT: "T",
	ui.KeyU: "U", ui.KeyV: "V", ui.KeyW: "W", ui.KeyX: "X", ui.KeyY: "Y",
	ui.KeyZ:  "Z",
	ui.KeyF1: "F1", ui.KeyF2: "F2", ui.KeyF3: "F3", ui.KeyF4: "F4",
	ui.KeyF5: "F5", ui.KeyF6: "F6", ui.KeyF7: "F7", ui.KeyF8: "F8",
	ui.KeyF9: "F9", ui.KeyF10: "F10", ui.KeyF11: "F11", ui.KeyF12: "F12",
}

// chordLabel writes a chord with the symbols the app's keycaps use.
func chordLabel(c chord) string {
	var b strings.Builder
	if ui.Cmd == ui.Ctrl {
		if c.Mods&ui.Ctrl != 0 {
			b.WriteString("Ctrl+")
		}
	} else {
		if c.Mods&ui.Cmd != 0 {
			b.WriteString("⌘")
		}
		if c.Mods&ui.Ctrl != 0 {
			b.WriteString("⌃")
		}
	}
	if c.Mods&ui.Alt != 0 {
		b.WriteString("⌥")
	}
	if c.Mods&ui.Shift != 0 {
		b.WriteString("⇧")
	}
	if label, ok := keyLabels[c.Key]; ok {
		b.WriteString(label)
	} else {
		b.WriteString("Key")
	}
	return b.String()
}

// shortcutRowIDs maps the keyboard page's labels to editable commands.
var shortcutRowIDs = map[string]string{
	"Command palette":             "palette",
	"New chat":                    "new-chat",
	"Find in chat":                "find",
	"Search chats":                "search",
	"Quick open file":             "quick-open",
	"Toggle sidebar":              "sidebar",
	"Toggle terminal":             "terminal",
	"Toggle tools":                "tools",
	"Toggle full view":            "full-view",
	"Activity view":               "activity",
	"Mark chat unread or read":    "unread",
	"Next chat needing attention": "next-marked",
	"Toggle file pane":            "files",
	"Open Changes":                "changes",
	"Choose a model":              "model",
	"Settings":                    "settings",
}

// shortcutRowKeys writes the keycap for a keyboard-page row, following a
// remap when one exists.
func (a *app) shortcutRowKeys(label, fallback string) string {
	id, ok := shortcutRowIDs[label]
	if !ok {
		return fallback
	}
	keys := a.shortcutKeys(id)
	if id == "terminal" {
		keys += " / ⌃`"
	}
	return keys
}

// shortcutActionIDs maps palette actions to the command that owns the key.
var shortcutActionIDs = map[string]string{
	"action:new-chat":     "new-chat",
	"action:search":       "search",
	"action:find":         "find",
	"action:model":        "model",
	"action:unread":       "unread",
	"action:next-marked":  "next-marked",
	"action:activity":     "activity",
	"action:files":        "quick-open",
	"action:changes":      "changes",
	"action:settings":     "settings",
	"action:terminal":     "terminal",
	"action:show-tools":   "tools",
	"action:hide-tools":   "tools",
	"action:show-sidebar": "sidebar",
	"action:hide-sidebar": "sidebar",
	"action:full-view":    "full-view",
}

// openShortcuts shows the customization dialog.
func (a *app) openShortcuts() {
	a.layout.shortcutOpen = true
	a.layout.shortcutRecord = ""
}

// bindShortcut registers one command at its current or default keys.
func (a *app) bindShortcut(c *ui.Context, id string, fn func()) {
	ch := a.shortcutChord(id)
	if ch.Key == 0 {
		return
	}
	c.OnShortcut(ch.Mods, ch.Key, fn)
}

func (a *app) shortcutsDialog(c *ui.Context, p palette) {
	if !a.layout.shortcutOpen {
		return
	}
	dialog(c, p, &a.layout.shortcutOpen, 620, "Window shortcuts", func() {
		if a.layout.shortcutRecord != "" {
			a.shortcutRecorder(c, p)
			return
		}
		ui.Text(c, "Window shortcuts").FontSize(15).FontWeight(600).TextColor(p.text)
		ui.Text(c, "Change a command's keys here. Recording takes any combination with Command, Control or Option; Delete resets it to the default.").
			FontSize(12).LineHeight(1.45).TextColor(p.muted)
		ui.Scroll(c).FillWidth().MaxHeight(390).Gap(2).Children(func() {
			for _, spec := range shortcutSpecs {
				a.shortcutEditRow(c, p, spec)
			}
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Esc closes").FontSize(11).TextColor(p.faint)
			ui.Spacer(c)
			if components.Button(c, "Reset all", components.ButtonGhost).Clicked() {
				a.preferences.keymap = map[string]chord{}
				a.saveState()
			}
			if components.Button(c, "Done", components.ButtonSecondary).Clicked() {
				a.layout.shortcutOpen = false
			}
		})
	})
}

func (a *app) shortcutEditRow(c *ui.Context, p palette, spec shortcutSpec) {
	c.Key("shortcut-row:" + spec.id)
	ui.Row(c).FillWidth().Height(40).Padding(0, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, spec.label).FontSize(12.5).TextColor(p.text).Grow(1).MinWidth(0).SingleLine()
		a.keycap(c, p, a.shortcutKeys(spec.id))
		change := ui.ButtonBase(c).Label("Change "+spec.label).Height(26).Radius(6).
			Padding(0, 8).Center()
		if change.Hovered() {
			change.Background(p.hover)
		}
		change.OnClick(func() { a.layout.shortcutRecord = spec.id })
		change.Children(func() {
			ui.Text(c, "Change").FontSize(11.5).TextColor(p.text)
		})
		_, overridden := a.preferences.keymap[spec.id]
		reset := ui.ButtonBase(c).Label("Reset "+spec.label).Height(26).Radius(6).
			Padding(0, 8).Center().Disabled(!overridden)
		if reset.Hovered() && overridden {
			reset.Background(p.hover)
		}
		reset.OnClick(func() { a.resetShortcut(spec.id) })
		reset.Children(func() {
			tint := p.faint
			if overridden {
				tint = p.muted
			}
			ui.Text(c, "Reset").FontSize(11.5).TextColor(tint)
		})
	})
}

func (a *app) shortcutRecorder(c *ui.Context, p palette) {
	spec, ok := shortcutSpecByID(a.layout.shortcutRecord)
	if !ok {
		a.layout.shortcutRecord = ""
		return
	}
	ui.Text(c, "Press the new shortcut for “"+spec.label+"”").
		FontSize(14).FontWeight(600).TextColor(p.text)
	ui.Text(c, "Command, Control or Option must be part of it. Esc cancels; Delete resets the default.").
		FontSize(12).LineHeight(1.45).TextColor(p.muted)
	rec := ui.ButtonBase(c).Label("Press a shortcut").Height(84).Radius(10).
		FillWidth().Center().Border(1, p.hairline).Background(p.detailBg)
	rec.Focus()
	rec.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputKeyDown {
			return false
		}
		if ev.Key == ui.KeyEscape && ev.Mods == 0 {
			a.layout.shortcutRecord = ""
			return true
		}
		if ev.Mods == 0 && (ev.Key == ui.KeyBackspace || ev.Key == ui.KeyDelete) {
			a.resetShortcut(spec.id)
			a.layout.shortcutRecord = ""
			a.notify(ui.Toast{Title: spec.label + " is back to its default"})
			return true
		}
		if ev.Mods == 0 || ev.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) == 0 || ev.Key == ui.KeyUnknown {
			return true
		}
		choice := chord{Mods: ev.Mods, Key: ev.Key}
		if label, used := a.shortcutConflict(spec.id, choice); used {
			a.notify(ui.Toast{Title: "Shortcut already used by " + label,
				Type: components.ToastError})
			return true
		}
		a.setShortcut(spec.id, choice)
		a.layout.shortcutRecord = ""
		a.notify(ui.Toast{Title: spec.label + " is now " + chordLabel(choice)})
		return true
	})
	rec.Children(func() {
		ui.Text(c, "Press the keys now").FontSize(13).TextColor(p.muted)
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Esc cancels").FontSize(11).TextColor(p.faint)
		ui.Spacer(c)
		if components.Button(c, "Cancel", components.ButtonGhost).Clicked() {
			a.layout.shortcutRecord = ""
		}
	})
}
