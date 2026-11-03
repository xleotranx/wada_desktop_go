package jobs

import (
	"fmt"
	"path/filepath"
	"strings"

	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/jobs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// titleButton opens the Jobs page from the title strip.
func (p *Plugin) titleButton(c *ui.Context) {
	pal := native.PaletteFor(c)
	b := ui.ButtonBase(c.Key("jobs.page")).Label("Jobs").Tooltip("Jobs  ⌘J").Size(28, 28).Radius(7).Center().
		Cursor(ui.CursorPointer)
	switch {
	case p.host.Page() == jobs.PageID:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.host.ShowPage(jobs.PageID) })
	b.Children(func() {
		ui.Icon(c, lucide.Briefcase()).FontSize(16).TextColor(pal.Muted)
	})
}

// page lists every job on the left and the chosen one on the right: its plan,
// its result, what the reviewer found and the files it left.
func (p *Plugin) page(c *ui.Context) {
	pal := native.PaletteFor(c)
	list := p.Jobs()
	picked := p.selected
	if _, ok := p.find(picked); !ok && len(list) > 0 {
		picked = list[0].ID
	}
	ui.Column(c).Fill().Children(func() {
		p.pageHead(c, pal)
		ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		p.pageBody(c, pal, list, picked)
	})
}

// pageBody is the list and the chosen job.
func (p *Plugin) pageBody(c *ui.Context, pal native.Palette, list []jobs.Job, picked string) {
	ui.Row(c).Fill().MinHeight(0).Children(func() {
		ui.Column(c).Width(280).FillHeight().Shrink(0).Padding(18, 12).Children(func() {
			ui.Row(c).FillWidth().Padding(6, 8, 12, 8).Gap(6).Children(func() {
				ui.Text(c, "Jobs").FontSize(13).FontWeight(600).TextColor(pal.Text)
				ui.Text(c, fmt.Sprintf("%d", len(list))).FontSize(11).TextColor(pal.Faint)
			})
			ui.Scroll(c).Fill().Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					if len(list) == 0 {
						ui.Text(c, "No jobs yet.").FontSize(12).TextColor(pal.Faint).Padding(8, 8)
						return
					}
					for _, job := range list {
						p.pageRow(c, pal, job, picked)
					}
				})
			})
		})
		ui.Scroll(c.Key("jobs.detail:" + picked)).Fill().MinWidth(0).Children(func() {
			job, ok := p.find(picked)
			if !ok {
				p.pageEmpty(c, pal)
				return
			}
			p.pageDetail(c, pal, job)
		})
	})
}

// pageHead starts a job without leaving the page: the goal, the folder it
// works in, and Start.
func (p *Plugin) pageHead(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(14, 18, 12, 18).Children(func() {
		ui.Box(c).FillWidth().Grow(1).MinWidth(0).Children(func() {
			ui.TextInput(c.Key("jobs.goal"), &p.draft).Placeholder("What should the job do?").MinHeight(30)
		})
		components.Button(c, p.folderLabel(), components.ButtonSecondary).OnClick(p.chooseFolder)
		components.Button(c, "Start job", components.ButtonPrimary).OnClick(p.startFromPage)
	})
	if p.folder == "" {
		ui.Text(c, "Choose a folder: the job reads its documents and writes there.").
			FontSize(11).TextColor(pal.Faint).Padding(0, 18, 6, 18)
	}
}

// folderLabel names the folder the next job works in.
func (p *Plugin) folderLabel() string {
	if p.folder == "" {
		return "Choose folder"
	}
	return native.Tilde(p.folder)
}

// chooseFolder asks for the folder the next job works in.
func (p *Plugin) chooseFolder() {
	p.workspace.ChooseFolder(func(path string) {
		if path == "" {
			return
		}
		p.folder = path
		p.ctx.Invalidate()
	})
}

// startFromPage starts the job typed on the page.
func (p *Plugin) startFromPage() {
	goal := strings.TrimSpace(p.draft)
	switch {
	case goal == "":
		p.host.Toast(ui.Toast{Title: "Type what the job should do"})
		return
	case p.folder == "":
		p.host.Toast(ui.Toast{Title: "Choose the folder the job works in"})
		return
	}
	job, err := p.Start(goal, p.folder)
	if err != nil {
		p.host.Toast(ui.Toast{Title: err.Error()})
		return
	}
	p.draft = ""
	p.selected = job.ID
}

// seedFolder offers the last job's folder, or the open chat's, when the page
// opens with none chosen.
func (p *Plugin) seedFolder() {
	if p.folder != "" {
		return
	}
	if len(p.jobs) > 0 {
		p.folder = p.jobs[len(p.jobs)-1].Folder
		return
	}
	p.folder = p.chat.Current().Directory
}

