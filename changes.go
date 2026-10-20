package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	hunk             int

	// review holds the notes written on the patches, kept while another
	// file is on show until they join the message or are discarded.
	review      []*reviewNote
	nextReview  uint64
	reviewFocus uint64

	// viewed marks the files a review has already read, and hideViewed
	// narrows the list to the rest.
	viewed     map[string]bool
	hideViewed bool
}

// reviewNote is one comment on a line of a patch: the file and side it
// belongs to, the line, and the words the user is writing.
type reviewNote struct {
	id    uint64
	file  string
	side  editor.DiffSide
	line  int
	draft string
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
	if st.mode != mode || st.dir != dir {
		st.viewed, st.hideViewed = nil, false
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
		// A mark belongs to a file still in the diff; one that left it
		// has nothing left to look at.
		for file := range st.viewed {
			if !slices.ContainsFunc(read.files, func(f opencode.Change) bool { return f.File == file }) {
				delete(st.viewed, file)
			}
		}
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
		if viewed := a.viewedChangeCount(); viewed > 0 {
			ui.Text(c, fmt.Sprintf("%d viewed", viewed)).FontSize(12).TextColor(p.faint).SingleLine()
			label := "Hide viewed"
			if st.hideViewed {
				label = "Show viewed"
			}
			a.linkButton(c, p, "Toggle viewed files", label, a.toggleHideViewed)
		}
		ui.Text(c, pluralFiles(len(st.files))).FontSize(12).TextColor(p.faint).SingleLine()
	})

	if len(st.files) == 0 {
		ui.Text(c, "Nothing changed here.").FontSize(12.5).TextColor(p.muted)
		return
	}
	shown := 0
	ui.Scroll(c).FillWidth().MaxHeight(190).Gap(1).Children(func() {
		for i, f := range st.files {
			i, f := i, f
			viewed := st.viewed[f.File]
			if st.hideViewed && viewed {
				continue
			}
			shown++
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
				c.Key("viewed:" + f.File)
				label := "Mark " + f.File + " as viewed"
				if viewed {
					label = "Mark " + f.File + " as unviewed"
				}
				check := ui.ButtonBase(c).Label(label).Size(20, 20).Radius(5).Center().Shrink(0)
				check.OnClick(func() { a.toggleChangeViewed(f.File) })
				if check.Hovered() {
					check.Background(p.hover)
				}
				check.Children(func() {
					icon, tint := icCircle, p.faint
					if viewed {
						icon, tint = icCheck, p.orange
					}
					ui.Icon(c, icon).FontSize(12).TextColor(tint)
				})
			})
		}
	})
	if st.hideViewed && shown == 0 {
		ui.Text(c, "Every file is marked viewed.").FontSize(12).TextColor(p.faint)
	}

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
		a.previewChangeButton(c, p, f.File)
		a.changeFileMenuButton(c, p, f.File)
		a.diffHunkControl(c, p, f.File)
		a.diffStyleControl(c, p)
	})
	layout := a.diffLayout()
	if st.diff == nil || st.patchFile != f.File || st.patch != f.Patch || st.diff.Layout != layout {
		if st.patchFile != f.File || st.patch != f.Patch {
			st.hunk = 0
		}
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
	} else {
		// The gutter's + opens a review note on its line, and the notes on
		// this file stand under the lines they belong to.
		st.diff.OnGutterButton = func(r editor.DiffLineRange) { a.beginReviewNote(f.File, r) }
		notes := a.reviewNotesFor(f.File)
		annotations := make([]editor.DiffAnnotation, 0, len(notes))
		for _, note := range notes {
			annotations = append(annotations, editor.DiffAnnotation{
				Side: note.side, Line: note.line, Height: 66, Key: note.id,
			})
		}
		st.diff.Annotations = annotations
		st.diff.RenderAnnotation = func(c *ui.Context, ann editor.DiffAnnotation) {
			if id, ok := ann.Key.(uint64); ok {
				a.reviewNoteView(c, p, id)
			}
		}
		st.diff.Theme = &editor.Theme{Background: p.bg, Text: p.muted,
			LineNumber: p.faint, Added: p.diffAdd, Deleted: p.diffDel, Modified: p.link}
		editor.DiffView(c.Key("patch:"+f.File), st.diff).FillWidth().Grow(1)
	}
	if len(st.review) > 0 {
		a.reviewTray(c, p)
	}
}

