package main

import (
	"context"
	"strconv"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/components"
)

// The two reads the Changes tool offers.
const (
	changesWorking = "working" // what the workspace has not committed yet
	changesBranch  = "branch"  // everything the branch holds beyond its base
)

// changesState is what the Changes tool holds: the folder it compared, the
// mode it compared in, the files that came back and which one's patch is
// on show.
type changesState struct {
	request requestState

	loading  bool
	dir      string
	mode     string
	info     opencode.VCSInfo
	files    []opencode.Change
	selected int
	note     string

	// Keep the selected patch's buffers and selection across frames.
	patchFile, patch string
	diff             *editor.DiffState
}

// changesRead is one read of a workspace's changes: the folder and mode it
// answered for, what version control the folder sits in, and the diff.
type changesRead struct {
	dir   string
	mode  string
	info  opencode.VCSInfo
	files []opencode.Change
}

// readChanges asks a server for one folder's version control and the diff
// of one mode. What it asked for rides along even when it fails, so the
// pane can tell its own read's trouble from another read's.
func readChanges(ctx context.Context, cfg serverConn, dir, mode string) (changesRead, error) {
	read := changesRead{dir: dir, mode: mode}
	ag, err := opencode.NewClient(cfg.transport())
	if err != nil {
		return read, err
	}
	info, err := ag.WorkspaceVcs(ctx, dir)
	if err != nil {
		return read, err
	}
	files, err := ag.WorkspaceChanges(ctx, dir, mode)
	if err != nil {
		return read, err
	}
	read.info, read.files = info, files
	return read, nil
}

// changesMode is the read the Changes tool is on, the working tree until
// another one was chosen.
func (a *app) changesMode() string {
	if a.workspace.changes != nil && a.workspace.changes.mode != "" {
		return a.workspace.changes.mode
	}
	return changesWorking
}

// openChanges reads the open chat's folder into the Changes tool, or
// re-reads it in another mode. A remote chat without a folder has nothing
// to compare — and naming this machine's folder to the server would file
// the server a project it does not have.
func (a *app) openChanges(mode string) {
	st := a.workspace.changes
	if st == nil {
		st = &changesState{}
		a.workspace.changes = st
	}
	dir := a.chatDir()
	if dir == "" {
		if !a.localActive() {
			st.loading, st.note = false, "This chat has no folder on the server yet, so there is nothing to compare."
			return
		}
		dir = a.preferences.home
	}
	st.loading, st.note, st.dir, st.mode = true, "", dir, mode
	st.files, st.selected = nil, 0
	cfg := a.activeServerConn()
	runRequest(a, &st.request, func(ctx context.Context) (changesRead, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return readChanges(ctx, cfg, dir, mode)
	}, func(read changesRead, err error) {
		if a.workspace.changes != st || st.dir != read.dir || st.mode != read.mode {
			return // another read moved on
		}
		st.loading = false
		if err != nil {
			st.note = "The changes did not load: " + err.Error()
			return
		}
		st.info, st.files = read.info, read.files
	})
}

// changesBody draws the Changes tool: what the workspace sits in, the two
// modes, the files touched with their counts, and the patch of the file on
// show.
func (a *app) changesBody(c *ui.Context, p palette) {
	st := a.workspace.changes
	if st == nil {
		return
	}
	if st.loading {
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.muted)
		components.Skeleton(c, components.SkeletonOptions{Lines: 5, Label: "Reading the workspace"})
		return
	}
	if st.note != "" {
		ui.Text(c, st.note).FontSize(12.5).LineHeight(1.45).TextColor(p.muted)
		return
	}

	label := st.info.Provider
	if st.info.Branch != "" {
		label += " · " + st.info.Branch
	}
	if st.mode == changesBranch && st.info.Base != "" {
		label += " against " + st.info.Base
	}
	added, removed := 0, 0
	for _, f := range st.files {
		added, removed = added+f.Additions, removed+f.Deletions
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.faint).SingleLine().Shrink(1).MinWidth(0)
		ui.Spacer(c)
		if len(st.files) > 0 {
			for _, part := range changesCounts(p, added, removed) {
				ui.Text(c, part.text).Font("monospace").FontSize(11).TextColor(part.tint).Shrink(0)
			}
		}
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		// The two reads share a track, as Onevra's segmented tabs do, so
		// the chosen one stands on the content surface.
		ui.Row(c).Shrink(0).Gap(2).Padding(2).Radius(8).Background(p.hover).Children(func() {
			a.modePill(c, p, "Uncommitted", changesWorking, st.mode == changesWorking)
			a.modePill(c, p, "Branch", changesBranch, st.mode == changesBranch)
		})
		ui.Spacer(c)
		ui.Text(c, pluralFiles(len(st.files))).FontSize(12).TextColor(p.faint).SingleLine()
	})

	if len(st.files) == 0 {
		ui.Text(c, "Nothing changed here.").FontSize(12.5).TextColor(p.muted)
		return
	}
	ui.Scroll(c).FillWidth().MaxHeight(190).Gap(1).Children(func() {
		for i, f := range st.files {
			i, f := i, f
			c.Key("change:" + f.File)
			b := ui.ButtonBase(c).Label("Show "+f.File).Height(26).Radius(6).
				Padding(0, 8).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.hover)
			} else if i == st.selected {
				b.Background(p.selected)
			}
			b.OnClick(func() { st.selected = i })
			b.Children(func() {
				ui.Text(c, statusLetter(f.Status)).Font("monospace").FontSize(11.5).
					TextColor(statusTint(p, f.Status)).Width(14).Shrink(0)
				ui.Text(c, f.File).FontSize(12.5).TextColor(p.text).MarginStart(6).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				for _, part := range changesCounts(p, f.Additions, f.Deletions) {
					ui.Text(c, part.text).Font("monospace").FontSize(11).TextColor(part.tint).Shrink(0)
				}
			})
		}
	})

	if st.selected >= len(st.files) {
		st.selected = 0
	}
	f := st.files[st.selected]
	ui.Box(c).FillWidth().Height(1).Background(p.hairline)
	// The file on show gets its own header: the name the patch belongs to
	// and the reading it is drawn in, so a long list never hides which
	// file the diff below describes.
	ui.Row(c).FillWidth().Height(30).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, f.File).Font("monospace").FontSize(12).FontWeight(500).TextColor(p.text).
			Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(f.File)
		for _, part := range changesCounts(p, f.Additions, f.Deletions) {
			ui.Text(c, part.text).Font("monospace").FontSize(11).TextColor(part.tint).Shrink(0)
		}
		a.diffStyleControl(c, p)
	})
	layout := a.diffLayout()
	if st.diff == nil || st.patchFile != f.File || st.patch != f.Patch || st.diff.Layout != layout {
		st.patchFile, st.patch = f.File, f.Patch
		st.diff = &editor.DiffState{Layout: layout, Indicators: editor.DiffClassic,
			Font: ui.Font{Family: "monospace", Size: 11.5}, LineHeight: 1.5}
		if patches, err := editor.ParsePatch(f.Patch); err == nil && len(patches) == 1 && len(patches[0].Hunks) > 0 {
			st.diff.SetPatch(patches[0])
			lang := editor.LanguageFor(f.File)
			st.diff.Old.SetLanguage(lang)
			st.diff.New.SetLanguage(lang)
		}
	}
	if st.diff.New == nil {
		// Binary, metadata-only and malformed patches still show their raw text.
		ui.TextAreaBase(c, &st.patch).Label("Raw patch").ReadOnly(true).
			Font("monospace").FontSize(11.5).LineHeight(1.5).FillWidth().Grow(1)
		return
	}
	st.diff.Theme = &editor.Theme{Background: p.bg, Text: p.muted,
		LineNumber: p.faint, Added: p.diffAdd, Deleted: p.diffDel, Modified: p.link}
	editor.DiffView(c.Key("patch:"+f.File), st.diff).FillWidth().Grow(1)
}

