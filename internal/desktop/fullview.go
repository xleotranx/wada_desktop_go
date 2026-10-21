package desktop

// toggleFullView hides the side panes for a conversation-only page, or
// restores the page they left. Settings step aside when the view opens.
func (a *app) toggleFullView() {
	if a.layout.fullView {
		a.leaveFullView(true)
		return
	}
	a.layout.settingsOpen = false
	a.layout.fullView = true
	a.layout.fullSidebar = a.layout.sidebarCollapsed
	a.layout.fullBrowser = a.layout.browserCollapsed
	a.layout.fullTerminalOpen = a.bottomOpen()
	a.layout.sidebarCollapsed = true
	a.layout.browserCollapsed = true
	a.setBottomOpen(false)
}

// leaveFullView closes the mode. restore puts back the page states the
// mode remembered; a layout key that acts during it instead keeps the
// panes as they stand, so the key it names is the only pane that moves.
func (a *app) leaveFullView(restore bool) {
	if !a.layout.fullView {
		return
	}
	a.layout.fullView = false
	if !restore {
		return
	}
	a.layout.sidebarCollapsed = a.layout.fullSidebar
	a.layout.browserCollapsed = a.layout.fullBrowser
	a.setBottomOpen(a.layout.fullTerminalOpen)
}

// toggleSidebar folds the sidebar, ending a full view so the key's pane
// is the one that changes.
func (a *app) toggleSidebar() {
	a.leaveFullView(false)
	a.layout.sidebarCollapsed = !a.layout.sidebarCollapsed
}

// toggleTools folds the tools pane, ending a full view so the key's pane
// is the one that changes.
func (a *app) toggleTools() {
	a.leaveFullView(false)
	a.layout.browserCollapsed = !a.layout.browserCollapsed
}

// showTools opens the tools pane from a command, ending a full view.
func (a *app) showTools() {
	a.leaveFullView(false)
	a.layout.browserCollapsed = false
}

// hideTools folds the tools pane from a command, ending a full view.
func (a *app) hideTools() {
	a.leaveFullView(false)
	a.layout.browserCollapsed = true
}

// showSidebar opens the sidebar from a command, ending a full view.
func (a *app) showSidebar() {
	a.leaveFullView(false)
	a.layout.sidebarCollapsed = false
}

// hideSidebar folds the sidebar from a command, ending a full view.
func (a *app) hideSidebar() {
	a.leaveFullView(false)
	a.layout.sidebarCollapsed = true
}