// pageRow is one job in the page's list.
func (p *Plugin) pageRow(c *ui.Context, pal native.Palette, job jobs.Job, picked string) {
	row := ui.ButtonBase(c.Key("jobs.nav:"+job.ID)).FillWidth().MinHeight(44).Padding(8, 10).Radius(8).
		AlignItems(ui.Start).Justify(ui.Start).Cursor(ui.CursorPointer)
	if job.ID == picked {
		row.Background(pal.Selected)
	} else if row.Hovered() {
		row.Background(pal.Hover)
	}
	row.OnClick(func() { p.selected = job.ID })
	row.Children(func() {
		ui.Column(c).FillWidth().Gap(1).Children(func() {
			ui.Text(c, job.Goal).FontSize(12).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, status(job)).FontSize(11).TextColor(pal.Faint).SingleLine().Ellipsis("…")
		})
	})
}

// pageDetail is the chosen job in full.
func (p *Plugin) pageDetail(c *ui.Context, pal native.Palette, job jobs.Job) {
	ui.Column(c).FillWidth().MaxWidth(860).Gap(20).Padding(30, 44, 40, 44).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, job.Goal).FontSize(22).FontWeight(600).TextColor(pal.Text)
			ui.Text(c, detailLine(job)).FontSize(12).TextColor(pal.Faint)
			ui.Row(c).Gap(6).Padding(4, 0, 0, 0).Children(func() {
				if job.State.Active() && job.State != jobs.PlanReview {
					components.Button(c, "Stop", components.ButtonGhost).OnClick(func() {
						if err := p.Cancel(job.ID); err != nil {
							p.host.Toast(ui.Toast{Title: err.Error()})
						}
					})
				}
				if !job.State.Active() {
					components.Button(c, "Remove", components.ButtonGhost).OnClick(func() {
						if err := p.Remove(job.ID); err != nil {
							p.host.Toast(ui.Toast{Title: err.Error()})
						}
					})
				}
			})
		})
		if job.Plan != nil && job.State != jobs.Planning {
			editable := job.State == jobs.PlanReview
			p.pageSection(c, pal, "Plan", func() { p.planCard(c, pal, job, editable) })
		}
		if job.Summary != "" {
			p.pageSection(c, pal, "Review", func() {
				ui.Text(c, job.Summary).FontSize(12).TextColor(pal.Muted)
			})
		}
		if job.Result != "" {
			p.pageSection(c, pal, "Result", func() {
				ui.Text(c, job.Result).FontSize(13).TextColor(pal.Text)
			})
		}
		if len(job.Findings) > 0 {
			p.pageSection(c, pal, "Findings", func() {
				ui.Column(c).FillWidth().Gap(3).Children(func() {
					for _, finding := range job.Findings {
						ui.Text(c, "• "+finding.Where+": "+finding.What).FontSize(12).TextColor(pal.Warning)
					}
				})
			})
		}
		if len(job.Files) > 0 {
			p.pageSection(c, pal, "Deliverables", func() {
				ui.Column(c).FillWidth().Gap(3).Children(func() {
					for _, file := range job.Files {
						ui.Text(c, filepath.Base(file.Path)+" · "+size(file.Bytes)).
							FontSize(12).TextColor(pal.Text)
					}
				})
			})
		}
	})
}

// pageSection is a titled block of the detail column.
func (p *Plugin) pageSection(c *ui.Context, pal native.Palette, title string, body func()) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, title).FontSize(11).TextColor(pal.Faint)
		body()
	})
}

// pageEmpty stands in the detail column while there is no job to show.
func (p *Plugin) pageEmpty(c *ui.Context, pal native.Palette) {
	ui.Column(c).Fill().Center().Gap(10).Padding(40, 40).Children(func() {
		ui.Text(c, "No jobs yet").FontSize(20).FontWeight(600).TextColor(pal.Text)
		ui.Text(c, "Open a folder in a chat, type the goal in the composer, then choose Agent ▸ Start Job from Draft.").
			FontSize(13).TextColor(pal.Muted).MaxWidth(380)
		components.Button(c, "Go to a chat", components.ButtonSecondary).OnClick(func() {
			p.host.ShowPage(chat.PageID)
		})
	})
}

// allJobs opens the page from the new chat's jobs block.
func (p *Plugin) allJobs(c *ui.Context) {
	components.Button(c, "All jobs", components.ButtonGhost).OnClick(func() {
		p.host.ShowPage(jobs.PageID)
	})
}

// detailLine says what the job has to show, beyond its state.
func detailLine(job jobs.Job) string {
	parts := []string{status(job)}
	if job.Sources > 0 {
		parts = append(parts, fmt.Sprintf("%d documents", job.Sources))
	}
	if job.Chars > 0 {
		parts = append(parts, size(job.Chars)+" read")
	}
	return strings.Join(parts, " · ")
}

// size writes a length the short way.
func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
