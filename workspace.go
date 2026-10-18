package main

// workspaceChanged releases the previous folder's tools and their pending
// reads, then opens the active tool over the conversation or draft on show.
func (a *app) workspaceChanged() {
	a.closeBrowse()
	if a.attach != nil {
		a.attach.request.stop()
		a.attach = nil
	}
	a.refreshWorkspacePolicy()
	mode := a.changesMode()
	if a.files != nil {
		a.files.readRequest.stop()
		a.files.findRequest.stop()
		for _, request := range a.files.listRequests {
			request.stop()
		}
	}
	if a.changes != nil {
		a.changes.request.stop()
	}
	a.files, a.changes = nil, nil
	switch a.browserTool {
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
			dir = a.home
		}
		a.watchWorkspacePolicy(dir)
	} else if dir != "" {
		a.watchServerPolicy(dir)
	} else {
		a.policyDir, a.policyLevel = "", "ask"
	}
}
