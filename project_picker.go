package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// A new chat chooses its workspace before the first message is sent.
func (a *app) projectPicker(c *ui.Context, p palette) {
	name := "Choose project"
	if a.draftProject != "" {
		name = dirBase(a.draftProject)
	}
	c.Key("draft-project")
	button := ui.ButtonBase(c).Label("Choose project").Height(28).Radius(9).PaddingX(9).
		Tooltip("Choose a project for this chat").Cursor(ui.CursorPointer)
	if button.Hovered() {
		button.Background(p.hover)
	}
	button.OnClick(func() { a.projectFilter = ""; a.projectPickerOpen = true })
	button.Children(func() {
		ui.Icon(c, icFolder).FontSize(14).TextColor(p.muted)
		ui.Text(c, name).MarginStart(6).FontSize(12.5).SingleLine()
		ui.Icon(c, icChevDown).MarginStart(6).FontSize(11).TextColor(p.muted)
	})
	ui.PopoverBase(c, button, &a.projectPickerOpen, func(panel ui.Element) {
		panel.Width(280).MaxHeight(360).Radius(12).Padding(10).Gap(4).Margin(4, 0).
			Background(p.bg).Border(1, p.hairline).
			Shadow(0, 10, 30, 0, p.shadow).Children(func() {
			textField(c, p, &a.projectFilter, "Search projects")
			ui.Scroll(c).Grow(1).FillWidth().Gap(2).Children(func() {
				matches := 0
				for _, dir := range a.projectDirectories() {
					if !strings.Contains(strings.ToLower(dir), strings.ToLower(strings.TrimSpace(a.projectFilter))) {
						continue
					}
					matches++
					c.Key("pick-project:" + dir)
					pick := ui.ButtonBase(c).Label("Use project " + dirBase(dir)).Height(30).FillWidth().Radius(7).PaddingX(8).Tooltip(dir).Disabled(a.unavailableProjects[dir])
					if pick.Hovered() {
						pick.Background(p.hover)
					} else if dir == a.draftProject {
						pick.Background(p.selected)
					}
					pick.OnClick(func() { a.selectDraftProject(dir) })
					pick.Children(func() {
						ui.Icon(c, icFolder).FontSize(13).TextColor(p.muted)
						ui.Text(c, dirBase(dir)).FontSize(12.5).MarginStart(7).Grow(1).SingleLine()
						if dir == a.draftProject {
							ui.Icon(c, icCheck).FontSize(13).TextColor(p.text)
						}
					})
				}
				if matches == 0 {
					ui.Text(c, "No projects match").FontSize(12).TextColor(p.muted).Padding(6)
				}
			})
			ui.Divider(c)
			label := "New project…"
			if !a.localActive() {
				// A remote server's folders are on its machine: the panel
				// below is this one's.
				label = "Browse the server…"
			}
			ui.Button(c, label).OnClick(func() {
				a.projectPickerOpen = false
				if a.localActive() {
					a.selectAddedProject = true
					a.openProjectDialog()
					return
				}
				a.browseServerFolders(true)
			})
			ui.Button(c, "Don't work in a project").OnClick(func() { a.selectDraftProject("") })
		})
	})
}

func (a *app) selectDraftProject(dir string) {
	a.draftProject = dir
	a.draftVersion++
	a.projectPickerOpen = false
	// The chat that grows from this draft belongs in view.
	delete(a.projectCollapsed, dir)
	a.freshTranscript()
	a.workspaceChanged()
}
