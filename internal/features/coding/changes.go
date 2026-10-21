package coding

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
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

// The two reads the Changes tool offers.
const (
	changesWorking = "working" // what the workspace has not committed yet
	changesBranch  = "branch"  // everything the branch holds beyond its base
)

// changesState is what the Changes tool holds: the folder it compared, the
// mode it compared in, the files that came back and which one's patch is
// on show.
type ChangesState struct {
	Request feature.Request

	Loading  bool
	Dir      string
	Mode     string
	Info     workspace.VCSInfo
	Files    []workspace.Change
	Selected int
	Note     string

	// Keep the selected patch's buffers and selection across frames.
	PatchFile, Patch string
	Diff             *editor.DiffState
	Hunk             int

	// review holds the notes written on the patches, kept while another
	// file is on show until they join the message or are discarded.
	Review      []*ReviewNote
	NextReview  uint64
	ReviewFocus uint64

	// viewed marks the files a review has already read, and hideViewed
	// narrows the list to the rest.
	Viewed     map[string]bool
	HideViewed bool
}

// reviewNote is one comment on a line of a patch: the file and side it
// belongs to, the line, and the words the user is writing.
type ReviewNote struct {
	Id    uint64
	File  string
	Side  editor.DiffSide
	Line  int
	Draft string
}

// changesRead is one read of a workspace's changes: the folder and mode it
// answered for, what version control the folder sits in, and the diff.
type ChangesRead struct {
	Dir   string
	Mode  string
	Info  workspace.VCSInfo
	Files []workspace.Change
}

// readChanges asks a server for one folder's version control and the diff
// of one mode. What it asked for rides along even when it fails, so the
// pane can tell its own read's trouble from another read's.
func readChanges(ctx context.Context, cfg workspace.Client, dir, mode string) (ChangesRead, error) {
	read := ChangesRead{Dir: dir, Mode: mode}
	ag := cfg
	if ag == nil {
		return read, fmt.Errorf("this backend does not provide workspace changes")
	}
	info, err := ag.WorkspaceVcs(ctx, dir)
	if err != nil {
		return read, err
	}
	files, err := ag.WorkspaceChanges(ctx, dir, mode)
	if err != nil {
		return read, err
	}
	read.Info, read.Files = info, files
	return read, nil
}

// changesMode is the read the Changes tool is on, the working tree until
// another one was chosen.
func (a *Module) ChangesMode() string {
	if a.Changes != nil && a.Changes.Mode != "" {
		return a.Changes.Mode
	}
	return changesWorking
}

// openChanges reads the open chat's folder into the Changes tool, or
// re-reads it in another mode. A remote chat without a folder has nothing
// to compare — and naming this machine's folder to the server would file
// the server a project it does not have.
func (a *Module) OpenChanges(mode string) {
	st := a.Changes
	if st == nil {
		st = &ChangesState{}
		a.Changes = st
	}
	dir := a.host.Context().Directory
	if dir == "" {
		if !a.host.Context().Local {
			st.Loading, st.Note = false, "This chat has no folder on the server yet, so there is nothing to compare."
			return
		}
		dir = a.host.Context().Home
	}
	if st.Mode != mode || st.Dir != dir {
		st.Viewed, st.HideViewed = nil, false
	}
	st.Loading, st.Note, st.Dir, st.Mode = true, "", dir, mode
	st.Files, st.Selected = nil, 0
	cfg := a.host.Context().Workspace
	feature.Run(a.host, &st.Request, func(ctx context.Context) (ChangesRead, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return readChanges(ctx, cfg, dir, mode)
	}, func(read ChangesRead, err error) {
		if a.Changes != st || st.Dir != read.Dir || st.Mode != read.Mode {
			return // another read moved on
		}
		st.Loading = false
		if err != nil {
			st.Note = "The changes did not load: " + err.Error()
			return
		}
		st.Info, st.Files = read.Info, read.Files
		// A mark belongs to a file still in the diff; one that left it
		// has nothing left to look at.
		for file := range st.Viewed {
			if !slices.ContainsFunc(read.Files, func(f workspace.Change) bool { return f.File == file }) {
				delete(st.Viewed, file)
			}
		}
	})
}

