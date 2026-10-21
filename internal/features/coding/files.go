package coding

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// filesShown is how much of one file the viewer shows: enough for a source
// file or a page of logs, not enough to hang the pane on a bundle.
const filesShown = 256 * 1024

// filesFound is how many matches the search asks the server for: more
// than the pane shows at once, few enough to rank well.
const filesFound = 50

// filesState is what the Files tool holds: the folder the tree hangs off,
// the listings read so far, which folders are unfolded, and the file on
// show.
type FilesState struct {
	ReadRequest, FindRequest feature.Request
	ListRequests             map[string]*feature.Request

	Location       string
	Open           map[string]bool                  // folder rel -> unfolded
	Entries        map[string][]workspace.FileEntry // folder rel -> what the server listed
	Loading        map[string]bool                  // folder rel -> a listing is on its way
	Path           string                           // the file on show, relative to the location
	Content        string
	Viewer         *editor.State
	Clipped        bool   // the file runs past what the viewer shows
	Note           string // trouble or an explanation, instead of a tree
	FileErr        string // the file on show refused to read
	Query          string // what the search field holds
	ResultsQuery   string // the query the results answered
	Results        []workspace.FileEntry
	SearchSelected int // the result the arrows chose, -1 before one is
	SearchScroll   ui.ScrollState
	Finding        bool   // a search is on its way
	FindErr        string // the search refused to run
}

// openFiles reads the open chat's folder into the Files tool. A remote chat
// without a folder says so rather than naming the server a folder of this
// machine's.
func (a *Module) OpenFiles() {
	st := a.Files
	if st == nil {
		st = &FilesState{Open: map[string]bool{}, Entries: map[string][]workspace.FileEntry{},
			Loading: map[string]bool{}}
		a.Files = st
	}
	dir := a.host.Context().Directory
	if dir == "" {
		if !a.host.Context().Local {
			st.Note = "This chat has no folder on the server yet — start it in one with Choose project."
			return
		}
		dir = a.host.Context().Home
	}
	if st.Location == dir {
		// The same folder: the tree and the file on show are read again for
		// whatever moved under them.
		a.FilesRefresh()
		return
	}
	st.Location, st.Note, st.FileErr = dir, "", ""
	st.Open, st.Entries, st.Loading = map[string]bool{}, map[string][]workspace.FileEntry{}, map[string]bool{}
	st.Path, st.Content, st.Clipped = "", "", false
	st.Viewer = nil
	st.Query, st.ResultsQuery, st.Results = "", "", nil
	st.SearchSelected, st.Finding, st.FindErr = -1, false, ""
	a.FilesList(".")
}

// filesRefresh reads again what the pane is showing: the tree with its
// root and every folder listed, or the search when one is on, and the file
// on show either way.
func (a *Module) FilesRefresh() {
	st := a.Files
	if st == nil || st.Location == "" {
		return
	}
	if st.Query != "" {
		a.FilesFind()
		return
	}
	rels := []string{"."}
	for rel := range st.Entries {
		if rel != "." {
			rels = append(rels, rel)
		}
	}
	for _, rel := range rels {
		a.FilesList(rel)
	}
	if st.Path != "" {
		a.FilesRead(st.Path)
	}
}

// filesList reads one folder of the tree, the location's own root when the
// folder is ".".
func (a *Module) FilesList(rel string) {
	st := a.Files
	if st == nil || st.Location == "" || st.Loading[rel] {
		return
	}
	if st.ListRequests == nil {
		st.ListRequests = map[string]*feature.Request{}
	}
	if st.ListRequests[rel] == nil {
		st.ListRequests[rel] = new(feature.Request)
	}
	st.Loading[rel] = true
	st.Note = ""
	snapshot := a.host.Context()
	cfg, location := snapshot.Workspace, st.Location
	feature.Run(a.host, st.ListRequests[rel], func(ctx context.Context) ([]workspace.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag := cfg
		if ag == nil {
			if snapshot.Unavailable != nil {
				return nil, snapshot.Unavailable
			}
			return nil, fmt.Errorf("this backend does not provide workspace files")
		}
		return ag.ListDir(ctx, location, rel)
	}, func(entries []workspace.FileEntry, err error) {
		if a.Files != st || st.Location != location {
			return
		}
		st.Loading[rel] = false
		if err != nil {
			st.Note = "The folder did not open: " + err.Error()
			return
		}
		st.Entries[rel] = entries
	})
}