// beginReviewNote opens a note on one line of the selected patch, or
// brings the note already there back to the keyboard.
func (a *app) beginReviewNote(file string, r editor.DiffLineRange) {
	st := a.workspace.changes
	if st == nil || file == "" {
		return
	}
	for _, note := range st.review {
		if note.file == file && note.side == r.Side && note.line == r.Start {
			st.reviewFocus = note.id
			return
		}
	}
	st.nextReview++
	note := &reviewNote{id: st.nextReview, file: file, side: r.Side, line: r.Start}
	st.review = append(st.review, note)
	st.reviewFocus = note.id
}

// reviewNotesFor lists the notes on one file, oldest first.
func (a *app) reviewNotesFor(file string) []*reviewNote {
	st := a.workspace.changes
	if st == nil {
		return nil
	}
	out := make([]*reviewNote, 0, len(st.review))
	for _, note := range st.review {
		if note.file == file {
			out = append(out, note)
		}
	}
	return out
}

// removeReviewNote discards one note.
func (a *app) removeReviewNote(id uint64) {
	st := a.workspace.changes
	if st == nil {
		return
	}
	for i, note := range st.review {
		if note.id == id {
			st.review = append(st.review[:i], st.review[i+1:]...)
			if st.reviewFocus == id {
				st.reviewFocus = 0
			}
			return
		}
	}
}

// addReviewNote writes one note into the composer as a sentence the agent
// reads with the rest of the message, then puts the keyboard there.
func (a *app) addReviewNote(note *reviewNote) {
	if note == nil {
		return
	}
	text := strings.TrimSpace(note.draft)
	if text == "" {
		return
	}
	entry := fmt.Sprintf("In %s at line %d: %s", note.file, note.line+1, text)
	draft := strings.TrimRight(a.conversation.draft, "\n")
	if draft != "" {
		draft += "\n\n"
	}
	a.conversation.draft = draft + entry + "\n"
	a.removeReviewNote(note.id)
	a.layout.composerFocus = true
}

// reviewNoteView draws the note editor an annotation makes room for: the
// line it belongs to, the words, and the actions that add or drop it.
func (a *app) reviewNoteView(c *ui.Context, p palette, id uint64) {
	st := a.workspace.changes
	if st == nil {
		return
	}
	var note *reviewNote
	for _, candidate := range st.review {
		if candidate.id == id {
			note = candidate
			break
		}
	}
	if note == nil {
		return
	}
	c.Key("review-note:" + strconv.FormatUint(note.id, 10))
	ui.Column(c).FillWidth().Padding(5, 8).Gap(4).Radius(8).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icQuote).FontSize(11).TextColor(p.orange).Shrink(0)
			ui.Text(c, fmt.Sprintf("%s:%d", note.file, note.line+1)).Font("monospace").
				FontSize(10.5).TextColor(p.muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			discard := ui.ButtonBase(c).Label("Discard the note").Size(18, 18).Radius(4).Center().
				Tooltip("Discard the note")
			if discard.Hovered() {
				discard.Background(p.hover)
			}
			discard.OnClick(func() { a.removeReviewNote(note.id) })
			discard.Children(func() { ui.Icon(c, icClose).FontSize(10).TextColor(p.faint) })
		})
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			in := components.TextBox(c, &note.draft, components.TextBoxOptions{}).Grow(1).MinWidth(0).
				Label("Review note").Placeholder("Ask for a change").FontSize(12).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter {
						return false
					}
					a.addReviewNote(note)
					return true
				})
			if st.reviewFocus == note.id {
				in.Focus()
				st.reviewFocus = 0
			}
			add := components.Button(c, "Add to message", components.ButtonSecondary).Shrink(0)
			add.Disabled(strings.TrimSpace(note.draft) == "")
			if add.Clicked() {
				a.addReviewNote(note)
			}
		})
	})
}

// viewedChangeCount counts the marks on files still in the diff.
func (a *app) viewedChangeCount() int {
	st := a.workspace.changes
	if st == nil {
		return 0
	}
	n := 0
	for _, f := range st.files {
		if st.viewed[f.File] {
			n++
		}
	}
	return n
}

