package main

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
type filesState struct {
	readRequest, findRequest requestState
	listRequests             map[string]*requestState

	location string
	open     map[string]bool                 // folder rel -> unfolded
	entries  map[string][]opencode.FileEntry // folder rel -> what the server listed
	loading  map[string]bool                 // folder rel -> a listing is on its way
	path     string                          // the file on show, relative to the location
	content  string
	viewer   *editor.State
	clipped  bool   // the file runs past what the viewer shows
	note     string // trouble or an explanation, instead of a tree
	fileErr  string // the file on show refused to read
	query    string // what the search field holds
	results  []opencode.FileEntry
	finding  bool   // a search is on its way
	findErr  string // the search refused to run
}

// openFiles reads the open chat's folder into the Files tool. A remote chat
// without a folder says so rather than naming the server a folder of this
// machine's.
func (a *app) openFiles() {
	st := a.files
	if st == nil {
		st = &filesState{open: map[string]bool{}, entries: map[string][]opencode.FileEntry{},
			loading: map[string]bool{}}
		a.files = st
	}
	dir := a.chatDir()
	if dir == "" {
		if !a.localActive() {
			st.note = "This chat has no folder on the server yet — start it in one with Choose project."
			return
		}
		dir = a.home
	}
	if st.location == dir {
		// The same folder: the tree and the file on show are read again for
		// whatever moved under them.
		a.filesRefresh()
		return
	}
	st.location, st.note, st.fileErr = dir, "", ""
	st.open, st.entries, st.loading = map[string]bool{}, map[string][]opencode.FileEntry{}, map[string]bool{}
	st.path, st.content, st.clipped = "", "", false
	st.viewer = nil
	st.query, st.results, st.finding, st.findErr = "", nil, false, ""
	a.filesList(".")
}

// filesRefresh reads again what the pane is showing: the tree with its
// root and every folder listed, or the search when one is on, and the file
// on show either way.
func (a *app) filesRefresh() {
	st := a.files
	if st == nil || st.location == "" {
		return
	}
	if st.query != "" {
		a.filesFind()
		return
	}
	rels := []string{"."}
	for rel := range st.entries {
		if rel != "." {
			rels = append(rels, rel)
		}
	}
	for _, rel := range rels {
		a.filesList(rel)
	}
	if st.path != "" {
		a.filesRead(st.path)
	}
}

// filesList reads one folder of the tree, the location's own root when the
// folder is ".".
func (a *app) filesList(rel string) {
	st := a.files
	if st == nil || st.location == "" || st.loading[rel] {
		return
	}
	if st.listRequests == nil {
		st.listRequests = map[string]*requestState{}
	}
	if st.listRequests[rel] == nil {
		st.listRequests[rel] = new(requestState)
	}
	st.loading[rel] = true
	st.note = ""
	cfg, location := a.activeServerConn(), st.location
	runRequest(a, st.listRequests[rel], func(ctx context.Context) ([]opencode.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		return ag.ListDir(ctx, location, rel)
	}, func(entries []opencode.FileEntry, err error) {
		if a.files != st || st.location != location {
			return
		}
		st.loading[rel] = false
		if err != nil {
			st.note = "The folder did not open: " + err.Error()
			return
		}
		st.entries[rel] = entries
	})
}

// filesFind searches the workspace for what the field holds: the files
// whose paths match stand in for the tree while a query is named, and an
// emptied field brings the tree back.
func (a *app) filesFind() {
	st := a.files
	if st == nil || st.location == "" {
		return
	}
	st.findRequest.stop()
	st.query = strings.TrimSpace(st.query)
	st.results, st.findErr, st.finding = nil, "", false
	if st.query == "" {
		return
	}
	st.finding = true
	cfg, location, query := a.activeServerConn(), st.location, st.query
	runRequest(a, &st.findRequest, func(ctx context.Context) ([]opencode.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		return ag.FindFiles(ctx, location, query, filesFound)
	}, func(entries []opencode.FileEntry, err error) {
		if a.files != st || st.location != location || st.query != query {
			return // another search moved on
		}
		st.finding = false
		if err != nil {
			st.findErr = "The search did not run: " + err.Error()
			return
		}
		st.results = entries
	})
}