// modePill is one of the Changes tool's reads, the chosen one standing out.
func (a *app) modePill(c *ui.Context, p palette, label, mode string, chosen bool) {
	c.Key("changes-mode:" + mode)
	b := ui.ButtonBase(c).Label(label + " changes").Height(24).Radius(6).PaddingX(10).Center()
	if chosen {
		b.Background(c.Theme().Background).Border(1, p.hairline)
	} else if b.Hovered() {
		b.Background(p.selected)
	}
	b.OnClick(func() { a.openChanges(mode) })
	b.Children(func() {
		tint := p.muted
		if chosen {
			tint = p.text
		}
		t := ui.Text(c, label).FontSize(12).TextColor(tint).SingleLine()
		if chosen {
			t.FontWeight(500)
		}
	})
}

// diffLayout is the reading the selected file's patch is drawn in: one
// column by default, two when the split was chosen.
func (a *app) diffLayout() editor.DiffLayout {
	if a.preferences.diffSplit {
		return editor.DiffSplit
	}
	return editor.DiffUnified
}

// setDiffSplit switches the diff's reading and saves it for the next
// visit, as the pane's own preference.
func (a *app) setDiffSplit(split bool) {
	if a.preferences.diffSplit == split {
		return
	}
	a.preferences.diffSplit = split
	a.saveState()
}

// diffStyleControl is the two-state reading switch in the selected file's
// header: the rows glyph for a unified diff, the columns glyph for a split
// one.
func (a *app) diffStyleControl(c *ui.Context, p palette) {
	ui.Row(c).Shrink(0).Gap(2).Padding(2).Radius(8).Background(p.hover).Children(func() {
		a.diffStyleButton(c, p, "Unified diff", icDiffRows, false)
		a.diffStyleButton(c, p, "Split diff", icDiffSplit, true)
	})
}

func (a *app) diffStyleButton(c *ui.Context, p palette, label string, icon *ui.SVG, split bool) {
	chosen := a.preferences.diffSplit == split
	c.Key("diff-style:" + label)
	b := ui.ButtonBase(c).Label(label).Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if chosen {
		b.Background(c.Theme().Background).Border(1, p.hairline)
	} else if b.Hovered() {
		b.Background(p.selected)
	}
	b.OnClick(func() { a.setDiffSplit(split) })
	tint := p.muted
	if chosen {
		tint = p.text
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(tint) })
}

// changesCount is one piece of a file's count line, colored by what it
// counts.
type changesCount struct {
	text string
	tint ui.Color
}

// changesCounts words what a diff added and removed.
func changesCounts(p palette, added, removed int) []changesCount {
	return []changesCount{
		{"+" + strconv.Itoa(added), p.diffAdd},
		{"−" + strconv.Itoa(removed), p.diffDel},
	}
}

// pluralFiles words how many files a diff touched.
func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return strconv.Itoa(n) + " files"
}

// statusLetter is what one changed file's status reads as, the way git
// spells it.
func statusLetter(status string) string {
	switch status {
	case "added":
		return "A"
	case "deleted":
		return "D"
	}
	return "M"
}

// statusTint colors one file row's status letter.
func statusTint(p palette, status string) ui.Color {
	switch status {
	case "added":
		return p.diffAdd
	case "deleted":
		return p.diffDel
	}
	return p.orange
}
