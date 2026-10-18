package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
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
	loading  bool
	dir      string
	mode     string
	info     vcsInfo
	files    []vcsChange
	selected int
	note     string
}

// changesRead is one read of a workspace's changes: the folder and mode it
// answered for, what version control the folder sits in, and the diff.
type changesRead struct {
	dir   string
	mode  string
	info  vcsInfo
	files []vcsChange
}

// readChanges asks a server for one folder's version control and the diff
// of one mode. What it asked for rides along even when it fails, so the
// pane can tell its own read's trouble from another read's.
func readChanges(ctx context.Context, cfg serverConn, dir, mode string) (changesRead, error) {
	read := changesRead{dir: dir, mode: mode}
	ag, err := standaloneAgent(cfg)
	if err != nil {
		return read, err
	}
	info, err := ag.workspaceVcs(ctx, dir)
	if err != nil {
		return read, err
	}
	files, err := ag.workspaceChanges(ctx, dir, mode)
	if err != nil {
		return read, err
	}
	read.info, read.files = info, files
	return read, nil
}

// changesMode is the read the Changes tool is on, the working tree until
// another one was chosen.
func (a *app) changesMode() string {
	if a.changes != nil && a.changes.mode != "" {
		return a.changes.mode
	}
	return changesWorking
}

// openChanges reads the open chat's folder into the Changes tool, or
// re-reads it in another mode. A remote chat without a folder has nothing
// to compare — and naming this machine's folder to the server would file
// the server a project it does not have.
func (a *app) openChanges(mode string) {
	st := a.changes
	if st == nil {
		st = &changesState{}
		a.changes = st
	}
	dir := a.chatDir()
	if dir == "" {
		if !a.localActive() {
			st.loading, st.note = false, "This chat has no folder on the server yet, so there is nothing to compare."
			return
		}
		dir = a.home
	}
	st.loading, st.note, st.dir, st.mode = true, "", dir, mode
	st.files, st.selected = nil, 0
	cfg := a.activeServerConn()
	inBackground(a, func() (changesRead, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return readChanges(ctx, cfg, dir, mode)
	}, func(read changesRead, err error) {
		if a.changes != st || st.dir != read.dir || st.mode != read.mode {
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
	st := a.changes
	if st == nil {
		a.openChanges(changesWorking)
		return
	}
	if st.loading {
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.muted)
		return
	}
	if st.note != "" {
		ui.Text(c, st.note).FontSize(12.5).LineHeight(1.45).TextColor(p.muted)
		return
	}

	label := st.info.provider
	if st.info.branch != "" {
		label += " · " + st.info.branch
	}
	if st.mode == changesBranch && st.info.base != "" {
		label += " against " + st.info.base
	}
	added, removed := 0, 0
	for _, f := range st.files {
		added, removed = added+f.additions, removed+f.deletions
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
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
		a.modePill(c, p, "Uncommitted", changesWorking, st.mode == changesWorking)
		a.modePill(c, p, "Branch", changesBranch, st.mode == changesBranch)
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
			c.Key("change:" + f.file)
			b := ui.ButtonBase(c).Label("Show "+f.file).Height(26).Radius(6).
				Padding(0, 8).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.hover)
			} else if i == st.selected {
				b.Background(p.selected)
			}
			b.OnClick(func() { st.selected = i })
			b.Children(func() {
				ui.Text(c, statusLetter(f.status)).Font("monospace").FontSize(11.5).
					TextColor(statusTint(p, f.status)).Width(14).Shrink(0)
				ui.Text(c, f.file).FontSize(12.5).TextColor(p.text).MarginStart(6).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				for _, part := range changesCounts(p, f.additions, f.deletions) {
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
	ui.ScrollBoth(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).Gap(0).Children(func() {
			// Every line rides its own strip, the way a diff reads in a
			// terminal: the pane's own color where nothing changed. Rows
			// past the window cost nothing, so the whole patch stands.
			for _, line := range strings.Split(strings.TrimSuffix(f.patch, "\n"), "\n") {
				ui.Box(c).FillWidth().Shrink(0).Padding(0, 6).
					Background(patchBg(p, line)).Children(func() {
					ui.Text(c, clipLine(line)).Font("monospace").FontSize(11.5).LineHeight(1.5).
						TextColor(patchTint(p, line)).SingleLine().Shrink(0)
				})
			}
		})
	})
}

// modePill is one of the Changes tool's reads, the chosen one standing out.
func (a *app) modePill(c *ui.Context, p palette, label, mode string, chosen bool) {
	c.Key("changes-mode:" + mode)
	b := ui.ButtonBase(c).Label(label + " changes").Height(24).Radius(999).PaddingX(10).Center()
	if chosen {
		b.Background(p.selected)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.openChanges(mode) })
	b.Children(func() {
		tint := p.muted
		if chosen {
			tint = p.text
		}
		ui.Text(c, label).FontSize(12).TextColor(tint).SingleLine()
	})
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

// patchBg is the strip one line of a patch rides: the adding and removing
// lines carry the pane's own washes, a hunk its quiet band, and everything
// else the pane's own color.
func patchBg(p palette, line string) ui.Color {
	switch {
	case strings.HasPrefix(line, "@@"):
		return p.diffHunkBg
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return p.bg
	case strings.HasPrefix(line, "+"):
		return p.diffAddBg
	case strings.HasPrefix(line, "-"):
		return p.diffDelBg
	}
	return p.bg
}

// patchTint colors one line of a patch: the adding and removing lines
// carry the pane's greens and reds, the hunks and headers read quiet.
func patchTint(p palette, line string) ui.Color {
	switch {
	case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			return p.muted
		}
		if strings.HasPrefix(line, "+") {
			return p.diffAdd
		}
		return p.diffDel
	case strings.HasPrefix(line, "@@"):
		return p.link
	case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "rename "):
		return p.faint
	}
	return p.muted
}