// filesFind searches the workspace for what the field holds: the files
// whose paths match stand in for the tree while a query is named, and an
// emptied field brings the tree back.
func (a *Module) FilesFind() {
	st := a.Files
	if st == nil || st.Location == "" {
		return
	}
	st.FindRequest.Stop()
	st.Query = strings.TrimSpace(st.Query)
	st.Results, st.SearchSelected, st.FindErr, st.Finding = nil, -1, "", false
	if st.Query == "" {
		st.ResultsQuery = ""
		return
	}
	st.Finding = true
	snapshot := a.host.Context()
	cfg, location, query := snapshot.Workspace, st.Location, st.Query
	feature.Run(a.host, &st.FindRequest, func(ctx context.Context) ([]workspace.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag := cfg
		if ag == nil {
			if snapshot.Unavailable != nil {
				return nil, snapshot.Unavailable
			}
			return nil, fmt.Errorf("this backend does not provide workspace files")
		}
		return ag.FindFiles(ctx, location, query, filesFound)
	}, func(entries []workspace.FileEntry, err error) {
		if a.Files != st || st.Location != location || st.Query != query {
			return // another search moved on
		}
		st.Finding = false
		if err != nil {
			st.FindErr = "The search did not run: " + err.Error()
			return
		}
		st.Results = entries
		st.ResultsQuery = query
	})
}

// filesRead puts one file's text in the viewer, cut at what the pane shows.
func (a *Module) FilesRead(rel string) {
	st := a.Files
	if st == nil || st.Location == "" {
		return
	}
	a.NoteRecentFile(st.Location, rel)
	snapshot := a.host.Context()
	cfg, location := snapshot.Workspace, st.Location
	st.Path, st.Content, st.Clipped = rel, "Reading…", false
	st.Viewer = nil
	feature.Run(a.host, &st.ReadRequest, func(ctx context.Context) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag := cfg
		if ag == nil {
			if snapshot.Unavailable != nil {
				return nil, snapshot.Unavailable
			}
			return nil, fmt.Errorf("this backend does not provide workspace files")
		}
		return ag.ReadWorkspaceFile(ctx, location, rel)
	}, func(data []byte, err error) {
		if a.Files != st || st.Location != location || st.Path != rel {
			return
		}
		if err != nil {
			st.Content, st.Clipped = "", false
			st.FileErr = "The file did not read: " + err.Error()
			return
		}
		st.FileErr = ""
		st.Clipped = len(data) > filesShown
		if st.Clipped {
			data = data[:filesShown]
		}
		st.Content = string(data)
		buf := editor.NewBuffer(st.Content)
		buf.SetLanguage(editor.LanguageFor(rel))
		st.Viewer = &editor.State{Buffer: buf, ReadOnly: true, Label: "File preview",
			Font: ui.Font{Family: "monospace", Size: 11.5}, LineHeight: 1.5,
			SoftWrap: a.FilesWrap}
	})
}

// filesBody draws the Files tool: the tree of the chat's folder, and the
// file on show under it.
func (a *Module) FilesBody(c *ui.Context, p native.Palette) {
	st := a.Files
	if st == nil || st.Location == "" {
		if st == nil || st.Location == "" {
			// Nothing to grow a tree from; the note says why.
			if st != nil && st.Note != "" {
				ui.Text(c, st.Note).FontSize(12.5).LineHeight(1.45).TextColor(p.Muted)
			}
			return
		}
	}
	if st.Note != "" {
		ui.Text(c, st.Note).FontSize(12.5).LineHeight(1.45).TextColor(p.Muted)
		// The tree failed, not a file: the folder it is about still reads.
		ui.Text(c, "Workspace  ·  "+dirBase(st.Location)).FontSize(12).TextColor(p.Faint)
		return
	}
	if _, ok := st.Entries["."]; !ok {
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.Muted)
		components.Skeleton(c, components.SkeletonOptions{Lines: 4, Label: "Reading the workspace"})
		return
	}

	a.FilesField(c, p)
	if st.Query != "" {
		a.FilesResults(c, p)
	} else {
		ui.Text(c, "Workspace  ·  "+dirBase(st.Location)).FontSize(12).TextColor(p.Faint).SingleLine()
		ui.Scroll(c).FillWidth().MaxHeight(260).Gap(0).Children(func() {
			a.FilesRows(c, p, ".", 0)
		})
	}
	a.FilesViewer(c, p)
}

