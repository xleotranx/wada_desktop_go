package main

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
)

// projectFolders lists a project's own folder first, then the folders
// linked to it. Links belong to the local server: a remote path is its
// machine's, so it only ever groups its own primary folder.
func (a *app) projectFolders(primary string) []string {
	primary = canonDir(primary)
	folders := []string{primary}
	if !a.localActive() {
		return folders
	}
	for _, dir := range a.preferences.projectLinks[primary] {
		if dir == "" || sameDir(dir, primary) || slices.ContainsFunc(folders, func(folder string) bool { return sameDir(folder, dir) }) {
			continue
		}
		folders = append(folders, dir)
	}
	return folders
}

// projectPrimaryFor names the project a folder belongs to, own or linked.
func (a *app) projectPrimaryFor(dir string) string {
	if dir == "" {
		return ""
	}
	if a.localActive() {
		for primary, linked := range a.preferences.projectLinks {
			for _, folder := range linked {
				if sameDir(folder, dir) {
					return primary
				}
			}
		}
	}
	if slices.ContainsFunc(a.projectDirectories(), func(project string) bool {
		return sameDir(project, dir)
	}) {
		return dir
	}
	return ""
}

// linkProjectFolder adds one folder to a local project. A folder that is
// already a project or another project's link is refused with a toast.
func (a *app) linkProjectFolder(primary, path string) {
	if !a.localActive() {
		return
	}
	primary = canonDir(primary)
	dir := canonDir(path)
	if dir == "" || sameDir(primary, dir) {
		return
	}
	if slices.ContainsFunc(a.preferences.projects, func(project string) bool {
		return sameDir(project, dir)
	}) {
		a.notify(ui.Toast{Title: "That folder is already its own project"})
		return
	}
	if owner := a.projectPrimaryFor(dir); owner != "" {
		a.notify(ui.Toast{Title: "That folder already belongs to " + dirBase(owner)})
		return
	}
	a.preferences.projectLinks[primary] = append(a.preferences.projectLinks[primary], dir)
	delete(a.layout.projectCollapsed, primary)
	a.saveState()
}

// unlinkProjectFolder takes one folder out of a project, leaving it and
// its chats out of the sidebar until it is added somewhere again.
func (a *app) unlinkProjectFolder(primary, dir string) {
	primary = canonDir(primary)
	linked := a.preferences.projectLinks[primary]
	for i, folder := range linked {
		if sameDir(folder, dir) {
			a.preferences.projectLinks[primary] = append(linked[:i], linked[i+1:]...)
			if len(a.preferences.projectLinks[primary]) == 0 {
				delete(a.preferences.projectLinks, primary)
			}
			a.saveState()
			return
		}
	}
}

// beginLinkProjectFolder asks the system for a folder to link.
func (a *app) beginLinkProjectFolder(primary string) {
	a.runtime.pickFolder(func(path string) {
		a.linkProjectFolder(primary, path)
	})
}

// projectFolderSummary names how many folders a project groups.
func projectFolderSummary(folders []string) string {
	if len(folders) <= 1 {
		return ""
	}
	return fmt.Sprintf("%d folders", len(folders))
}