// changesBody draws the Changes tool: what the workspace sits in, the two
// modes, the files touched with their counts, and the patch of the file on
// show.
func (a *Module) ChangesBody(c *ui.Context, p native.Palette) {
	st := a.Changes
	if st == nil {
		return
	}
	if st.Loading {
		ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.Muted)
		components.Skeleton(c, components.SkeletonOptions{Lines: 5, Label: "Reading the workspace"})
		return
	}
	if st.Note != "" {
		ui.Text(c, st.Note).FontSize(12.5).LineHeight(1.45).TextColor(p.Muted)
		return
	}

	label := st.Info.Provider
	if st.Info.Branch != "" {
		label += " · " + st.Info.Branch
	}
	if st.Mode == changesBranch && st.Info.Base != "" {
		label += " against " + st.Info.Base
	}
	added, removed := 0, 0
	for _, f := range st.Files {
		added, removed = added+f.Additions, removed+f.Deletions
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.Faint).SingleLine().Shrink(1).MinWidth(0)
		ui.Spacer(c)
		if len(st.Files) > 0 {
			for _, part := range changesCounts(p, added, removed) {
				ui.Text(c, part.Text).Font("monospace").FontSize(11).TextColor(part.Tint).Shrink(0)
			}
		}
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		// The two reads share a track, as Onevra's segmented tabs do, so
		// the chosen one stands on the content surface.
		ui.Row(c).Shrink(0).Gap(2).Padding(2).Radius(8).Background(p.Hover).Children(func() {
			a.ModePill(c, p, "Uncommitted", changesWorking, st.Mode == changesWorking)
			a.ModePill(c, p, "Branch", changesBranch, st.Mode == changesBranch)
		})
		ui.Spacer(c)
		if viewed := a.ViewedChangeCount(); viewed > 0 {
			ui.Text(c, fmt.Sprintf("%d viewed", viewed)).FontSize(12).TextColor(p.Faint).SingleLine()
			label := "Hide viewed"
			if st.HideViewed {
				label = "Show viewed"
			}
			native.LinkButton(c, p, "Toggle viewed files", label, a.ToggleHideViewed)
		}
		ui.Text(c, pluralFiles(len(st.Files))).FontSize(12).TextColor(p.Faint).SingleLine()
	})

	if len(st.Files) == 0 {
		ui.Text(c, "Nothing changed here.").FontSize(12.5).TextColor(p.Muted)
		return
	}
	shown := 0
	ui.Scroll(c).FillWidth().MaxHeight(190).Gap(1).Children(func() {
		for i, f := range st.Files {
			i, f := i, f
			viewed := st.Viewed[f.File]
			if st.HideViewed && viewed {
				continue
			}
			shown++
			c.Key("change:" + f.File)
			b := ui.ButtonBase(c).Label("Show "+f.File).Height(26).Radius(6).
				Padding(0, 8).Justify(ui.Start).FillWidth()
			if b.Hovered() {
				b.Background(p.Hover)
			} else if i == st.Selected {
				b.Background(p.Selected)
			}
			b.OnClick(func() { st.Selected = i })
			b.Children(func() {
				ui.Text(c, StatusLetter(f.Status)).Font("monospace").FontSize(11.5).
					TextColor(statusTint(p, f.Status)).Width(14).Shrink(0)
				ui.Text(c, f.File).FontSize(12.5).TextColor(p.Text).MarginStart(6).
					Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				for _, part := range changesCounts(p, f.Additions, f.Deletions) {
					ui.Text(c, part.Text).Font("monospace").FontSize(11).TextColor(part.Tint).Shrink(0)
				}
				c.Key("viewed:" + f.File)
				label := "Mark " + f.File + " as viewed"
				if viewed {
					label = "Mark " + f.File + " as unviewed"
				}
				check := ui.ButtonBase(c).Label(label).Size(20, 20).Radius(5).Center().Shrink(0)
				check.OnClick(func() { a.ToggleChangeViewed(f.File) })
				if check.Hovered() {
					check.Background(p.Hover)
				}
				check.Children(func() {
					icon, tint := lucide.Circle(), p.Faint
					if viewed {
						icon, tint = lucide.Check(), p.Orange
					}
					ui.Icon(c, icon).FontSize(12).TextColor(tint)
				})
			})
		}
	})
	if st.HideViewed && shown == 0 {
		ui.Text(c, "Every file is marked viewed.").FontSize(12).TextColor(p.Faint)
	}

	if st.Selected >= len(st.Files) {
		st.Selected = 0
	}
	f := st.Files[st.Selected]
	ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
	// The file on show gets its own header: the name the patch belongs to
	// and the reading it is drawn in, so a long list never hides which
	// file the diff below describes.
	ui.Row(c).FillWidth().Height(30).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, f.File).Font("monospace").FontSize(12).FontWeight(500).TextColor(p.Text).
			Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(f.File)
		for _, part := range changesCounts(p, f.Additions, f.Deletions) {
			ui.Text(c, part.Text).Font("monospace").FontSize(11).TextColor(part.Tint).Shrink(0)
		}
		a.PreviewChangeButton(c, p, f.File)
		a.ChangeFileMenuButton(c, p, f.File)
		a.DiffHunkControl(c, p, f.File)
		a.DiffStyleControl(c, p)
	})
	layout := a.DiffLayout()
	if st.Diff == nil || st.PatchFile != f.File || st.Patch != f.Patch || st.Diff.Layout != layout {
		if st.PatchFile != f.File || st.Patch != f.Patch {
			st.Hunk = 0
		}
		st.PatchFile, st.Patch = f.File, f.Patch
		st.Diff = &editor.DiffState{Layout: layout, Indicators: editor.DiffClassic,
			Font: ui.Font{Family: "monospace", Size: 11.5}, LineHeight: 1.5}
		if patches, err := editor.ParsePatch(f.Patch); err == nil && len(patches) == 1 && len(patches[0].Hunks) > 0 {
			st.Diff.SetPatch(patches[0])
			lang := editor.LanguageFor(f.File)
			st.Diff.Old.SetLanguage(lang)
			st.Diff.New.SetLanguage(lang)
		}
	}
	if st.Diff.New == nil {
		// Binary, metadata-only and malformed patches still show their raw text.
		ui.TextAreaBase(c, &st.Patch).Label("Raw patch").ReadOnly(true).
			Font("monospace").FontSize(11.5).LineHeight(1.5).FillWidth().Grow(1)
	} else {
		// The gutter's + opens a review note on its line, and the notes on
		// this file stand under the lines they belong to.
		st.Diff.OnGutterButton = func(r editor.DiffLineRange) { a.BeginReviewNote(f.File, r) }
		notes := a.ReviewNotesFor(f.File)
		annotations := make([]editor.DiffAnnotation, 0, len(notes))
		for _, note := range notes {
			annotations = append(annotations, editor.DiffAnnotation{
				Side: note.Side, Line: note.Line, Height: 66, Key: note.Id,
			})
		}
		st.Diff.Annotations = annotations
		st.Diff.RenderAnnotation = func(c *ui.Context, ann editor.DiffAnnotation) {
			if id, ok := ann.Key.(uint64); ok {
				a.ReviewNoteView(c, p, id)
			}
		}
		st.Diff.Theme = &editor.Theme{Background: p.Bg, Text: p.Muted,
			LineNumber: p.Faint, Added: p.DiffAdd, Deleted: p.DiffDel, Modified: p.Link}
		editor.DiffView(c.Key("patch:"+f.File), st.Diff).FillWidth().Grow(1)
	}
	if len(st.Review) > 0 {
		a.ReviewTray(c, p)
	}
}

