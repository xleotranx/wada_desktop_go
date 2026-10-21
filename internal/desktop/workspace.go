package desktop

import "wada-desktop-go/internal/feature"

// workspaceChanged releases the previous folder's tools and their pending
// reads, then opens the active tool over the conversation or draft on show.
func (a *app) workspaceChanged() {
	a.closeBrowse()
	a.closeAttach()
	a.featureRevision++
	for _, module := range a.features {
		if aware, ok := module.(feature.ContextAware); ok {
			aware.ContextChanged()
		}
	}
	if panel, ok := a.featurePanel(a.layout.browserTool); ok {
		if panel.Open != nil {
			panel.Open()
		}
	}

}
