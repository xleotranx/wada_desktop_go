package main

import (
	"path/filepath"

	"wada-desktop-go/internal/opencode"
)

// recentFile is one file the Files tool has shown, kept with the folder
// it was opened in so another workspace's relative path cannot be offered.
type recentFile struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}

const recentFileLimit = 8

// noteRecentFile moves a file to the front of its server's recent list.
// A refresh that reads the same file again does not rewrite anything.
func (a *app) noteRecentFile(dir, path string) {
	if dir == "" || path == "" {
		return
	}
	entry := recentFile{Dir: dir, Path: path}
	list := a.preferences.recent[a.connections.activeServer]
	if len(list) > 0 && list[0] == entry {
		return
	}
	next := make([]recentFile, 0, min(len(list)+1, recentFileLimit))
	next = append(next, entry)
	for _, old := range list {
		if old == entry {
			continue
		}
		next = append(next, old)
		if len(next) == recentFileLimit {
			break
		}
	}
	if a.preferences.recent == nil {
		a.preferences.recent = map[string][]recentFile{}
	}
	a.preferences.recent[a.connections.activeServer] = next
	a.saveState()
}

// recentFilesFor lists the recent files that belong to one workspace.
func (a *app) recentFilesFor(dir string) []opencode.FileEntry {
	var out []opencode.FileEntry
	for _, entry := range a.preferences.recent[a.connections.activeServer] {
		if !sameDir(entry.Dir, dir) {
			continue
		}
		out = append(out, opencode.FileEntry{
			Path: entry.Path,
			Name: filepath.Base(entry.Path),
		})
	}
	return out
}