// beginReviewNote opens a note on one line of the selected patch, or
// brings the note already there back to the keyboard.
func (a *Module) BeginReviewNote(file string, r editor.DiffLineRange) {
	st := a.Changes
	if st == nil || file == "" {
		return
	}
	for _, note := range st.Review {
		if note.File == file && note.Side == r.Side && note.Line == r.Start {
			st.ReviewFocus = note.Id
			return
		}
	}
	st.NextReview++
	note := &ReviewNote{Id: st.NextReview, File: file, Side: r.Side, Line: r.Start}
	st.Review = append(st.Review, note)
	st.ReviewFocus = note.Id
}

// reviewNotesFor lists the notes on one file, oldest first.
func (a *Module) ReviewNotesFor(file string) []*ReviewNote {
	st := a.Changes
	if st == nil {
		return nil
	}
	out := make([]*ReviewNote, 0, len(st.Review))
	for _, note := range st.Review {
		if note.File == file {
			out = append(out, note)
		}
	}
	return out
}

// removeReviewNote discards one note.
func (a *Module) RemoveReviewNote(id uint64) {
	st := a.Changes
	if st == nil {
		return
	}
	for i, note := range st.Review {
		if note.Id == id {
			st.Review = append(st.Review[:i], st.Review[i+1:]...)
			if st.ReviewFocus == id {
				st.ReviewFocus = 0
			}
			return
		}
	}
}

