package workflow

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

func (p *Plugin) activities(id agent.SessionID) []chat.ActivityItem {
	l := p.lanes[id]
	if l == nil {
		return nil
	}
	var out []chat.ActivityItem
	for _, t := range l.Tasks {
		out = append(out, chat.ActivityItem{ID: t.ID, After: t.Anchor, Render: func(c *ui.Context) { p.taskView(c, l, t) }})
	}
	if len(l.Queue) > 0 || l.Error != "" {
		out = append(out, chat.ActivityItem{ID: "queue-" + string(id), After: 0, Render: func(c *ui.Context) { p.queueView(c, l) }})
	}
	return out
}
func (p *Plugin) taskView(c *ui.Context, l *lane, t *task) {
	pal := native.PaletteFor(c)
	title := t.Flow
	if f, ok := p.flow(t.Flow); ok {
		title = f.Title
	}
	status := t.State
	if t.State == "running" {
		status = t.Phase
	}
	if t.State == "done" {
		status = t.Outcome
	}
	ui.Column(c).FillWidth().Gap(8).Padding(12, 14).Radius(12).Background(pal.DetailBg).Border(1, pal.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title+" · "+status).FontSize(13).FontWeight(600).TextColor(pal.Text)
			components.Button(c, "Details", components.ButtonSecondary).OnClick(func() { p.expanded[t.ID] = !p.expanded[t.ID] })
			if t.State == "paused" || t.State == "stopped" || t.State == "interrupted" || t.State == "failed" {
				label := "Resume"
				if t.State == "failed" {
					label = "Retry"
				}
				components.Button(c, label, components.ButtonSecondary).Disabled(l.Working || l.stopping).OnClick(func() { p.resume(l, t) })
			}
			if l.Working && l.Current == t.ID {
				components.Button(c, "Stop", components.ButtonSecondary).OnClick(func() { p.Stop(l.Session) })
			}
		})
		if t.Summary != "" {
			ui.Text(c, t.Summary).FontSize(12).TextColor(pal.Muted).Wrap()
		}
		if len(t.Changes) > 0 {
			p.deliverableCard(c, pal, l, t)
		}
		if p.expanded[t.ID] {
			for _, part := range []struct{ label, text string }{
				{"Request", t.Brief},
				{"Plan", t.Plan},
				{"Work", t.Work},
			} {
				if part.text == "" {
					continue
				}
				ui.Text(c, part.label).FontSize(10.5).FontWeight(600).TextColor(pal.Faint)
				ui.Text(c, part.text).FontSize(12).TextColor(pal.Text).Wrap()
			}
			for i, r := range t.Reports {
				c.Key(i)
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					ui.Text(c, fmt.Sprintf("%s · %s", r.Status, r.Summary)).FontSize(12).TextColor(pal.Text).Wrap()
					for j, f := range r.Findings {
						c.Key(j)
						ui.Text(c, f.Severity+" · "+f.File+" · "+f.Evidence).FontSize(12).TextColor(pal.Muted).Wrap()
					}
				})
			}
		}
	})
}

// deliverableCard shows the files the task changed, with a way to open them,
// and where the sources live. The review's task-start diff supplies the files.
func (p *Plugin) deliverableCard(c *ui.Context, pal native.Palette, l *lane, t *task) {
	files := make([]agent.FileChange, 0, len(t.Changes))
	for _, ch := range t.Changes {
		if ch.Status != agent.FileDeleted {
			files = append(files, ch)
		}
	}
	if len(files) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(10).Radius(8).Background(pal.Raised).Border(1, pal.Hairline).Children(func() {
		ui.Text(c, "Deliverable").FontSize(10.5).FontWeight(600).TextColor(pal.Faint)
		for i, ch := range files {
			if i == 4 {
				ui.Text(c, fmt.Sprintf("+%d more files", len(files)-4)).FontSize(11).TextColor(pal.Faint)
				break
			}
			c.Key("deliverable:" + ch.Path)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, lucide.FileText()).FontSize(13).TextColor(pal.Orange).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, filepath.Base(ch.Path)).FontSize(12.5).FontWeight(500).TextColor(pal.Text).
						SingleLine().Ellipsis("…")
					meta := ch.Path
					if ch.Added > 0 || ch.Removed > 0 {
						meta += fmt.Sprintf(" · +%d −%d", ch.Added, ch.Removed)
					}
					ui.Text(c, meta).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
				})
				if l.Directory != "" {
					components.Button(c, "Open", components.ButtonSecondary).OnClick(func() { p.openPath(l.Directory, ch.Path) })
				}
			})
		}
		if l.Directory != "" {
			c.Key("sources")
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, lucide.Folder()).FontSize(11).TextColor(pal.Faint).Shrink(0)
				names := []string{filepath.Base(l.Directory)}
				for _, f := range t.References {
					if f.Name != "" {
						names = append(names, f.Name)
					}
				}
				ui.Text(c, "Sources · "+strings.Join(names, " · ")).FontSize(11).TextColor(pal.Muted).
					Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				components.Button(c, "Reveal", components.ButtonGhost).OnClick(func() { p.openPath(l.Directory, ".") })
			})
		}
	})
}

// openPath opens a task file or the work folder with the desktop. A path
// outside the work folder is refused, so a model-supplied path cannot open
// anything else.
func (p *Plugin) openPath(dir, rel string) {
	if dir == "" || rel == "" {
		return
	}
	root := filepath.Clean(dir)
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	inside, err := filepath.Rel(root, path)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		p.host.Toast(ui.Toast{Title: "That file is outside the work folder", Type: components.ToastError})
		return
	}
	p.host.OpenURL((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
}

func (p *Plugin) queueView(c *ui.Context, l *lane) {
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(6).Padding(12).Radius(12).Border(1, pal.Hairline).Children(func() {
		if l.Error != "" {
			ui.Text(c, l.Error).FontSize(12).TextColor(pal.Text).Wrap()
		}
		if len(l.Queue) > 0 {
			ui.Text(c, "Queued · handled after the active turn").FontSize(12).TextColor(pal.Muted)
		}
		for i, d := range l.Queue {
			c.Key(i)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, d.Text).FontSize(12).TextColor(pal.Text).Wrap().Grow(1)
				components.Button(c, "Remove", components.ButtonSecondary).OnClick(func() { p.removeQueued(l, i) })
			})
		}
		if l.Halted && l.Pending != nil && l.Pending.TaskID == "" {
			components.Button(c, "Retry failed send", components.ButtonSecondary).Disabled(l.Working || l.stopping).OnClick(func() { p.resume(l, nil) })
		}
		if l.Halted && len(l.Queue) > 0 {
			components.Button(c, "Continue queued messages", components.ButtonSecondary).Disabled(l.Working || l.stopping).OnClick(func() { l.Halted = false; l.Pending = nil; l.Error = ""; p.save(l); p.advance(l) })
		}
	})
}

func (p *Plugin) removeQueued(l *lane, i int) {
	d := l.Queue[i]
	l.Queue = slices.Delete(slices.Clone(l.Queue), i, i+1)
	for j := i; j < len(l.Queue); j++ {
		if l.Queue[j].Anchor > d.Anchor {
			l.Queue[j].Anchor--
		}
	}
	kernel.Emit(p.ctx, chat.QueueRemoved, d)
	p.save(l)
}
