package starters

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// starter is one way to begin a new chat. Choosing it fills the draft.
type starter struct {
	ID      string
	Title   string
	Detail  string
	Prompt  string
	Icon    func() *ui.SVG
	Primary bool
}

// view draws the starters under the chat's composer. The chat only renders
// its Starters entries for an empty new chat.
func (p *Plugin) view(c *ui.Context) {
	conv := p.chat.Current()
	if conv.Session != "" || conv.Directory == "" {
		return
	}
	pal := native.PaletteFor(c)
	if p.folder.dir != conv.Directory || p.folder.loading {
		ui.Text(c, "Looking at this folder…").FontSize(12).TextColor(pal.Muted)
		return
	}
	if p.folder.err != nil {
		ui.Text(c, "This folder can't be read. Choose another from the composer.").FontSize(12).TextColor(pal.Danger)
		return
	}
	list := p.starterList()
	if len(list) == 0 {
		return
	}
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(9).MaxWidth(onechat.ContentWidth).Children(func() {
		if names := p.folderNames(); names != "" {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, lucide.Files()).FontSize(12).TextColor(pal.Faint).Shrink(0)
				ui.Text(c, names).FontSize(11.5).TextColor(pal.Muted).SingleLine().Ellipsis("…")
			})
		}
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			for _, s := range list {
				c.Key(s.ID)
				p.starterButton(c, pal, s)
			}
		})
	})
}

// starterList is the folder's starters, best first.
func (p *Plugin) starterList() []starter {
	info := p.folder.info
	switch {
	case info.codeFolder():
		return codeStarters()
	case info.Files == 0:
		return emptyStarters()
	default:
		return fileStarters()
	}
}

func (p *Plugin) starterButton(c *ui.Context, pal native.Palette, s starter) {
	b := ui.ButtonBase(c).Label(s.Title).FillWidth().Height(44).Padding(7, 12).Radius(10).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	b.Border(1, pal.Hairline)
	if s.Primary {
		b.Border(1, pal.Orange.Alpha(.6))
	}
	switch {
	case b.Hovered():
		b.Background(pal.Hover)
	case s.Primary:
		b.Background(pal.Orange.Alpha(.08))
	}
	b.OnClick(func() { p.useStarter(s) })
	b.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			color := pal.Muted
			if s.Primary {
				color = pal.Orange
			}
			ui.Icon(c, s.Icon()).FontSize(15).TextColor(color).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, s.Title).FontSize(13).FontWeight(500).TextColor(pal.Text).SingleLine()
				ui.Text(c, s.Detail).FontSize(11.5).TextColor(pal.Muted).SingleLine().Ellipsis("…")
			})
			ui.Icon(c, lucide.ArrowRight()).FontSize(13).TextColor(pal.Faint).Shrink(0)
		})
	})
}

// useStarter fills the draft.
func (p *Plugin) useStarter(s starter) {
	p.chat.SetDraft(s.Prompt)
	p.ctx.Invalidate()
}

// folderNames are the first few code files the starters came from.
func (p *Plugin) folderNames() string {
	names, total := p.folder.info.CodeNames, p.folder.info.Code
	if len(names) == 0 {
		return ""
	}
	line := strings.Join(names, "  ·  ")
	if total > len(names) {
		line += "  ·  +" + strconv.Itoa(total-len(names)) + " more"
	}
	return line
}

func codeStarters() []starter {
	return []starter{
		{ID: "explain", Title: "Explain this project", Detail: "What it does, how it is laid out, where to start.",
			Prompt: "Give me a tour of this project: what it does, how it is laid out and where to start reading.",
			Icon:   lucide.BookOpen, Primary: true},
		{ID: "review", Title: "Review the changes", Detail: "Uncommitted work, for bugs and anything unclear.",
			Prompt: "Review my uncommitted changes for bugs and anything unclear.",
			Icon:   lucide.GitPullRequest},
		{ID: "bug", Title: "Find a bug", Detail: "Reproduce one problem and point at the cause.",
			Prompt: "Find a bug in the code in this folder, explain how to reproduce it, and point to the files involved.",
			Icon:   lucide.Bug},
	}
}

func emptyStarters() []starter {
	return []starter{
		{ID: "scaffold", Title: "Scaffold a new project", Detail: "Initialize a codebase in this folder.",
			Prompt: "Help me scaffold a new project in this folder. Ask me about the language, stack, and layout before writing files.",
			Icon:   lucide.NotebookPen, Primary: true},
		{ID: "look", Title: "Explore this folder", Detail: "See what is currently here.",
			Prompt: "List what is in this folder and summarize its structure.",
			Icon:   lucide.FolderSearch},
	}
}

func fileStarters() []starter {
	return []starter{
		{ID: "look", Title: "Explore this folder", Detail: "Inspect the files and structure here.",
			Prompt: "List what is in this folder and summarize its structure.",
			Icon:   lucide.FolderSearch, Primary: true},
		{ID: "review", Title: "Review the changes", Detail: "Uncommitted work, for bugs and anything unclear.",
			Prompt: "Review my uncommitted changes for bugs and anything unclear.",
			Icon:   lucide.GitPullRequest},
	}
}