// addReviewNote writes one note into the composer as a sentence the agent
// reads with the rest of the message, then puts the keyboard there.
func (a *Module) AddReviewNote(note *ReviewNote) {
	if note == nil {
		return
	}
	text := strings.TrimSpace(note.Draft)
	if text == "" {
		return
	}
	entry := fmt.Sprintf("In %s at line %d: %s", note.File, note.Line+1, text)
	draft := strings.TrimRight(a.host.Draft(), "\n")
	if draft != "" {
		draft += "\n\n"
	}
	a.host.SetDraft(draft + entry + "\n")
	a.RemoveReviewNote(note.Id)

}

// reviewNoteView draws the note editor an annotation makes room for: the
// line it belongs to, the words, and the actions that add or drop it.
func (a *Module) ReviewNoteView(c *ui.Context, p native.Palette, id uint64) {
	st := a.Changes
	if st == nil {
		return
	}
	var note *ReviewNote
	for _, candidate := range st.Review {
		if candidate.Id == id {
			note = candidate
			break
		}
	}
	if note == nil {
		return
	}
	c.Key("review-note:" + strconv.FormatUint(note.Id, 10))
	ui.Column(c).FillWidth().Padding(5, 8).Gap(4).Radius(8).
		Background(p.DetailBg).Border(1, p.Hairline).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, lucide.TextQuote()).FontSize(11).TextColor(p.Orange).Shrink(0)
			ui.Text(c, fmt.Sprintf("%s:%d", note.File, note.Line+1)).Font("monospace").
				FontSize(10.5).TextColor(p.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			discard := ui.ButtonBase(c).Label("Discard the note").Size(18, 18).Radius(4).Center().
				Tooltip("Discard the note")
			if discard.Hovered() {
				discard.Background(p.Hover)
			}
			discard.OnClick(func() { a.RemoveReviewNote(note.Id) })
			discard.Children(func() { ui.Icon(c, lucide.X()).FontSize(10).TextColor(p.Faint) })
		})
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			in := components.TextBox(c, &note.Draft, components.TextBoxOptions{}).Grow(1).MinWidth(0).
				Label("Review note").Placeholder("Ask for a change").FontSize(12).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter {
						return false
					}
					a.AddReviewNote(note)
					return true
				})
			if st.ReviewFocus == note.Id {
				in.Focus()
				st.ReviewFocus = 0
			}
			add := components.Button(c, "Add to message", components.ButtonSecondary).Shrink(0)
			add.Disabled(strings.TrimSpace(note.Draft) == "")
			if add.Clicked() {
				a.AddReviewNote(note)
			}
		})
	})
}

// viewedChangeCount counts the marks on files still in the diff.
func (a *Module) ViewedChangeCount() int {
	st := a.Changes
	if st == nil {
		return 0
	}
	n := 0
	for _, f := range st.Files {
		if st.Viewed[f.File] {
			n++
		}
	}
	return n
}