// filesField is the pane's search: what is typed narrows the workspace to
// the files whose paths match it, and Enter runs it.
func (a *Module) FilesField(c *ui.Context, p native.Palette) {
	st := a.Files
	in := components.TextBox(c, &st.Query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).
		FillWidth().Placeholder("Search files").Label("Search files").FontSize(12.5)
	in.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEnter && k.Mods == 0 {
			// Enter opens the chosen match when the results answer what the
			// field holds; otherwise it runs the search.
			if !st.Finding && len(st.Results) > 0 && st.Query == st.ResultsQuery {
				index := st.SearchSelected
				if index < 0 || index >= len(st.Results) {
					index = 0
				}
				a.FilesRead(st.Results[index].Path)
				return true
			}
			a.FilesFind()
			return true
		}
		if (k.Key == ui.KeyDown || k.Key == ui.KeyUp) && !st.Finding && len(st.Results) > 0 {
			if k.Key == ui.KeyDown {
				st.SearchSelected = min(st.SearchSelected+1, len(st.Results)-1)
			} else if st.SearchSelected < 0 {
				st.SearchSelected = len(st.Results) - 1
			} else {
				st.SearchSelected = max(st.SearchSelected-1, 0)
			}
			a.ScrollToResult(st.SearchSelected)
			return true
		}
		return false
	})
	if in.Changed() {
		st.SearchSelected = -1
	}
}

// toggleFiles opens the Files tool, or folds the pane when that tool is
// already on show.
func (a *Module) ToggleFiles() { a.host.TogglePanel("Files") }

func (a *Module) ScrollToResult(index int) {
	st := a.Files
	st.SearchScroll.ScrollTo(0, max(0, float32(index*25)-100), ui.Animation{})
}

func (a *Module) FilesResults(c *ui.Context, p native.Palette) {
	st := a.Files
	if st.Finding {
		ui.Text(c, "Searching…").FontSize(12.5).TextColor(p.Muted)
		return
	}
	if st.FindErr != "" {
		ui.Text(c, st.FindErr).FontSize(12.5).LineHeight(1.45).TextColor(p.Danger)
		return
	}
	if len(st.Results) == 0 {
		ui.Text(c, "No files match "+st.Query+".").FontSize(12.5).TextColor(p.Muted)
		return
	}
	ui.Scroll(c).FillWidth().MaxHeight(260).Gap(0).TrackScroll(&st.SearchScroll).Children(func() {
		for i, e := range st.Results {
			e := e
			chosen := e.Path == st.Path || i == st.SearchSelected
			c.Key("found:" + e.Path)
			b := ui.ButtonBase(c).Label("Read "+e.Path).Height(25).Radius(6).
				Padding(0, 6).Justify(ui.Start).FillWidth()
			b.Drag(feature.FileDrag{Path: e.Path})
			if b.Hovered() {
				b.Background(p.Hover)
			} else if chosen {
				b.Background(p.Selected)
			}
			b.OnClick(func() { a.FilesRead(e.Path) })
			b.Children(func() {
				ui.Icon(c, lucide.FileText()).FontSize(12).TextColor(p.Faint).Shrink(0)
				ui.Text(c, e.Name).MarginStart(6).FontSize(12).TextColor(p.Text).
					Shrink(0)
				if dir := path.Dir(e.Path); dir != "." {
					ui.Text(c, dir).MarginStart(6).FontSize(11.5).TextColor(p.Faint).
						Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				} else {
					ui.Spacer(c)
				}
			})
		}
	})
}

