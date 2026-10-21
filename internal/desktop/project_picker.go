package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// projectChoice is one row of the project picker: a project's own folder,
// which starts a chat there, or a folder linked into it, which starts the
// chat where that folder stands. folder and folderCount describe the
// project's own row, whose summary names how many folders it groups.
type projectChoice struct {
	dir         string
	project     string
	folderCount int
}

// projectChoices is the picker's rows: every project, then the folders
// linked into it. A filter naming any folder of a group keeps the whole
// group, so a linked folder never shows without the project it belongs to.
func (a *app) projectChoices() []projectChoice {
	query := strings.ToLower(strings.TrimSpace(a.layout.projectFilter))
	out := []projectChoice{}
	for _, dir := range a.projectDirectories() {
		folders := a.projectFolders(dir)
		if query != "" && !slices.ContainsFunc(folders, func(folder string) bool {
			return strings.Contains(strings.ToLower(folder), query)
		}) {
			continue
		}
		out = append(out, projectChoice{dir: dir, folderCount: len(folders)})
		for _, folder := range folders[1:] {
			out = append(out, projectChoice{dir: folder, project: dir})
		}
	}
	return out
}

// openProjectPicker shows the chooser under the draft's project chip with
// the filter cleared and the draft's own folder selected, so Enter keeps
// the chat where it is.
func (a *app) openProjectPicker() {
	a.layout.projectFilter = ""
	a.layout.projectPickerOpen = true
	a.layout.projectFocus = true
	a.layout.projectSelected = 0
	for i, choice := range a.projectChoices() {
		if a.draftProjectIs(choice.dir) {
			a.layout.projectSelected = i
			break
		}
	}
	a.layout.projectList.ScrollTo(a.layout.projectSelected, ui.Center)
}

// projectSelectedIndex keeps the keyboard selection on a row that exists
// after a filter change, however many matches are left.
func (a *app) projectSelectedIndex(choices []projectChoice) int {
	if len(choices) == 0 {
		a.layout.projectSelected = 0
		return 0
	}
	a.layout.projectSelected = min(max(a.layout.projectSelected, 0), len(choices)-1)
	return a.layout.projectSelected
}

// draftProjectIs reports whether a picker row is where a new chat would
// start right now.
func (a *app) draftProjectIs(dir string) bool {
	return a.conversation.draftProject != "" && dir != "" && sameDir(a.conversation.draftProject, dir)
}

// A new chat chooses its workspace before the first message is sent.
func (a *app) projectPicker(c *ui.Context, p palette) {
	name := "Choose project"
	if a.conversation.draftProject != "" {
		name = dirBase(a.conversation.draftProject)
	}
	c.Key("draft-project")
	button := ui.ButtonBase(c).Label("Choose project").Height(28).Radius(9).PaddingX(9).
		Tooltip("Choose a project for this chat").Cursor(ui.CursorPointer)
	if button.Hovered() {
		button.Background(p.Hover)
	}
	button.OnClick(a.openProjectPicker)
	button.Children(func() {
		ui.Icon(c, icFolder).FontSize(14).TextColor(p.Muted)
		ui.Text(c, name).MarginStart(6).FontSize(12.5).SingleLine()
		ui.Icon(c, icChevDown).MarginStart(6).FontSize(11).TextColor(p.Muted)
	})
	components.Popover(c, button, &a.layout.projectPickerOpen, func(panel ui.Element) {
		panel.Width(300).MaxHeight(440).Children(func() {
			in := components.TextBox(c, &a.layout.projectFilter, components.TextBoxOptions{LeadingIcon: icSearch}).
				FillWidth().Label("Search projects").Placeholder("Search projects").FontSize(13).
				OnTextKey(func(k ui.TextKey) bool {
					choices := a.projectChoices()
					selected := a.projectSelectedIndex(choices)
					if k.Key == ui.KeyEnter {
						if len(choices) > 0 {
							a.selectDraftProject(choices[selected].dir)
						}
						return true
					}
					if k.Key == ui.KeyDown && len(choices) > 0 {
						a.layout.projectSelected = min(selected+1, len(choices)-1)
						a.layout.projectList.ScrollTo(a.layout.projectSelected, ui.Center)
						return true
					}
					if k.Key == ui.KeyUp && len(choices) > 0 {
						a.layout.projectSelected = max(selected-1, 0)
						a.layout.projectList.ScrollTo(a.layout.projectSelected, ui.Center)
						return true
					}
					return false
				})
			if in.Changed() {
				a.layout.projectSelected = 0
				a.layout.projectList.ScrollTo(0, ui.Start)
			}
			if a.layout.projectFocus {
				in.Focus()
				a.layout.projectFocus = false
			}
			choices := a.projectChoices()
			selected := a.projectSelectedIndex(choices)
			ui.List(c, &a.layout.projectList, len(choices)).MaxHeight(256).FillWidth().Gap(2).
				ItemKey(func(i int) any { return choices[i].dir }).Rows(func(row ui.ListRow) {
				a.projectChoiceRow(row.Context, p, choices[row.Index], row.Index == selected)
			})
			if len(choices) == 0 {
				ui.Text(c, "No projects match").FontSize(12).TextColor(p.Muted).Padding(6)
			}
			ui.Divider(c)
			label := "New project…"
			if !a.localActive() {
				// A remote server's folders are on its machine: the panel
				// below is this one's.
				label = "Browse the server…"
			}
			if components.Button(c, label, components.ButtonOutline).FillWidth().Clicked() {
				a.layout.projectPickerOpen = false
				if a.localActive() {
					a.layout.selectAddedProject = true
					a.openProjectDialog()
					return
				}
				a.browseServerFolders(true)
			}
			if components.Button(c, "Don't work in a project", components.ButtonGhost).FillWidth().Clicked() {
				a.selectDraftProject("")
			}
		})
	})
}

