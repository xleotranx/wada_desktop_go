package desktop

import "github.com/egoist/mygo/ui"

// appShortcuts registers the window's command keys: navigation stays
// one keypress away wherever focus sits. They hold back while a modal
// dialog covers the window, and the cards the tools grid draws spell
// the same keys ('Ctrl+Shift+G' opens Changes, Cmd+P opens Files).
func (a *app) appShortcuts(c *ui.Context) {
	if a.dialogsUp() {
		return
	}
	a.bindShortcut(c, "palette", a.openCommandPalette)
	a.bindShortcut(c, "new-chat", a.startNewChat)
	for i, key := range [...]ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5,
		ui.Key6, ui.Key7, ui.Key8, ui.Key9} {
		c.OnShortcut(ui.Cmd, key, func() { a.openRecentChat(i) })
	}
	a.bindShortcut(c, "search", a.openChatSearch)
	a.bindShortcut(c, "full-view", a.toggleFullView)
	a.bindShortcut(c, "text-larger", func() { a.bumpChatText(1) })
	if _, overridden := a.preferences.keymap["text-larger"]; !overridden {
		c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyEqual, func() { a.bumpChatText(1) })
	}
	a.bindShortcut(c, "text-smaller", func() { a.bumpChatText(-1) })
	a.bindShortcut(c, "text-reset", func() {
		a.setChatText(chatTextDefault)
		a.notify(ui.Toast{Title: "Conversation text: Default"})
	})
	a.bindShortcut(c, "activity", a.toggleActivity)
	a.bindShortcut(c, "unread", a.toggleUnread)
	a.bindShortcut(c, "next-marked", a.openNextMarked)
	a.bindShortcut(c, "find", a.openChatFind)
	a.bindShortcut(c, "model", a.openModelPicker)
	a.bindShortcut(c, "sidebar", a.toggleSidebar)
	a.bindShortcut(c, "tools", a.toggleTools)
	for _, action := range a.featureActions() {
		if action.Shortcut != "" {
			a.bindShortcut(c, action.Shortcut, action.Run)
		}
	}
	if a.hasFeatureAction("terminal") {
		c.OnShortcut(ui.Ctrl, ui.KeyBackquote, a.toggleTerminal)
	}
	a.bindShortcut(c, "settings", func() { a.openSettings("") })
	a.bindShortcut(c, "shortcuts", func() { a.openSettings(tabShortcuts) })
	if a.layout.findOpen {
		c.OnShortcut(0, ui.KeyEscape, func() { a.layout.findOpen = false })
	}
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
		a.modulesModal() ||
		a.layout.commandOpen ||
		a.layout.shortcutOpen
}