// toggleChangeViewed marks or unmarks one file. Marking the file on show
// moves to the next unread one, as a review pass runs.
func (a *app) toggleChangeViewed(file string) {
	st := a.workspace.changes
	if st == nil {
		return
	}
	if st.viewed == nil {
		st.viewed = map[string]bool{}
	}
	st.viewed[file] = !st.viewed[file]
	if !st.viewed[file] || len(st.files) == 0 {
		return
	}
	if st.files[st.selected].File == file {
		if next := a.nextUnviewedChange(st.selected); next >= 0 {
			st.selected = next
		}
	}
}

// nextUnviewedChange finds the next file without a mark, wrapping once.
func (a *app) nextUnviewedChange(from int) int {
	st := a.workspace.changes
	if st == nil || len(st.files) == 0 {
		return -1
	}
	for step := 1; step <= len(st.files); step++ {
		i := (from + step) % len(st.files)
		if !st.viewed[st.files[i].File] {
			return i
		}
	}
	return -1
}

// toggleHideViewed narrows the file list to the unread ones, moving off a
// file that would hide itself.
func (a *app) toggleHideViewed() {
	st := a.workspace.changes
	if st == nil {
		return
	}
	st.hideViewed = !st.hideViewed
	if !st.hideViewed || len(st.files) == 0 || !st.viewed[st.files[st.selected].File] {
		return
	}
	if next := a.nextUnviewedChange(st.selected); next >= 0 {
		st.selected = next
	}
}

// addAllReviewNotes writes every note into the composer, in file and line
// order, and clears the tray.
func (a *app) addAllReviewNotes() {
	st := a.workspace.changes
	if st == nil || len(st.review) == 0 {
		return
	}
	notes := slices.Clone(st.review)
	slices.SortStableFunc(notes, func(one, two *reviewNote) int {
		if one.file != two.file {
			return strings.Compare(one.file, two.file)
		}
		if one.line != two.line {
			return one.line - two.line
		}
		return int(one.id) - int(two.id)
	})
	entries := make([]string, 0, len(notes))
	for _, note := range notes {
		text := strings.TrimSpace(note.draft)
		if text == "" {
			continue
		}
		entries = append(entries, fmt.Sprintf("In %s at line %d: %s", note.file, note.line+1, text))
	}
	if len(entries) > 0 {
		draft := strings.TrimRight(a.conversation.draft, "\n")
		if draft != "" {
			draft += "\n\n"
		}
		a.conversation.draft = draft + strings.Join(entries, "\n\n") + "\n"
	}
	st.review = nil
	a.layout.composerFocus = true
}