// filesViewer draws the file on show: its path, what the viewer cut off it,
// and its text.
func (a *Module) FilesViewer(c *ui.Context, p native.Palette) {
	st := a.Files
	ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
	if st.Path == "" {
		ui.Text(c, "Pick a file to read it.").FontSize(12.5).TextColor(p.Muted)
		return
	}
	line := st.Path
	if st.Clipped {
		line += fmt.Sprintf("  ·  the first %d KB", filesShown/1024)
	}
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(2).
		Label("Preview actions: " + st.Path)
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Copy path").Chosen() {
			a.CopyFilesPath()
		}
		if m.Item("Copy contents").Chosen() {
			a.CopyFilesContents()
		}
		m.Separator()
		if a.host.Context().Local {
			if m.Item("Open in editor").Chosen() {
				a.OpenFileExternally(c)
			}
			if m.Item("Reveal in folder").Chosen() {
				a.RevealFile(c)
			}
		}
		if m.Item("Show in tree").Chosen() {
			a.FilesShowInTree()
		}
		if m.Item("Close preview").Chosen() {
			a.FilesCloseViewer()
		}
	})
	row.Children(func() {
		ui.Text(c, line).FontSize(12).TextColor(p.Faint).Grow(1).MinWidth(0).
			SingleLine().Ellipsis("…")
		a.FilesWrapControl(c, p)
		native.IconButton(c, p, "Copy file path", lucide.Copy(), a.CopyFilesPath, false)
		if a.host.Context().Local {
			native.IconButton(c, p, "Open in editor", lucide.ExternalLink(), func() { a.OpenFileExternally(c) }, false)
			native.IconButton(c, p, "Reveal in folder", lucide.Folder(), func() { a.RevealFile(c) }, false)
		}
		native.IconButton(c, p, "Show in tree", lucide.ListTree(), a.FilesShowInTree, false)
		native.IconButton(c, p, "Close file preview", lucide.X(), a.FilesCloseViewer, false)
	})
	if st.FileErr != "" {
		ui.Text(c, st.FileErr).FontSize(12).TextColor(p.Danger).SingleLine()
		return
	}
	if st.Viewer == nil {
		ui.Text(c, st.Content).FontSize(12).TextColor(p.Muted)
		return
	}
	st.Viewer.SoftWrap = a.FilesWrap
	st.Viewer.Theme = &editor.Theme{Background: p.Bg, Text: p.Muted, LineNumber: p.Faint}
	editor.View(c.Key("preview:"+st.Path), st.Viewer).FillWidth().Grow(1)
}

// filesCloseViewer puts the tree back in place of the read file, which
// also frees the text the viewer held.
func (a *Module) FilesCloseViewer() {
	st := a.Files
	if st == nil {
		return
	}
	st.Path, st.Content, st.Viewer = "", "", nil
	st.FileErr, st.Clipped = "", false
}

// filesShowInTree unfolds every folder on the way to the file on show,
// clearing a search so the tree is visible, then closes the viewer there.
// Folders not read yet are asked for; the tree says they are coming.
func (a *Module) FilesShowInTree() {
	st := a.Files
	if st == nil || st.Path == "" {
		return
	}
	if st.Query != "" {
		st.FindRequest.Stop()
		st.Query, st.ResultsQuery, st.Results = "", "", nil
		st.SearchSelected, st.Finding, st.FindErr = -1, false, ""
	}
	parts := strings.Split(st.Path, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], "/")
		st.Open[parent] = true
		if _, loaded := st.Entries[parent]; !loaded {
			a.FilesList(parent)
		}
	}
	a.FilesCloseViewer()
}

// filesWrapControl wraps long lines in the viewer, or unwraps them; the
// choice holds for the next file too.
func (a *Module) FilesWrapControl(c *ui.Context, p native.Palette) {
	wrapped := a.FilesWrap
	label := "Wrap long lines"
	if wrapped {
		label = "Unwrap long lines"
	}
	c.Key("files-wrap")
	b := ui.ButtonBase(c).Label(label).Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.Hover)
	} else if wrapped {
		b.Background(p.Selected)
	}
	b.OnClick(func() {
		a.FilesWrap = !wrapped
		a.host.Save()
	})
	tint := p.Muted
	if wrapped {
		tint = p.Orange
	}
	b.Children(func() { ui.Icon(c, lucide.TextWrap()).FontSize(13).TextColor(tint) })
}