// projectChoiceRow draws one project or linked folder: the project's own
// row carries how many folders it groups, a linked folder sits indented
// under it with the link glyph, and the check marks where the draft runs.
func (a *app) projectChoiceRow(c *ui.Context, p palette, choice projectChoice, selected bool) {
	c.Key("pick-project:" + choice.dir)
	label := "Use project " + dirBase(choice.dir)
	if choice.project != "" {
		label = "Use folder " + dirBase(choice.dir) + " in " + dirBase(choice.project)
	}
	row := ui.ButtonBase(c).Label(label).Height(30).FillWidth().Radius(7).PaddingX(8).
		Tooltip(choice.dir).Disabled(a.preferences.unavailableProjects[choice.dir])
	if selected {
		row.Background(p.Selected)
	}
	if row.Hovered() {
		row.Background(p.Hover)
	}
	row.OnClick(func() { a.selectDraftProject(choice.dir) })
	row.Children(func() {
		if choice.project != "" {
			ui.Icon(c, icFolderLink).FontSize(12).TextColor(p.Faint).MarginStart(14).Shrink(0)
		} else {
			ui.Icon(c, icFolder).FontSize(13).TextColor(p.Muted).Shrink(0)
		}
		ui.Text(c, dirBase(choice.dir)).FontSize(12.5).MarginStart(7).Grow(1).SingleLine()
		if summary := folderCountSummary(choice.folderCount); summary != "" {
			ui.Text(c, summary).FontSize(10.5).TextColor(p.Faint).MarginEnd(7).Shrink(0)
		}
		if a.draftProjectIs(choice.dir) {
			ui.Icon(c, icCheck).FontSize(13).TextColor(p.Text)
		}
	})
}

// folderCountSummary names how many folders a project groups, for the
// picker's project row: one folder needs no summary.
func folderCountSummary(count int) string {
	if count <= 1 {
		return ""
	}
	return fmt.Sprintf("%d folders", count)
}

func (a *app) selectDraftProject(dir string) {
	a.connections.booted = true
	a.conversation.draftProject = dir
	a.conversation.target.revision++
	a.layout.projectPickerOpen = false
	// The chat that grows from this draft belongs in view: a linked
	// folder unfolds the project it belongs to, not a row of its own.
	if dir != "" {
		delete(a.layout.projectCollapsed, dir)
		if owner := a.projectPrimaryFor(dir); owner != "" {
			delete(a.layout.projectCollapsed, owner)
		}
	}
	a.freshTranscript()
	a.workspaceChanged()
}
