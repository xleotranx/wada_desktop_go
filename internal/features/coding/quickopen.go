package coding

import (
	"fmt"
	"path/filepath"
	"sort"

	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// openQuickOpen shows the centered file picker. It reads from the Files
// tool's state, so choosing a result lands in the pane with its tree and
// viewer already loaded, but the pane itself stays folded while searching.
func (a *Module) OpenQuickOpen() {
	a.host.Reveal()
	a.OpenFiles()
	st := a.Files
	if st == nil {
		return
	}
	st.FindRequest.Stop()
	st.Query, st.Results, st.FindErr, st.Finding = "", nil, "", false
	a.QuickOpen = true
	a.QuickFocus = true
	a.QuickSelected = 0
	a.QuickList.ScrollTo(0, ui.Start)
}

// quickOpenEntries is what the picker lists: the search's matches, or the
// files at the workspace root before anything is typed.
func (a *Module) QuickOpenEntries(st *FilesState) []workspace.FileEntry {
	if st.Query != "" {
		return st.Results
	}
	if recent := a.RecentFilesFor(st.Location); len(recent) > 0 {
		return recent
	}
	out := make([]workspace.FileEntry, 0, len(st.Entries["."]))
	for _, entry := range st.Entries["."] {
		if !entry.Directory {
			out = append(out, entry)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > filesFound {
		out = out[:filesFound]
	}
	return out
}

// openQuickFile closes the picker and shows the chosen file in the Files
// tool. The query is cleared so the pane opens on the tree, not a search.
func (a *Module) OpenQuickFile(path string) {
	a.QuickOpen = false
	if st := a.Files; st != nil {
		st.FindRequest.Stop()
		st.Query, st.Results, st.FindErr, st.Finding = "", nil, "", false
	}
	a.host.OpenPanel("Files")
	a.FilesRead(path)
}

func (a *Module) QuickOpenDialog(c *ui.Context, p native.Palette) {
	if !a.QuickOpen {
		return
	}
	st := a.Files
	if st == nil {
		return
	}
	native.Dialog(c, &a.QuickOpen, 600, "Open file", func() {
		in := components.TextBox(c, &st.Query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).
			FillWidth().Label("Search files").Placeholder("Search files by name").
			FontSize(13.5).OnTextKey(func(k ui.TextKey) bool {
			entries := a.QuickOpenEntries(st)
			if k.Key == ui.KeyEnter {
				if len(entries) > 0 {
					index := min(max(a.QuickSelected, 0), len(entries)-1)
					a.OpenQuickFile(entries[index].Path)
				}
				return true
			}
			if k.Key == ui.KeyDown {
				if len(entries) > 0 {
					a.QuickSelected = min(a.QuickSelected+1, len(entries)-1)
					a.QuickList.ScrollTo(a.QuickSelected, ui.Center)
				}
				return true
			}
			if k.Key == ui.KeyUp {
				if len(entries) > 0 {
					a.QuickSelected = max(a.QuickSelected-1, 0)
					a.QuickList.ScrollTo(a.QuickSelected, ui.Center)
				}
				return true
			}
			return false
		})
		if in.Changed() {
			st.Results, st.FindErr, st.Finding = nil, "", false
			a.QuickSelected = 0
			a.FilesFind()
		}
		if a.QuickFocus {
			in.Focus()
			a.QuickFocus = false
		}

		entries := a.QuickOpenEntries(st)
		if a.QuickSelected >= len(entries) {
			a.QuickSelected = max(0, len(entries)-1)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			label := "Workspace files"
			if st.Query != "" {
				label = "Matches"
			} else if len(a.RecentFilesFor(st.Location)) > 0 {
				label = "Recent files"
			}
			ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(p.Muted).
				SingleLine()
			ui.Spacer(c)
			if st.Finding {
				ui.Spinner(c).Size(11, 11).Label("Searching files")
			} else if len(entries) > 0 {
				ui.Text(c, fmt.Sprintf("%d", len(entries))).FontSize(11.5).
					TextColor(p.Faint)
			}
		})
		switch {
		case st.Note != "":
			ui.Text(c, st.Note).FontSize(12.5).LineHeight(1.45).TextColor(p.Muted)
		case st.FindErr != "":
			ui.Text(c, st.FindErr).FontSize(12.5).LineHeight(1.45).TextColor(p.Danger)
		case st.Query == "" && len(st.Entries["."]) == 0 && !st.Loading["."]:
			ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.Muted)
		case len(entries) == 0 && !st.Finding:
			text := "No files match " + st.Query + "."
			if st.Query == "" {
				text = "No files at the workspace root."
			}
			ui.Text(c, text).FontSize(12.5).TextColor(p.Muted)
		default:
			ui.List(c, &a.QuickList, len(entries)).MaxHeight(330).FillWidth().Gap(2).
				ItemKey(func(i int) any { return entries[i].Path }).Rows(func(row ui.ListRow) {
				a.QuickOpenRow(row.Context, p, entries[row.Index], row.Index == a.QuickSelected)
			})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, "↑↓ choose · Enter opens · type to search all files").FontSize(11).TextColor(p.Faint).
				SingleLine()
			ui.Spacer(c)
			ui.Text(c, "Esc closes").FontSize(11).TextColor(p.Faint).SingleLine()
		})
	})
}

func (a *Module) QuickOpenRow(c *ui.Context, p native.Palette, entry workspace.FileEntry, selected bool) {
	c.Key("quickopen:" + entry.Path)
	b := ui.ButtonBase(c).Label("Open "+entry.Path).Height(36).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer)
	if selected {
		b.Background(p.Selected)
	}
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.OpenQuickFile(entry.Path) })
	b.Children(func() {
		ui.Icon(c, lucide.FileText()).FontSize(13).TextColor(p.Muted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Start).Children(func() {
			ui.Text(c, filepath.Base(entry.Path)).FontSize(12.5).TextColor(p.Text).
				SingleLine().Ellipsis("…")
			if dir := filepath.Dir(entry.Path); dir != "." && dir != "" {
				ui.Text(c, dir).FontSize(10.5).TextColor(p.Faint).
					SingleLine().Ellipsis("…")
			}
		})
	})
}
