package main

import "github.com/egoist/mygo"

// appMenu is the window's menu bar: the standard app, edit and window
// menus around the app's own File, Chat, View and Help actions. The app's
// own items carry no accelerators: the window's remappable keys own the
// shortcuts, and a menu key equivalent would take a key away from the
// keyboard map the user can change.
func appMenu(a *app) *mygo.Menu {
	run := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			a.menuAction(fn)
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Label: "Settings…", Click: run(func() { a.openSettings("") })},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "New Chat", Click: run(a.startNewChat)},
			{Label: "Open Folder…", Click: run(a.openProjectDialog)},
			{Label: "Quick Open File…", Click: run(a.openQuickOpen)},
			mygo.Separator(),
			{Label: "Close Chat Tab", Click: run(func() {
				if id := string(a.conversation.target.session); id != "" {
					a.closeTab(id)
				}
			})},
			{Label: "Reopen Closed Chat Tab", Click: run(a.reopenClosedTab)},
			mygo.Separator(),
			{Label: "Export Transcript…", Click: run(a.exportTranscript)},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "Chat", Submenu: []*mygo.MenuItem{
			{Label: "Find…", Click: run(a.openChatFind)},
			{Label: "Search Chats…", Click: run(a.openChatSearch)},
			{Label: "Chat History…", Click: run(a.openHistory)},
			{Label: "Copy Transcript", Click: run(a.copyTranscript)},
			{Label: "Choose Model…", Click: run(a.openModelPicker)},
			mygo.Separator(),
			{Label: "Mark Unread or Read", Click: run(a.toggleUnread)},
			{Label: "Next Chat Needing Attention", Click: run(a.openNextMarked)},
		}},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Toggle Sidebar", Click: run(a.toggleSidebar)},
			{Label: "Toggle Tools", Click: run(a.toggleTools)},
			{Label: "Toggle Terminal", Click: run(a.toggleTerminal)},
			{Label: "Toggle Full View", Click: run(a.toggleFullView)},
			mygo.Separator(),
			{Label: "Reset Pane Sizes", Click: run(a.resetPaneWidths)},
			{Role: mygo.RoleToggleDevTools},
		}},
		{Role: mygo.RoleWindowMenu},
		{Label: "Help", Submenu: []*mygo.MenuItem{
			{Label: "Keyboard Shortcuts", Click: run(func() { a.openSettings(tabShortcuts) })},
			{Label: "Slash Commands", Click: run(func() { a.layout.slashHelp = true })},
		}},
	})
}

// menuAction runs one menu bar choice on the UI thread. A test has no
// window and sets the menu hook to run the choice at once.
func (a *app) menuAction(fn func()) {
	if a.runtime.closed || fn == nil {
		return
	}
	if a.runtime.menu != nil {
		a.runtime.menu(fn)
		return
	}
	if a.runtime.win != nil {
		a.runtime.win.Update(fn)
	}
}
