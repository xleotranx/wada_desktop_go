package coding

import (
	"path/filepath"

	"wada-desktop-go/internal/workspace"
)

// recentFile is one file the Files tool has shown, kept with the folder
// it was opened in so another workspace's relative path cannot be offered.
type RecentFile struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}

const recentFileLimit = 8

// noteRecentFile moves a file to the front of its server's recent list.
// A refresh that reads the same file again does not rewrite anything.
func (a *Module) NoteRecentFile(dir, path string) {
	if dir == "" || path == "" {
		return
	}
	entry := RecentFile{Dir: dir, Path: path}
	list := a.Recent[a.host.Context().Server]
	if len(list) > 0 && list[0] == entry {
		return
	}
	next := make([]RecentFile, 0, min(len(list)+1, recentFileLimit))
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
	if a.Recent == nil {
		a.Recent = map[string][]RecentFile{}
	}
	a.Recent[a.host.Context().Server] = next
	a.host.Save()
}

// recentFilesFor lists the recent files that belong to one workspace.
func (a *Module) RecentFilesFor(dir string) []workspace.FileEntry {
	var out []workspace.FileEntry
	for _, entry := range a.Recent[a.host.Context().Server] {
		if !sameDir(entry.Dir, dir) {
			continue
		}
		out = append(out, workspace.FileEntry{
			Path: entry.Path,
			Name: filepath.Base(entry.Path),
		})
	}
	return out
}