// filesRead puts one file's text in the viewer, cut at what the pane shows.
func (a *app) filesRead(rel string) {
	st := a.files
	if st == nil || st.location == "" {
		return
	}
	cfg, location := a.activeServerConn(), st.location
	st.path, st.content, st.clipped = rel, "Reading…", false
	st.viewer = nil
	runRequest(a, &st.readRequest, func(ctx context.Context) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		return ag.ReadWorkspaceFile(ctx, location, rel)
	}, func(data []byte, err error) {
		if a.files != st || st.location != location || st.path != rel {
			return
		}
		if err != nil {
			st.content, st.clipped = "", false
			st.fileErr = "The file did not read: " + err.Error()
			return
		}
		st.fileErr = ""
		st.clipped = len(data) > filesShown
		if st.clipped {
			data = data[:filesShown]
		}
		st.content = string(data)
		buf := editor.NewBuffer(st.content)
		buf.SetLanguage(editor.LanguageFor(rel))
		st.viewer = &editor.State{Buffer: buf, ReadOnly: true, Label: "File preview",
			Font: ui.Font{Family: "monospace", Size: 11.5}, LineHeight: 1.5}
	})
}

// filesBody draws the Files tool: the tree of the chat's folder, and the
// file on show under it.
func (a *app) filesBody(c *ui.Context, p palette) {
	st := a.files
	if st == nil || st.location == "" {
		if st == nil || st.location == "" {
			// Nothing to grow a tree from; the note says why.
			if st != nil && st.note != "" {
				ui.Text(c, st.note).FontSize(12.5).LineHeight(1.45).TextColor(p.muted)
			}
			return
		}
	}
	if st.note != "" {
		ui.Text(c, st.note).FontSize(12.5).LineHeight(1.45).TextColor(p.muted)
		// The tree failed, not a file: the folder it is about still reads.
		ui.Text(c, "Workspace  ·  "+dirBase(st.location)).FontSize(12).TextColor(p.faint)
		return
	}
	if _, ok := st.entries["."]; !ok {
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.muted)
		return
	}

	a.filesField(c, p)
	if st.query != "" {
		a.filesResults(c, p)
	} else {
		ui.Text(c, "Workspace  ·  "+dirBase(st.location)).FontSize(12).TextColor(p.faint).SingleLine()
		ui.Scroll(c).FillWidth().MaxHeight(260).Gap(0).Children(func() {
			a.filesRows(c, p, ".", 0)
		})
	}
	a.filesViewer(c, p)
}

// filesField is the pane's search: what is typed narrows the workspace to
// the files whose paths match it, and Enter runs it.
func (a *app) filesField(c *ui.Context, p palette) {
	st := a.files
	field := ui.Box(c).FillWidth().Height(32).Radius(999).Padding(0, 10).
		Background(p.composerBg).Border(1, p.composerBorder).Justify(ui.Center)
	if field.FocusWithin() {
		field.Border(1, p.composerBorderFocus)
	}
	field.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, icSearch).FontSize(13).TextColor(p.muted)
			ui.TextInputBase(c, &st.query).FontSize(12.5).
				Placeholder("Search files").Label("Search files").FillWidth().
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == 0 {
						a.filesFind()
						return true
					}
					return false
				})
		})
	})
}