// toggleChangeViewed marks or unmarks one file. Marking the file on show
// moves to the next unread one, as a review pass runs.
func (a *Module) ToggleChangeViewed(file string) {
	st := a.Changes
	if st == nil {
		return
	}
	if st.Viewed == nil {
		st.Viewed = map[string]bool{}
	}
	st.Viewed[file] = !st.Viewed[file]
	if !st.Viewed[file] || len(st.Files) == 0 {
		return
	}
	if st.Files[st.Selected].File == file {
		if next := a.NextUnviewedChange(st.Selected); next >= 0 {
			st.Selected = next
		}
	}
}

// nextUnviewedChange finds the next file without a mark, wrapping once.
func (a *Module) NextUnviewedChange(from int) int {
	st := a.Changes
	if st == nil || len(st.Files) == 0 {
		return -1
	}
	for step := 1; step <= len(st.Files); step++ {
		i := (from + step) % len(st.Files)
		if !st.Viewed[st.Files[i].File] {
			return i
		}
	}
	return -1
}

// toggleHideViewed narrows the file list to the unread ones, moving off a
// file that would hide itself.
func (a *Module) ToggleHideViewed() {
	st := a.Changes
	if st == nil {
		return
	}
	st.HideViewed = !st.HideViewed
	if !st.HideViewed || len(st.Files) == 0 || !st.Viewed[st.Files[st.Selected].File] {
		return
	}
	if next := a.NextUnviewedChange(st.Selected); next >= 0 {
		st.Selected = next
	}
}

// addAllReviewNotes writes every note into the composer, in file and line
// order, and clears the tray.
func (a *Module) AddAllReviewNotes() {
	st := a.Changes
	if st == nil || len(st.Review) == 0 {
		return
	}
	notes := slices.Clone(st.Review)
	slices.SortStableFunc(notes, func(one, two *ReviewNote) int {
		if one.File != two.File {
			return strings.Compare(one.File, two.File)
		}
		if one.Line != two.Line {
			return one.Line - two.Line
		}
		return int(one.Id) - int(two.Id)
	})
	entries := make([]string, 0, len(notes))
	for _, note := range notes {
		text := strings.TrimSpace(note.Draft)
		if text == "" {
			continue
		}
		entries = append(entries, fmt.Sprintf("In %s at line %d: %s", note.File, note.Line+1, text))
	}
	if len(entries) > 0 {
		draft := strings.TrimRight(a.host.Draft(), "\n")
		if draft != "" {
			draft += "\n\n"
		}
		a.host.SetDraft(draft + strings.Join(entries, "\n\n") + "\n")
	}
	st.Review = nil

}

// reviewTray is the bar under the patch while notes are waiting: how many
// there are and the action that takes them all to the message.
func (a *Module) ReviewTray(c *ui.Context, p native.Palette) {
	st := a.Changes
	n := len(st.Review)
	label := "1 review note"
	if n > 1 {
		label = fmt.Sprintf("%d review notes", n)
	}
	ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
	ui.Row(c).FillWidth().Height(34).AlignItems(ui.Center).Gap(7).Children(func() {
		ui.Icon(c, lucide.TextQuote()).FontSize(12).TextColor(p.Orange).Shrink(0)
		ui.Text(c, label).FontSize(11.5).TextColor(p.Muted).SingleLine()
		ui.Spacer(c)
		if components.Button(c, "Add all to message", components.ButtonSecondary).Clicked() {
			a.AddAllReviewNotes()
		}
	})
}

// modePill is one of the Changes tool's reads, the chosen one standing out.
func (a *Module) ModePill(c *ui.Context, p native.Palette, label, mode string, chosen bool) {
	c.Key("changes-mode:" + mode)
	b := ui.ButtonBase(c).Label(label + " changes").Height(24).Radius(6).PaddingX(10).Center()
	if chosen {
		b.Background(c.Theme().Background).Border(1, p.Hairline)
	} else if b.Hovered() {
		b.Background(p.Selected)
	}
	b.OnClick(func() { a.OpenChanges(mode) })
	b.Children(func() {
		tint := p.Muted
		if chosen {
			tint = p.Text
		}
		t := ui.Text(c, label).FontSize(12).TextColor(tint).SingleLine()
		if chosen {
			t.FontWeight(500)
		}
	})
}

