package activity

import (
	"path"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

const (
	outputShown = 12  // the last lines of a command's output
	patchShown  = 400 // lines of one file's diff
)

// changesView lists the changed files with their line counts; a file opens
// to its diff.
func (p *Plugin) changesView(c *ui.Context) {
	pal := native.PaletteFor(c)
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

func (p *Plugin) file(c *ui.Context, pal native.Palette, ch agent.FileChange) {
	open := p.open[ch.Path]
	dir, name := path.Split(ch.Path)
	b := ui.ButtonBase(c).Label(ch.Path).FillWidth().Height(28).Radius(6).Padding(0, 6).Gap(6).
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
		label := ui.Text(c, name).FontSize(12.5).TextColor(pal.Text).Shrink(0).SingleLine()
		switch ch.Status {
		case agent.FileAdded:
			label.TextColor(pal.DiffAdd)
		case agent.FileDeleted:
			label.TextColor(pal.DiffDel)
		case agent.FileModified:
		}
		ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(11.5).TextColor(pal.Faint).Grow(1).MinWidth(0).
			SingleLine().Ellipsis("…")
		counts(c, pal, ch.Added, ch.Removed)
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

// commandView shows the last shell command, how it ended and the end of its
// output.
func (p *Plugin) commandView(c *ui.Context) {
	pal := native.PaletteFor(c)
	t := p.last
	ui.Column(c).FillWidth().Radius(8).Clip().Background(pal.Bg).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Padding(8, 10).AlignItems(ui.Start).Children(func() {
			ui.Text(c, "$").Font("monospace").FontSize(12).TextColor(pal.Faint).Shrink(0)
			ui.Text(c, t.Command).Font("monospace").FontSize(12).TextColor(pal.Text).Grow(1).MinWidth(0).Selectable()
		})
		lines := output(t)
		if len(lines) > 0 {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
			ui.Column(c).FillWidth().Padding(6, 10).Children(func() {
				if len(lines) > outputShown {
					ui.Text(c, "…").FontSize(11.5).TextColor(pal.Faint)
					lines = lines[len(lines)-outputShown:]
				}
				for _, line := range lines {
					ui.Text(c, line).Font("monospace").FontSize(11.5).LineHeight(1.5).TextColor(pal.Muted).SingleLine()
				}
			})
		}
	})
	exit, color := "exit "+strconv.Itoa(t.Exit), pal.Faint
	if t.Exit != 0 {
		color = pal.DiffDel
	}
	ui.Text(c, exit).FontSize(11.5).TextColor(color)
}

func counts(c *ui.Context, pal native.Palette, added, removed int) {
	ui.Row(c).Gap(6).Shrink(0).Children(func() {
		ui.Text(c, "+"+strconv.Itoa(added)).FontSize(11.5).TextColor(pal.DiffAdd).FontFeatures("tnum")
		ui.Text(c, "−"+strconv.Itoa(removed)).FontSize(11.5).TextColor(pal.DiffDel).FontFeatures("tnum")
	})
}
