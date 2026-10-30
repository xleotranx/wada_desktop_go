package workflow

import (
	"fmt"
	"slices"

	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
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
		if p.expanded[t.ID] {
			ui.Text(c, t.Brief).FontSize(12).TextColor(pal.Text).Wrap()
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