// diffLayout is the reading the selected file's patch is drawn in: one
// column by default, two when the split was chosen.
func (a *Module) DiffLayout() editor.DiffLayout {
	if a.DiffSplit {
		return editor.DiffSplit
	}
	return editor.DiffUnified
}

// setDiffSplit switches the diff's reading and saves it for the next
// visit, as the pane's own preference.
func (a *Module) SetDiffSplit(split bool) {
	if a.DiffSplit == split {
		return
	}
	a.DiffSplit = split
	a.host.Save()
}

// diffStyleControl is the two-state reading switch in the selected file's
// header: the rows glyph for a unified diff, the columns glyph for a split
// one.
func (a *Module) DiffStyleControl(c *ui.Context, p native.Palette) {
	ui.Row(c).Shrink(0).Gap(2).Padding(2).Radius(8).Background(p.Hover).Children(func() {
		a.DiffStyleButton(c, p, "Unified diff", lucide.Rows3(), false)
		a.DiffStyleButton(c, p, "Split diff", lucide.Columns2(), true)
	})
}

func (a *Module) DiffStyleButton(c *ui.Context, p native.Palette, label string, icon *ui.SVG, split bool) {
	chosen := a.DiffSplit == split
	c.Key("diff-style:" + label)
	b := ui.ButtonBase(c).Label(label).Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip(label)
	if chosen {
		b.Background(c.Theme().Background).Border(1, p.Hairline)
	} else if b.Hovered() {
		b.Background(p.Selected)
	}
	b.OnClick(func() { a.SetDiffSplit(split) })
	tint := p.Muted
	if chosen {
		tint = p.Text
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(tint) })
}

// previewChangeButton hands the selected file to the Files tool, so the
// patch and the whole file sit one click apart; the Changes tab stays in
// the strip.
func (a *Module) PreviewChangeButton(c *ui.Context, p native.Palette, path string) {
	c.Key("preview-change:" + path)
	b := ui.ButtonBase(c).Label("Preview the selected file").Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip("Preview in Files")
	if b.Hovered() {
		b.Background(p.Selected)
	}
	b.OnClick(func() { a.PreviewChangedFile(path) })
	b.Children(func() { ui.Icon(c, lucide.FileText()).FontSize(14).TextColor(p.Muted) })
}

// changeFileMenuButton gathers the desktop actions for one changed file:
// preview, the editor, the file manager and the path. The local actions
// only appear for a workspace on this machine.
func (a *Module) ChangeFileMenuButton(c *ui.Context, p native.Palette, file string) {
	c.Key("change-actions:" + file)
	b := ui.ButtonBase(c).Label("File actions for "+file).Size(26, 24).Radius(6).Center().
		Cursor(ui.CursorPointer).Tooltip("File actions")
	if b.Hovered() {
		b.Background(p.Selected)
	}
	b.Menu(func(m *ui.Menu) {
		if m.Item("Preview in Files").Chosen() {
			a.PreviewChangedFile(file)
		}
		if a.host.Context().Local && a.ChangesDir() != "" {
			if m.Item("Open in editor").Chosen() {
				a.OpenChangeFile(c, file)
			}
			if m.Item("Reveal in folder").Chosen() {
				a.RevealChangeFile(c, file)
			}
		}
		m.Separator()
		if m.Item("Copy path").Chosen() {
			a.CopyChangePath(file)
		}
	})
	b.Children(func() { ui.Icon(c, lucide.Ellipsis()).FontSize(14).TextColor(p.Muted) })
}

// changesDir is the folder the diff compares, empty before its first read.
func (a *Module) ChangesDir() string {
	if st := a.Changes; st != nil {
		return st.Dir
	}
	return ""
}

