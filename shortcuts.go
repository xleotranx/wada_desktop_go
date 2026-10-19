package main

import "github.com/egoist/mygo/ui"

// appShortcuts registers the window's command keys: navigation stays
// one keypress away wherever focus sits. They hold back while a modal
// dialog covers the window, and the cards the tools grid draws spell
// the same keys ('Ctrl+Shift+G' opens Changes, Cmd+P opens Files).
func (a *app) appShortcuts(c *ui.Context) {
	if a.dialogsUp() {
		return
	}
	c.OnShortcut(ui.Cmd, ui.KeyK, a.openCommandPalette)
	c.OnShortcut(ui.Cmd, ui.KeyN, a.startNewChat)
	c.OnShortcut(ui.Cmd, ui.KeyF, a.openChatSearch)
	c.OnShortcut(ui.Cmd, ui.KeyP, func() { a.openTool("Files") })
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyG, func() { a.openTool("Changes") })
	c.OnShortcut(ui.Cmd, ui.KeyComma, func() { a.openSettings("") })
}

// dialogsUp reports a modal dialog covering the window: rename, delete
// confirm, attach, folder browse or the server's editor pair.
func (a *app) dialogsUp() bool {
	return (a.layout.rename != nil && a.layout.rename.open) ||
		(a.layout.confirm != nil && a.layout.confirm.open) ||
		(a.layout.attach != nil && a.layout.attach.open) ||
		(a.layout.browse != nil && a.layout.browse.open) ||
		(a.layout.serverEditor != nil && a.layout.serverEditor.open) ||
		(a.layout.serverConfirm != nil && a.layout.serverConfirm.open) ||
		a.layout.commandOpen
}
