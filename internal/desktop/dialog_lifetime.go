package desktop

func (a *app) closeAttach() {
	if a.layout.attach != nil {
		a.layout.attach.request.stop()
		a.layout.attach = nil
	}
}
func (a *app) closeServerEditor() {
	if request := a.connections.registryRequests["pair:editor"]; request != nil {
		request.stop()
		delete(a.connections.registryRequests, "pair:editor")
	}
	a.layout.serverEditor = nil
}

// DialogBase clears open on Escape/backdrop dismissal. The next build
// releases those owners before drawing or accepting any later callbacks.
func (a *app) releaseClosedDialogs() {
	if a.layout.attach != nil && !a.layout.attach.open {
		a.closeAttach()
	}
	if a.layout.browse != nil && !a.layout.browse.open {
		a.closeBrowse()
	}
	if a.layout.serverEditor != nil && !a.layout.serverEditor.open {
		a.closeServerEditor()
	}
}
