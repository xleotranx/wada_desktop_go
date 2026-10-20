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
			if k.Key == ui.KeyEnter && k.Mods == 0 {
				if entries := a.quickOpenEntries(st); len(entries) > 0 {
					a.openQuickFile(entries[0].Path)
				}
				return true
			}
			return false
		})
		if in.Changed() {
			st.results, st.findErr, st.finding = nil, "", false
			a.filesFind()
		}
		if a.layout.quickFocus {
			in.Focus()
			a.layout.quickFocus = false
		}

		entries := a.quickOpenEntries(st)
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
			ui.Scroll(c).FillWidth().MaxHeight(330).Gap(2).Children(func() {
				for _, entry := range entries {
					a.quickOpenRow(c, p, entry)
				}
			})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, "Enter opens the first match").FontSize(11).TextColor(p.faint).
				SingleLine()
			ui.Spacer(c)
			ui.Text(c, "Esc closes").FontSize(11).TextColor(p.faint).SingleLine()
		})
	})
}

func (a *app) quickOpenRow(c *ui.Context, p palette, entry opencode.FileEntry) {
	c.Key("quickopen:" + entry.Path)
	b := ui.ButtonBase(c).Label("Open "+entry.Path).Height(30).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer)
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
