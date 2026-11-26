package starters

import (
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// starter is one way to begin a new chat. Choosing it fills the draft and,
// for reviewed work, selects the workflow that plans, produces and reviews
// the result.
type starter struct {
	ID       string
	Title    string
	Detail   string
	Prompt   string
	Icon     func() *ui.SVG
	Reviewed bool
	Primary  bool
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
		if reviewed(list) {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, lucide.ShieldCheck()).FontSize(12).TextColor(pal.Orange).Shrink(0)
				ui.Text(c, "Reviewed work · Wada plans, writes, then a second agent checks the result.").
					FontSize(11.5).TextColor(pal.Muted).SingleLine().Ellipsis("…")
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
	_, hasReview := p.reviewedWorkflow()
	switch {
	case info.Docs > 0 && !info.codeFolder():
		return documentStarters(hasReview)
	case info.Code > 0:
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

// useStarter fills the draft. A reviewed starter selects the reviewed
// workflow for this message; any other clears a stale selection.
func (p *Plugin) useStarter(s starter) {
	workflow := ""
	if s.Reviewed {
		if id, ok := p.reviewedWorkflow(); ok {
			workflow = id
		}
	}
	kernel.Emit(p.ctx, chat.WorkflowSelected, workflow)
	p.chat.SetDraft(s.Prompt)
	p.ctx.Invalidate()
}

// reviewedWorkflow is the first workflow offered for reviewed work.
func (p *Plugin) reviewedWorkflow() (id string, ok bool) {
	for _, e := range kernel.Items(p.ctx, chat.ReviewedWork) {
		return e.ID, true
	}
	return "", false
}

// folderNames are the first few files the starters came from.
func (p *Plugin) folderNames() string {
	names, total := p.folder.info.DocsNames, p.folder.info.Docs
	if p.folder.info.codeFolder() {
		names, total = p.folder.info.CodeNames, p.folder.info.Code
	}
	if len(names) == 0 {
		return ""
	}
	line := strings.Join(names, "  ·  ")
	if total > len(names) {
		line += "  ·  +" + strconv.Itoa(total-len(names)) + " more"
	}
	return line
}

func reviewed(list []starter) bool {
	for _, s := range list {
		if s.Reviewed {
			return true
		}
	}
	return false
}

func documentStarters(reviewed bool) []starter {
	return []starter{
		{ID: "summarize", Title: "Summarize these documents", Detail: "Read what is here and pull out what matters.",
			Prompt: "Read the documents in this folder and summarize what matters: the key points, decisions, numbers and open questions. Cite each source file.",
			Icon:   lucide.FileText, Reviewed: reviewed, Primary: true},
		{ID: "report", Title: "Draft a report from them", Detail: "Turn the material into a document you can send.",
			Prompt: "Draft a report from the documents in this folder. Keep facts and figures accurate, cite the sources, and save the report in this folder.",
			Icon:   lucide.FilePen, Reviewed: reviewed},
		{ID: "compare", Title: "Compare them", Detail: "Show what they agree on and where they differ.",
			Prompt: "Compare the documents in this folder: show what they agree on, where they differ, and anything missing or inconsistent. Cite each source file.",
			Icon:   lucide.Columns2, Reviewed: reviewed},
	}
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
		{ID: "start", Title: "Start a document", Detail: "Make the first file in this folder.",
			Prompt: "Help me start a new document in this folder. Ask me what it should be about before writing.",
			Icon:   lucide.NotebookPen, Primary: true},
		{ID: "look", Title: "What is in this folder?", Detail: "See what Wada can read here.",
			Prompt: "List what is in this folder and tell me which files you can read.",
			Icon:   lucide.FolderSearch},
	}
}

func fileStarters() []starter {
	return []starter{
		{ID: "look", Title: "What is in this folder?", Detail: "See what Wada can read here.",
			Prompt: "List what is in this folder and tell me which files you can read.",
			Icon:   lucide.FolderSearch, Primary: true},
		{ID: "start", Title: "Start a document", Detail: "Make a new file in this folder.",
			Prompt: "Help me start a new document in this folder. Ask me what it should be about before writing.",
			Icon:   lucide.NotebookPen},
	}
}