// filesResults draws what the search found in place of the tree: the
// files' names, the folders they sit in beside them.
func (a *app) filesResults(c *ui.Context, p palette) {
	st := a.files
	if st.finding {
		ui.Text(c, "Searching…").FontSize(12.5).TextColor(p.muted)
		return
	}
	if st.findErr != "" {
		ui.Text(c, st.findErr).FontSize(12.5).LineHeight(1.45).TextColor(p.danger)
		return
	}
	if len(st.results) == 0 {
		ui.Text(c, "No files match "+st.query+".").FontSize(12.5).TextColor(p.muted)
		return
	}
	ui.Scroll(c).FillWidth().MaxHeight(260).Gap(0).Children(func() {
		for _, e := range st.results {
			e := e
			chosen := e.Path == st.path
			c.Key("found:" + e.Path)
			b := ui.ButtonBase(c).Label("Read "+e.Path).Height(25).Radius(6).
				Padding(0, 6).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.hover)
			} else if chosen {
				b.Background(p.selected)
			}
			b.OnClick(func() { a.filesRead(e.Path) })
			b.Children(func() {
				ui.Icon(c, icFileDoc).FontSize(12).TextColor(p.faint).Shrink(0)
				ui.Text(c, e.Name).MarginStart(6).FontSize(12).TextColor(p.text).
					Shrink(0)
				if dir := path.Dir(e.Path); dir != "." {
					ui.Text(c, dir).MarginStart(6).FontSize(11.5).TextColor(p.faint).
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
func (a *app) filesViewer(c *ui.Context, p palette) {
	st := a.files
	ui.Box(c).FillWidth().Height(1).Background(p.hairline)
	if st.path == "" {
		ui.Text(c, "Pick a file to read it.").FontSize(12.5).TextColor(p.muted)
		return
	}
	line := st.path
	if st.clipped {
		line += fmt.Sprintf("  ·  the first %d KB", filesShown/1024)
	}
	ui.Text(c, line).FontSize(12).TextColor(p.faint).SingleLine().Ellipsis("…")
	if st.fileErr != "" {
		ui.Text(c, st.fileErr).FontSize(12).TextColor(p.danger).SingleLine()
		return
	}
	if st.viewer == nil {
		ui.Text(c, st.content).FontSize(12).TextColor(p.muted)
		return
	}
	st.viewer.Theme = &editor.Theme{Background: p.bg, Text: p.muted, LineNumber: p.faint}
	editor.View(c.Key("preview:"+st.path), st.viewer).FillWidth().Grow(1)
}

// filesRows draws one folder's children, the unfolded folders' own under
// them, the way the tree nests.
func (a *app) filesRows(c *ui.Context, p palette, rel string, depth int) {
	st := a.files
	for _, e := range st.entries[rel] {
		indent := float32(depth) * 13
		if !e.Directory {
			chosen := e.Path == st.path
			c.Key("file:" + e.Path)
			b := ui.ButtonBase(c).Label("Read "+e.Name).Height(25).Radius(6).
				Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.hover)
			} else if chosen {
				b.Background(p.selected)
			}
			b.OnClick(func() { a.filesRead(e.Path) })
			b.Children(func() {
				ui.Icon(c, icFileDoc).FontSize(12).TextColor(p.faint).Shrink(0)
				ui.Text(c, e.Name).MarginStart(6).FontSize(12).TextColor(p.text).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
			})
			continue
		}
		c.Key("folder:" + e.Path)
		b := ui.ButtonBase(c).Label("Unfold "+e.Name).Height(25).Radius(6).
			Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
		if b.Hovered() {
			b.Background(p.hover)
		}
		b.OnClick(func() {
			if st.open[e.Path] {
				delete(st.open, e.Path)
				return
			}
			st.open[e.Path] = true
			a.filesList(e.Path)
		})
		b.Children(func() {
			chevron := icChevRight
			if st.open[e.Path] {
				chevron = icChevDown
			}
			ui.Icon(c, chevron).FontSize(11).TextColor(p.faint).Shrink(0)
			ui.Icon(c, icFolder).FontSize(12).TextColor(p.muted).MarginStart(2).Shrink(0)
			ui.Text(c, e.Name).MarginStart(6).FontSize(12.5).TextColor(p.text).
				Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
		})
		if st.open[e.Path] {
			if _, ok := st.entries[e.Path]; !ok {
				ui.Row(c).Height(24).MarginStart(indent + 13).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Reading…").FontSize(11.5).TextColor(p.faint)
				})
				continue
			}
			a.filesRows(c, p, e.Path, depth+1)
		}
	}
}