// reviewTray is the bar under the patch while notes are waiting: how many
// there are and the action that takes them all to the message.
func (a *app) reviewTray(c *ui.Context, p palette) {
	st := a.workspace.changes
	n := len(st.review)
	label := "1 review note"
	if n > 1 {
		label = fmt.Sprintf("%d review notes", n)
	}
	ui.Box(c).FillWidth().Height(1).Background(p.hairline)
	ui.Row(c).FillWidth().Height(34).AlignItems(ui.Center).Gap(7).Children(func() {
		ui.Icon(c, icQuote).FontSize(12).TextColor(p.orange).Shrink(0)
		ui.Text(c, label).FontSize(11.5).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		if components.Button(c, "Add all to message", components.ButtonSecondary).Clicked() {
			a.addAllReviewNotes()
		}
	})
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

// previewChangeButton hands the selected file to the Files tool, so the
// patch and the whole file sit one click apart; the Changes tab stays in
// the strip.
func (a *app) previewChangeButton(c *ui.Context, p palette, path string) {
	c.Key("preview-change:" + path)
	b := ui.ButtonBase(c).Label("Preview the selected file").Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip("Preview in Files")
	if b.Hovered() {
		b.Background(p.selected)
	}
	b.OnClick(func() { a.previewChangedFile(path) })
	b.Children(func() { ui.Icon(c, icFileDoc).FontSize(14).TextColor(p.muted) })
}

// changeFileMenuButton gathers the desktop actions for one changed file:
// preview, the editor, the file manager and the path. The local actions
// only appear for a workspace on this machine.
func (a *app) changeFileMenuButton(c *ui.Context, p palette, file string) {
	c.Key("change-actions:" + file)
	b := ui.ButtonBase(c).Label("File actions for "+file).Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip("File actions")
	if b.Hovered() {
		b.Background(p.selected)
	}
	b.Menu(func(m *ui.Menu) {
		if m.Item("Preview in Files").Chosen() {
			a.previewChangedFile(file)
		}
		if a.localActive() && a.changesDir() != "" {
			if m.Item("Open in editor").Chosen() {
				a.openChangeFile(c, file)
			}
			if m.Item("Reveal in folder").Chosen() {
				a.revealChangeFile(c, file)
			}
		}
		m.Separator()
		if m.Item("Copy path").Chosen() {
			a.copyChangePath(file)
		}
	})
	b.Children(func() { ui.Icon(c, icEllipsis).FontSize(14).TextColor(p.muted) })
}

// changesDir is the folder the diff compares, empty before its first read.
func (a *app) changesDir() string {
	if st := a.workspace.changes; st != nil {
		return st.dir
	}
	return ""
}

// changePath resolves one changed file against the compared folder.
func (a *app) changePath(file string) string {
	if dir := a.changesDir(); dir != "" {
		return filepath.Join(dir, file)
	}
	return file
}

// openChangeFile hands a changed file to this machine's editor.
func (a *app) openChangeFile(c *ui.Context, file string) {
	a.openLocalPath(c, a.changePath(file))
}

// revealChangeFile shows a changed file's folder in this machine's file
// manager.
func (a *app) revealChangeFile(c *ui.Context, file string) {
	a.openLocalPath(c, filepath.Dir(a.changePath(file)))
}

// copyChangePath puts one changed file's path on the clipboard, absolute
// for a folder on this machine and as the server knows it otherwise.
func (a *app) copyChangePath(file string) {
	text := file
	if a.localActive() && a.changesDir() != "" {
		text = filepath.Join(a.changesDir(), file)
	}
	if err := a.runtime.copy(text); err != nil {
		a.notify(ui.Toast{Title: "Could not copy the path", Description: err.Error()})
		return
	}
	a.notify(ui.Toast{Title: "File path copied"})
}

// diffHunkControl walks the diff's changes: previous, next, and where the
// walk stands. It shows once the patch has more than one hunk.
func (a *app) diffHunkControl(c *ui.Context, p palette, file string) {
	st := a.workspace.changes
	if st == nil || st.diff == nil || st.diff.New == nil || st.patchFile != file {
		return
	}
	hunks := st.diff.Hunks()
	if len(hunks) <= 1 {
		return
	}
	st.hunk = min(max(st.hunk, 0), len(hunks)-1)
	c.Key("diff-hunks")
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(1).Children(func() {
		a.hunkButton(c, p, "Previous change", icChevUp, st.hunk == 0, func() {
			st.hunk--
			st.diff.GoToHunk(st.hunk)
		})
		ui.Text(c, fmt.Sprintf("%d/%d", st.hunk+1, len(hunks))).FontSize(11).
			TextColor(p.faint).SingleLine()
		a.hunkButton(c, p, "Next change", icChevDown, st.hunk == len(hunks)-1, func() {
			st.hunk++
			st.diff.GoToHunk(st.hunk)
		})
	})
}

// hunkButton is one step of the hunk walk, disabled at either end.
func (a *app) hunkButton(c *ui.Context, p palette, label string, icon *ui.SVG, disabled bool, on func()) {
	b := ui.ButtonBase(c).Label(label).Size(22, 22).Radius(5).Center().
		Disabled(disabled).Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() && !disabled {
		b.Background(p.hover)
	}
	b.OnClick(on)
	b.Children(func() { ui.Icon(c, icon).FontSize(12).TextColor(p.muted) })
}

// previewChangedFile shows one changed file in the Files tool, growing
// that tool's tree first when it has not run this session.
func (a *app) previewChangedFile(path string) {
	if path == "" {
		return
	}
	a.openFiles()
	a.openTool("Files")
	a.filesRead(path)
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