// changePath resolves one changed file against the compared folder.
func (a *Module) ChangePath(file string) string {
	if dir := a.ChangesDir(); dir != "" {
		return filepath.Join(dir, file)
	}
	return file
}

// openChangeFile hands a changed file to this machine's editor.
func (a *Module) OpenChangeFile(c *ui.Context, file string) {
	native.OpenLocalPath(c, a.ChangePath(file))
}

// revealChangeFile shows a changed file's folder in this machine's file
// manager.
func (a *Module) RevealChangeFile(c *ui.Context, file string) {
	native.OpenLocalPath(c, filepath.Dir(a.ChangePath(file)))
}

// copyChangePath puts one changed file's path on the clipboard, absolute
// for a folder on this machine and as the server knows it otherwise.
func (a *Module) CopyChangePath(file string) {
	text := file
	if a.host.Context().Local && a.ChangesDir() != "" {
		text = filepath.Join(a.ChangesDir(), file)
	}
	if err := a.host.Copy(text); err != nil {
		a.host.Notify(ui.Toast{Title: "Could not copy the path", Description: err.Error()})
		return
	}
	a.host.Notify(ui.Toast{Title: "File path copied"})
}

// diffHunkControl walks the diff's changes: previous, next, and where the
// walk stands. It shows once the patch has more than one hunk.
func (a *Module) DiffHunkControl(c *ui.Context, p native.Palette, file string) {
	st := a.Changes
	if st == nil || st.Diff == nil || st.Diff.New == nil || st.PatchFile != file {
		return
	}
	hunks := st.Diff.Hunks()
	if len(hunks) <= 1 {
		return
	}
	st.Hunk = min(max(st.Hunk, 0), len(hunks)-1)
	c.Key("diff-hunks")
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(1).Children(func() {
		a.HunkButton(c, p, "Previous change", lucide.ChevronUp(), st.Hunk == 0, func() {
			st.Hunk--
			st.Diff.GoToHunk(st.Hunk)
		})
		ui.Text(c, fmt.Sprintf("%d/%d", st.Hunk+1, len(hunks))).FontSize(11).
			TextColor(p.Faint).SingleLine()
		a.HunkButton(c, p, "Next change", lucide.ChevronDown(), st.Hunk == len(hunks)-1, func() {
			st.Hunk++
			st.Diff.GoToHunk(st.Hunk)
		})
	})
}

// hunkButton is one step of the hunk walk, disabled at either end.
func (a *Module) HunkButton(c *ui.Context, p native.Palette, label string, icon *ui.SVG, disabled bool, on func()) {
	b := ui.ButtonBase(c).Label(label).Size(22, 22).Radius(5).Center().
		Disabled(disabled).Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() && !disabled {
		b.Background(p.Hover)
	}
	b.OnClick(on)
	b.Children(func() { ui.Icon(c, icon).FontSize(12).TextColor(p.Muted) })
}

// previewChangedFile shows one changed file in the Files tool, growing
// that tool's tree first when it has not run this session.
func (a *Module) PreviewChangedFile(path string) {
	if path == "" {
		return
	}
	a.OpenFiles()
	a.host.OpenPanel("Files")
	a.FilesRead(path)
}

// changesCount is one piece of a file's count line, colored by what it
// counts.
type ChangesCount struct {
	Text string
	Tint ui.Color
}

// changesCounts words what a diff added and removed.
func changesCounts(p native.Palette, added, removed int) []ChangesCount {
	return []ChangesCount{
		{"+" + strconv.Itoa(added), p.DiffAdd},
		{"−" + strconv.Itoa(removed), p.DiffDel},
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
func StatusLetter(status string) string {
	switch status {
	case "added":
		return "A"
	case "deleted":
		return "D"
	}
	return "M"
}

// statusTint colors one file row's status letter.
func statusTint(p native.Palette, status string) ui.Color {
	switch status {
	case "added":
		return p.DiffAdd
	case "deleted":
		return p.DiffDel
	}
	return p.Orange
}
