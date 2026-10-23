package filechanges

import (
	"path"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

const patchShown = 400 // lines of one file's diff

// changesView lists the changed files with their line counts; a file opens
// to its diff.
func (p *Plugin) changesView(c *ui.Context) {
	pal := native.PaletteFor(c)
	p.sourcePicker(c, pal)
	if p.mode == uncommittedChanges {
		ui.Text(c, "Local repository · staged, unstaged and untracked").FontSize(11).TextColor(pal.Faint).Padding(0, 0, 6, 0)
	}
	if p.problem != "" {
		ui.Text(c, p.problem).FontSize(12).TextColor(pal.Muted)
	} else if !p.known {
		ui.Text(c, "Loading file changes…").FontSize(12).TextColor(pal.Faint)
		return
	}
	if len(p.changes) == 0 {
		if p.problem == "" {
			empty := "No file changes"
			if p.mode == uncommittedChanges {
				empty = "No uncommitted changes"
			}
			ui.Text(c, empty).FontSize(12).TextColor(pal.Faint)
		}
		return
	}
	added, removed := 0, 0
	for _, ch := range p.changes {
		added, removed = added+ch.Added, removed+ch.Removed
	}
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Padding(0, 0, 6, 0).Children(func() {
			files := strconv.Itoa(len(p.changes)) + " files"
			if len(p.changes) == 1 {
				files = "1 file"
			}
			ui.Text(c, files).FontSize(12.5).TextColor(pal.Text).Grow(1)
			counts(c, pal, added, removed)
		})
		for _, ch := range p.changes {
			c.Key(ch.Path)
			p.file(c, pal, ch)
		}
	})
}

func (p *Plugin) sourcePicker(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Gap(6).Padding(0, 0, 8, 0).Children(func() {
		c.Key("source")
		sel := ui.SelectBase(c, &p.mode)
		sel.Trigger.Label("Change source").Height(28).Grow(1).MinWidth(0).Radius(6).Padding(0, 8).
			Background(pal.Bg).Cursor(ui.CursorPointer).OnChange(p.chooseSource)
		if sel.Trigger.Hovered() {
			sel.Trigger.Background(pal.Hover)
		}
		sel.Trigger.Children(func() {
			ui.Text(c, p.mode.label()).FontSize(12).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(pal.Faint).Shrink(0)
		})
		sel.Popup(func(panel ui.Element) {
			panel.Width(252).Padding(4).Radius(8).Background(pal.Bg).Border(1, pal.Hairline)
			for _, source := range []changeSource{sessionChanges, uncommittedChanges} {
				c.Key(source)
				item := sel.Item(source).Label(source.label()).Height(30).Padding(0, 8).Radius(4)
				if item.Highlighted() {
					item.Background(pal.Hover)
				}
				item.Children(func() { ui.Text(c, source.label()).FontSize(12).TextColor(pal.Text) })
			}
		})
		c.Key("refresh")
		ui.ButtonBase(c).Label("Refresh file changes").Tooltip("Refresh file changes").Size(28, 28).Radius(6).
			Disabled(p.load.Running()).Cursor(ui.CursorPointer).Center().OnClick(p.readChanges).Children(func() {
			ui.Icon(c, lucide.RefreshCw()).FontSize(13).TextColor(pal.Faint)
		})
	})
}

func (p *Plugin) file(c *ui.Context, pal native.Palette, ch agent.FileChange) {
	open := p.open[ch.Path]
	dir, name := path.Split(ch.Path)
	height := float32(28)
	if dir != "" {
		height = 44
	}
	b := ui.ButtonBase(c).Label(ch.Path).FillWidth().Height(height).Radius(6).Padding(4, 6).Gap(6).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.open[ch.Path] = !open })
	b.Children(func() {
		chevron := lucide.ChevronRight
		if open {
			chevron = lucide.ChevronDown
		}
		ui.Icon(c, chevron()).FontSize(11).TextColor(pal.Faint).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				label := ui.Text(c, name).FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				switch ch.Status {
				case agent.FileAdded:
					label.TextColor(pal.DiffAdd)
					ui.Text(c, "Added").FontSize(10.5).TextColor(pal.DiffAdd).Shrink(0)
				case agent.FileDeleted:
					label.TextColor(pal.DiffDel)
					ui.Text(c, "Deleted").FontSize(10.5).TextColor(pal.DiffDel).Shrink(0)
				case agent.FileModified:
				}
				counts(c, pal, ch.Added, ch.Removed)
			})
			if dir != "" {
				ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(10.5).TextColor(pal.Faint).FillWidth().
					SingleLine().Ellipsis("…")
			}
		})
	})
	if open {
		patch(c, pal, ch.Patch)
	}
}

// patch draws a unified diff's hunks, added and removed lines on their tints.
func patch(c *ui.Context, pal native.Palette, text string) {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "===") || strings.HasPrefix(line, "Index:") {
			continue
		}
		lines = append(lines, line)
	}
	ui.Column(c).FillWidth().Radius(6).Clip().Background(pal.Bg).Padding(4, 0).Margin(2, 0, 6, 0).Children(func() {
		if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
			ui.Text(c, "No diff to show").FontSize(12).TextColor(pal.Faint).Padding(4, 8)
			return
		}
		for i, line := range lines {
			if i == patchShown {
				ui.Text(c, strconv.Itoa(len(lines)-i)+" more lines").FontSize(11.5).TextColor(pal.Faint).Padding(2, 8)
				break
			}
			row := ui.Text(c, line).Font("monospace").FontSize(11.5).LineHeight(1.5).TextColor(pal.Text).
				FillWidth().Padding(0, 8).SingleLine()
			switch {
			case strings.HasPrefix(line, "@@"):
				row.TextColor(pal.Faint)
			case strings.HasPrefix(line, "+"):
				row.Background(pal.DiffAddBg)
			case strings.HasPrefix(line, "-"):
				row.Background(pal.DiffDelBg)
			}
		}
	})
}

func counts(c *ui.Context, pal native.Palette, added, removed int) {
	ui.Row(c).Gap(6).Shrink(0).Children(func() {
		ui.Text(c, "+"+strconv.Itoa(added)).FontSize(11.5).TextColor(pal.DiffAdd).FontFeatures("tnum")
		ui.Text(c, "−"+strconv.Itoa(removed)).FontSize(11.5).TextColor(pal.DiffDel).FontFeatures("tnum")
	})
}
