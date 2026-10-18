package main

// workspaceChanged releases the previous folder's tools and their pending
// reads, then opens the active tool over the conversation or draft on show.
func (a *app) workspaceChanged() {
	a.closeBrowse()
	if a.layout.attach != nil {
		a.layout.attach.request.stop()
		a.layout.attach = nil
	}
	a.refreshWorkspacePolicy()
	mode := a.changesMode()
	if a.workspace.files != nil {
		a.workspace.files.readRequest.stop()
		a.workspace.files.findRequest.stop()
		for _, request := range a.workspace.files.listRequests {
			request.stop()
		}
	}
	if a.workspace.changes != nil {
		a.workspace.changes.request.stop()
	}
	a.workspace.files, a.workspace.changes = nil, nil
	switch a.layout.browserTool {
	case "Files":
		a.openFiles()
	case "Changes":
		a.openChanges(mode)
	}
}

// refreshWorkspacePolicy runs when navigation changes, never while drawing.
func (a *app) refreshWorkspacePolicy() {
	dir := a.chatDir()
	if a.localActive() {
		if dir == "" {
			dir = a.preferences.home
		}
		a.watchWorkspacePolicy(dir)
	} else if dir != "" {
		a.watchServerPolicy(dir)
	} else {
		a.watchServerPolicy("")
	}
}
