package main

import (
	"fmt"
	"sort"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// openQuickOpen shows the centered file picker. It reads from the Files
// tool's state, so choosing a result lands in the pane with its tree and
// viewer already loaded, but the pane itself stays folded while searching.
func (a *app) openQuickOpen() {
	a.leaveFullView(false)
	a.layout.settingsOpen = false
	a.openFiles()
	st := a.workspace.files
	if st == nil {
		return
	}
	st.findRequest.stop()
	st.query, st.results, st.findErr, st.finding = "", nil, "", false
	a.layout.quickOpen = true
	a.layout.quickFocus = true
	a.layout.quickSelected = 0
	a.layout.quickList.ScrollTo(0, ui.Start)
}

// quickOpenEntries is what the picker lists: the search's matches, or the
// files at the workspace root before anything is typed.
func (a *app) quickOpenEntries(st *filesState) []opencode.FileEntry {
	if st.query != "" {
		return st.results
	}
	out := make([]opencode.FileEntry, 0, len(st.entries["."]))
	for _, entry := range st.entries["."] {
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
func (a *app) openQuickFile(path string) {
	a.layout.quickOpen = false
	if st := a.workspace.files; st != nil {
		st.findRequest.stop()
		st.query, st.results, st.findErr, st.finding = "", nil, "", false
	}
	a.openTool("Files")
	a.filesRead(path)
}

func (a *app) quickOpenDialog(c *ui.Context, p palette) {
	if !a.layout.quickOpen {
		return
	}
	st := a.workspace.files
	if st == nil {
		return
	}
	dialog(c, p, &a.layout.quickOpen, 600, "Open file", func() {
		in := components.TextBox(c, &st.query, components.TextBoxOptions{LeadingIcon: icSearch}).
			FillWidth().Label("Search files").Placeholder("Search files by name").
			FontSize(13.5).OnTextKey(func(k ui.TextKey) bool {
			entries := a.quickOpenEntries(st)
			if k.Key == ui.KeyEnter {
				if len(entries) > 0 {
					index := min(max(a.layout.quickSelected, 0), len(entries)-1)
					a.openQuickFile(entries[index].Path)
				}
				return true
			}
			if k.Key == ui.KeyDown {
				if len(entries) > 0 {
					a.layout.quickSelected = min(a.layout.quickSelected+1, len(entries)-1)
					a.layout.quickList.ScrollTo(a.layout.quickSelected, ui.Center)
				}
				return true
			}
			if k.Key == ui.KeyUp {
				if len(entries) > 0 {
					a.layout.quickSelected = max(a.layout.quickSelected-1, 0)
					a.layout.quickList.ScrollTo(a.layout.quickSelected, ui.Center)
				}
				return true
			}
			return false
		})
		if in.Changed() {
			st.results, st.findErr, st.finding = nil, "", false
			a.layout.quickSelected = 0
			a.filesFind()
		}
		if a.layout.quickFocus {
			in.Focus()
			a.layout.quickFocus = false
		}

		entries := a.quickOpenEntries(st)
		if a.layout.quickSelected >= len(entries) {
			a.layout.quickSelected = max(0, len(entries)-1)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			label := "Workspace files"
			if st.query != "" {
				label = "Matches"
			}
			ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(p.muted).
				SingleLine()
			ui.Spacer(c)
			if st.finding {
				ui.Spinner(c).Size(11, 11).Label("Searching files")
			} else if len(entries) > 0 {
				ui.Text(c, fmt.Sprintf("%d", len(entries))).FontSize(11.5).
					TextColor(p.faint)
			}
		})
		switch {
		case st.note != "":
			ui.Text(c, st.note).FontSize(12.5).LineHeight(1.45).TextColor(p.muted)
		case st.findErr != "":
			ui.Text(c, st.findErr).FontSize(12.5).LineHeight(1.45).TextColor(p.danger)
		case st.query == "" && len(st.entries["."]) == 0 && !st.loading["."]:
			ui.Text(c, "Reading the workspace…").FontSize(12.5).TextColor(p.muted)
		case len(entries) == 0 && !st.finding:
			text := "No files match " + st.query + "."
			if st.query == "" {
				text = "No files at the workspace root."
			}
			ui.Text(c, text).FontSize(12.5).TextColor(p.muted)
		default:
			ui.List(c, &a.layout.quickList, len(entries)).MaxHeight(330).FillWidth().Gap(2).
				ItemKey(func(i int) any { return entries[i].Path }).Rows(func(row ui.ListRow) {
				a.quickOpenRow(row.Context, p, entries[row.Index], row.Index == a.layout.quickSelected)
			})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, "↑↓ choose · Enter opens").FontSize(11).TextColor(p.faint).
				SingleLine()
			ui.Spacer(c)
			ui.Text(c, "Esc closes").FontSize(11).TextColor(p.faint).SingleLine()
		})
	})
}

func (a *app) quickOpenRow(c *ui.Context, p palette, entry opencode.FileEntry, selected bool) {
	c.Key("quickopen:" + entry.Path)
	b := ui.ButtonBase(c).Label("Open "+entry.Path).Height(30).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer)
	if selected {
		b.Background(p.selected)
	}
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.openQuickFile(entry.Path) })
	b.Children(func() {
		ui.Icon(c, icFileDoc).FontSize(13).TextColor(p.muted).Shrink(0)
		ui.Text(c, entry.Path).FontSize(12.5).TextColor(p.text).
			SingleLine().Ellipsis("…")
	})
}