// copyFilesContents puts what the viewer read on the clipboard, saying
// so when the read is still on its way or the viewer cut the file short.
func (a *Module) CopyFilesContents() {
	st := a.Files
	if st == nil || st.Path == "" {
		return
	}
	if st.FileErr != "" {
		a.host.Notify(ui.Toast{Title: "The file did not read", Description: st.FileErr})
		return
	}
	if st.Content == "" || st.Content == "Reading…" {
		a.host.Notify(ui.Toast{Title: "The file is still being read"})
		return
	}
	if err := a.host.Copy(st.Content); err != nil {
		a.host.Notify(ui.Toast{Title: "Could not copy the file", Description: err.Error()})
		return
	}
	title := "File contents copied"
	if st.Clipped {
		title = "Copied the first " + strconv.Itoa(filesShown/1024) + " KB"
	}
	a.host.Notify(ui.Toast{Title: title})
}

// copyFilesPath puts the open file's path on the clipboard.
func (a *Module) CopyFilesPath() {
	st := a.Files
	if st == nil || st.Path == "" {
		return
	}
	text := st.Path
	if a.host.Context().Local {
		text = filepath.Join(st.Location, st.Path)
	}
	if err := a.host.Copy(text); err != nil {
		a.host.Notify(ui.Toast{Title: "Could not copy the path", Description: err.Error()})
		return
	}
	a.host.Notify(ui.Toast{Title: "File path copied"})
}

// openFileExternally hands the local file to the app registered for it.
func (a *Module) OpenFileExternally(c *ui.Context) {
	st := a.Files
	if st == nil || st.Path == "" || !a.host.Context().Local {
		return
	}
	native.OpenLocalPath(c, filepath.Join(st.Location, st.Path))
}

// revealFile opens the file's folder in the desktop's file manager.
func (a *Module) RevealFile(c *ui.Context) {
	st := a.Files
	if st == nil || st.Path == "" || !a.host.Context().Local {
		return
	}
	abs := filepath.Join(st.Location, st.Path)
	native.OpenLocalPath(c, filepath.Dir(abs))
}

// openLocalPath hands a local file or folder to its desktop handler.
// fileDrag is one file row's drag payload: the workspace-relative path a
// drop into the composer writes as an @mention.
// filesRows draws one folder's children, the unfolded folders' own under
// them, the way the tree nests.
func (a *Module) FilesRows(c *ui.Context, p native.Palette, rel string, depth int) {
	st := a.Files
	for _, e := range st.Entries[rel] {
		indent := float32(depth) * 13
		if !e.Directory {
			chosen := e.Path == st.Path
			c.Key("file:" + e.Path)
			b := ui.ButtonBase(c).Label("Read "+e.Name).Height(25).Radius(6).
				Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.Hover)
			} else if chosen {
				b.Background(p.Selected)
			}
			b.Drag(feature.FileDrag{Path: e.Path})
			b.OnClick(func() { a.FilesRead(e.Path) })
			b.Children(func() {
				ui.Icon(c, lucide.FileText()).FontSize(12).TextColor(p.Faint).Shrink(0)
				ui.Text(c, e.Name).MarginStart(6).FontSize(12).TextColor(p.Text).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
			})
			continue
		}
		c.Key("folder:" + e.Path)
		b := ui.ButtonBase(c).Label("Unfold "+e.Name).Height(25).Radius(6).
			Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
		if b.Hovered() {
			b.Background(p.Hover)
		}
		b.OnClick(func() {
			if st.Open[e.Path] {
				delete(st.Open, e.Path)
				return
			}
			st.Open[e.Path] = true
			a.FilesList(e.Path)
		})
		b.Children(func() {
			chevron := lucide.ChevronRight()
			if st.Open[e.Path] {
				chevron = lucide.ChevronDown()
			}
			ui.Icon(c, chevron).FontSize(11).TextColor(p.Faint).Shrink(0)
			ui.Icon(c, lucide.Folder()).FontSize(12).TextColor(p.Muted).MarginStart(2).Shrink(0)
			ui.Text(c, e.Name).MarginStart(6).FontSize(12.5).TextColor(p.Text).
				Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
		})
		if st.Open[e.Path] {
			if _, ok := st.Entries[e.Path]; !ok {
				ui.Row(c).Height(24).MarginStart(indent + 13).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Reading…").FontSize(11.5).TextColor(p.Faint)
				})
				continue
			}
			a.FilesRows(c, p, e.Path, depth+1)
		}
	}
}
