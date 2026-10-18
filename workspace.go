package main

// workspaceChanged releases the previous folder's tools and their pending
// reads, then opens the active tool over the conversation or draft on show.
func (a *app) workspaceChanged() {
	mode := a.changesMode()
	a.files, a.changes = nil, nil
	switch a.browserTool {
	case "Files":
		a.openFiles()
	case "Changes":
		a.openChanges(mode)
	}
}
