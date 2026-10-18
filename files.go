package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// filesShown is how much of one file the viewer shows: enough for a source
// file or a page of logs, not enough to hang the pane on a bundle.
const filesShown = 256 * 1024

// filesLineCut is how much of one line the viewer draws: a minified file
// can hold a line the length of the whole window, and measuring it costs a
// frame of its own.
const filesLineCut = 500

// clipLine cuts one line to what the viewer draws, the cut shown.
func clipLine(line string) string {
	runes := []rune(line)
	if len(runes) <= filesLineCut {
		return line
	}
	return string(runes[:filesLineCut]) + "…"
}

// filesState is what the Files tool holds: the folder the tree hangs off,
// the listings read so far, which folders are unfolded, and the file on
// show.
type filesState struct {
	location string
	open     map[string]bool      // folder rel -> unfolded
	entries  map[string][]fsEntry // folder rel -> what the server listed
	loading  map[string]bool      // folder rel -> a listing is on its way
	path     string               // the file on show, relative to the location
	content  string
	clipped  bool   // the file runs past what the viewer shows
	note     string // trouble or an explanation, instead of a tree
	fileErr  string // the file on show refused to read
}

// openFiles reads the open chat's folder into the Files tool. A remote chat
// without a folder says so rather than naming the server a folder of this
// machine's.
func (a *app) openFiles() {
	st := a.files
	if st == nil {
		st = &filesState{open: map[string]bool{}, entries: map[string][]fsEntry{},
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
	st.open, st.entries, st.loading = map[string]bool{}, map[string][]fsEntry{}, map[string]bool{}
	st.path, st.content, st.clipped = "", "", false
	a.filesList(".")
}

// filesRefresh reads the tree again: the folder's own root, every folder
// already listed, and the file on show.
func (a *app) filesRefresh() {
	st := a.files
	if st == nil || st.location == "" {
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
	st.loading[rel] = true
	st.note = ""
	cfg, location := a.activeServerConn(), st.location
	inBackground(a, func() ([]fsEntry, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ag, err := standaloneAgent(cfg)
		if err != nil {
			return nil, err
		}
		return ag.listDir(ctx, location, rel)
	}, func(entries []fsEntry, err error) {
		st := a.files
		if st == nil || st.location != location {
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

// filesRead puts one file's text in the viewer, cut at what the pane shows.
func (a *app) filesRead(rel string) {
	st := a.files
	if st == nil || st.location == "" {
		return
	}
	cfg, location := a.activeServerConn(), st.location
	st.path, st.content, st.clipped = rel, "Reading…", false
	inBackground(a, func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ag, err := standaloneAgent(cfg)
		if err != nil {
			return nil, err
		}
		return ag.readWorkspaceFile(ctx, location, rel)
	}, func(data []byte, err error) {
		st := a.files
		if st == nil || st.location != location || st.path != rel {
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
	})
}

// filesBody draws the Files tool: the tree of the chat's folder, and the
// file on show under it.
func (a *app) filesBody(c *ui.Context, p palette) {
	st := a.files
	if st == nil || st.location == "" {
		a.openFiles()
		st = a.files
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
		a.filesList(".")
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.muted)
		return
	}

	ui.Text(c, "Workspace  ·  "+dirBase(st.location)).FontSize(12).TextColor(p.faint).SingleLine()
	ui.Scroll(c).FillWidth().MaxHeight(260).Gap(0).Children(func() {
		a.filesRows(c, p, ".", 0)
	})

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
	ui.ScrollBoth(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).Gap(0).Children(func() {
			for _, text := range strings.Split(strings.TrimSuffix(st.content, "\n"), "\n") {
				ui.Text(c, clipLine(text)).Font("monospace").FontSize(11.5).LineHeight(1.5).
					TextColor(p.muted).SingleLine().Shrink(0)
			}
		})
	})
}

// filesRows draws one folder's children, the unfolded folders' own under
// them, the way the tree nests.
func (a *app) filesRows(c *ui.Context, p palette, rel string, depth int) {
	st := a.files
	for _, e := range st.entries[rel] {
		indent := float32(depth) * 13
		if !e.dir {
			chosen := e.rel == st.path
			c.Key("file:" + e.rel)
			b := ui.ButtonBase(c).Label("Read "+e.name).Height(25).Radius(6).
				Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.hover)
			} else if chosen {
				b.Background(p.selected)
			}
			b.OnClick(func() { a.filesRead(e.rel) })
			b.Children(func() {
				ui.Icon(c, icFileDoc).FontSize(12).TextColor(p.faint).Shrink(0)
				ui.Text(c, e.name).MarginStart(6).FontSize(12).TextColor(p.text).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
			})
			continue
		}
		c.Key("folder:" + e.rel)
		b := ui.ButtonBase(c).Label("Unfold "+e.name).Height(25).Radius(6).
			Padding(0, 6).MarginStart(indent).Justify(ui.Start).FillWidth()
		if b.Hovered() {
			b.Background(p.hover)
		}
		b.OnClick(func() {
			if st.open[e.rel] {
				delete(st.open, e.rel)
				return
			}
			st.open[e.rel] = true
			a.filesList(e.rel)
		})
		b.Children(func() {
			chevron := icChevRight
			if st.open[e.rel] {
				chevron = icChevDown
			}
			ui.Icon(c, chevron).FontSize(11).TextColor(p.faint).Shrink(0)
			ui.Icon(c, icFolder).FontSize(12).TextColor(p.muted).MarginStart(2).Shrink(0)
			ui.Text(c, e.name).MarginStart(6).FontSize(12.5).TextColor(p.text).
				Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
		})
		if st.open[e.rel] {
			if _, ok := st.entries[e.rel]; !ok {
				ui.Row(c).Height(24).MarginStart(indent + 13).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Reading…").FontSize(11.5).TextColor(p.faint)
				})
				continue
			}
			a.filesRows(c, p, e.rel, depth+1)
		}
	}
}
