package schedules

import (
	"cmp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/workspace"
)

// heartbeatView is the work panel's section: the open conversation's
// heartbeats, or a way to add one.
func (p *Plugin) heartbeatView(c *ui.Context) {
	pal := native.PaletteFor(c)
	beats := p.heartbeats()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		for _, sc := range beats {
			c.Key("beat:" + sc.ID)
			p.scheduleRow(c, pal, sc, false)
		}
		if len(beats) == 0 {
			ui.Text(c, "Nudge this conversation on a schedule, such as to check CI and carry on.").
				FontSize(12).TextColor(pal.Muted)
			c.Key("add")
			textButton(c, pal, lucide.Plus, "Add heartbeat…", func() { p.openNew(true) })
		}
	})
}

// paneView is the Schedules tab: every schedule.
func (p *Plugin) paneView(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).Fill().Padding(20, 24).Gap(12).Background(pal.Bg).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "Schedules").FontSize(16).FontWeight(600).TextColor(pal.Text)
				ui.Text(c, "Run by the daemon in its local time. A run that fails is not retried.").
					FontSize(12).TextColor(pal.Muted)
			})
			c.Key("new")
			components.Button(c, "New Schedule…", components.ButtonPrimary).
				Disabled(p.agent.Status().State != agent.Ready).OnClick(func() { p.openNew(false) })
		})
		if !p.sourced {
			ui.Text(c, "This backend keeps no schedules.").FontSize(12.5).TextColor(pal.Muted)
			return
		}
		if len(p.list) == 0 {
			ui.Text(c, "No schedules yet. A schedule starts a conversation on a cron or an interval; a heartbeat, "+
				"added from a conversation's work panel, nudges that one.").FontSize(12.5).TextColor(pal.Muted)
			return
		}
		ui.Scroll(c.Key("list")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for _, sc := range p.list {
					c.Key(sc.ID)
					p.scheduleRow(c, pal, sc, true)
				}
			})
		})
	})
}

// scheduleRow is one schedule with its actions; full adds where it runs.
func (p *Plugin) scheduleRow(c *ui.Context, pal native.Palette, sc agent.Schedule, full bool) {
	ui.Column(c).FillWidth().Gap(4).Padding(8, 10).Radius(8).Background(pal.Raised).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			icon := lucide.CalendarClock
			if sc.Heartbeat() {
				icon = lucide.HeartPulse
			}
			ui.Icon(c, icon()).FontSize(12).TextColor(pal.Muted).Shrink(0)
			ui.Text(c, sc.Name).FontSize(12.5).FontWeight(500).TextColor(pal.Text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			_, busy := p.acts[sc.ID]
			c.Key("run")
			iconButton(c, pal, lucide.Play, "Run now", busy, func() { p.runNow(sc) })
			c.Key("pause")
			if sc.Paused {
				iconButton(c, pal, lucide.Timer, "Resume", busy, func() { p.setPaused(sc, false) })
			} else {
				iconButton(c, pal, lucide.Pause, "Pause", busy, func() { p.setPaused(sc, true) })
			}
			c.Key("edit")
			iconButton(c, pal, lucide.Pencil, "Edit", busy, func() { p.openEdit(sc) })
			c.Key("delete")
			iconButton(c, pal, lucide.Trash, "Delete", busy, func() { p.remove(sc) })
		})
		ui.Text(c, when(sc)).FontSize(11.5).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		if full {
			ui.Text(c, p.target(sc)).FontSize(11.5).TextColor(pal.Faint).SingleLine().Ellipsis("…")
		}
		ui.Text(c, strings.TrimSpace(sc.Prompt)).FontSize(11.5).TextColor(pal.Faint).MaxLines(2).Ellipsis("…")
		if line, col := lastRun(sc, pal); line != "" {
			ui.Text(c, line).FontSize(11.5).TextColor(col).MaxLines(2).Ellipsis("…")
		}
	})
}

// target is where a schedule's runs go.
func (p *Plugin) target(sc agent.Schedule) string {
	ws, hasWS := kernel.Use(p.ctx, workspace.ServiceKey)
	if sc.Heartbeat() {
		if hasWS {
			if conv, ok := ws.Lookup(sc.Session); ok && conv.Title != "" {
				return "Heartbeat of “" + conv.Title + "”"
			}
		}
		return "Heartbeat of a conversation"
	}
	where := "the inbox"
	if sc.Directory != "" {
		where = sc.Directory
		if hasWS {
			where = ws.Name(sc.Directory)
		}
	}
	out := "A new conversation in " + where
	if sc.Profile != "" {
		out += " as " + sc.Profile
	} else if sc.Provider != "" {
		out += " on " + sc.Provider
	}
	return out
}

// when says how often a schedule runs and when it runs next.
func when(sc agent.Schedule) string {
	how := "Every " + sc.Every
	if sc.Cron != "" {
		how = "Cron " + sc.Cron
	}
	if sc.Paused || sc.NextRun == 0 {
		return how + " · paused"
	}
	return how + " · next " + stamp(sc.NextRun)
}

// lastRun says how the last run went, in its color.
func lastRun(sc agent.Schedule, pal native.Palette) (string, ui.Color) {
	at := stamp(sc.LastRun)
	switch sc.LastStatus {
	case agent.ScheduleRan:
		return "Ran " + at, pal.Muted
	case agent.ScheduleSkipped:
		return "Skipped " + at + ": " + sc.LastError, pal.Warning
	case agent.ScheduleFailed:
		return "Failed " + at + ": " + sc.LastError, pal.DangerText
	case agent.ScheduleNotRun:
	}
	return "", pal.Muted
}

// stamp is a time as the user reads it: the time today, else the date too.
func stamp(ms int64) string {
	t := time.UnixMilli(ms)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// dialog is the add-or-change form.
func (p *Plugin) dialog(c *ui.Context) {
	if !p.form.on {
		return
	}
	pal := native.PaletteFor(c)
	title := "New schedule"
	switch {
	case p.form.id != "":
		title = "Edit schedule"
	case p.form.heartbeat:
		title = "New heartbeat"
	}
	c.Key("schedule-form")
	native.Dialog(c, &p.form.on, 480, title, func() { p.formView(c, pal, title) })
	if !p.form.on {
		p.form.save.Cancel()
	}
}

func (p *Plugin) formView(c *ui.Context, pal native.Palette, title string) {
	f := &p.form
	ui.Text(c, title).FontSize(14).FontWeight(600)
	if f.heartbeat {
		ui.Text(c, "Sends the prompt to this conversation on its schedule; a beat while it's working is skipped.").
			FontSize(12.5).TextColor(pal.Muted)
	} else {
		ui.Text(c, "Starts a conversation in "+cmp.Or(p.dirName(f.directory), "the inbox")+
			" each time, and skips a run while the last one is still working.").FontSize(12.5).TextColor(pal.Muted)
	}
	c.Key("name")
	components.TextBox(c, &f.name, components.TextBoxOptions{}).FillWidth().Placeholder("Name (default: the prompt's first line)").FontSize(13)
	c.Key("prompt")
	components.TextBox(c, &f.prompt, components.TextBoxOptions{Multiline: true}).FillWidth().Height(110).
		Placeholder("Prompt").FontSize(13)
	field(c, pal, "Runs", func() {
		toggle(c, pal, "Every", !f.cron, func() { f.cron, f.when = false, "30m" })
		toggle(c, pal, "Cron", f.cron, func() { f.cron, f.when = true, "0 9 * * 1-5" })
		c.Key("when")
		placeholder := "30m, 2h"
		if f.cron {
			placeholder = "minute hour day month weekday"
		}
		components.TextBox(c, &f.when, components.TextBoxOptions{}).Grow(1).Placeholder(placeholder).FontSize(13)
	})
	if !f.heartbeat {
		field(c, pal, "Access", func() {
			toggle(c, pal, "Ask", f.mode != "full", func() { f.mode = "ask" })
			toggle(c, pal, "Without approvals", f.mode == "full", func() { f.mode = "full" })
		})
		field(c, pal, "Profile", func() {
			c.Key("profile")
			components.TextBox(c, &f.profile, components.TextBoxOptions{}).Grow(1).Placeholder("Optional agent profile").FontSize(13)
		})
	}
	if f.problem != "" {
		ui.Text(c, f.problem).FontSize(12).TextColor(pal.DangerText)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { f.on = false })
		label := "Save"
		if f.id == "" {
			label = "Add"
		}
		if f.busy {
			label += "…"
		}
		ready := strings.TrimSpace(f.prompt) != "" && strings.TrimSpace(f.when) != ""
		components.Button(c, label, components.ButtonPrimary).Disabled(!ready || f.busy).OnClick(p.submit)
	})
}

func (p *Plugin) dirName(dir string) string {
	if dir == "" {
		return ""
	}
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
		return ws.Name(dir)
	}
	return dir
}

func field(c *ui.Context, pal native.Palette, label string, fn func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted).Width(64).Shrink(0)
		fn()
	})
}

// toggle is one of a set of buttons, filled while on.
func toggle(c *ui.Context, pal native.Palette, label string, on bool, set func()) {
	c.Key(label)
	b := ui.ButtonBase(c).Label(label).Height(28).Padding(0, 10).Radius(6).Center().Cursor(ui.CursorPointer).OnClick(set)
	switch {
	case on:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.Children(func() { ui.Text(c, label).FontSize(12.5).TextColor(pal.Text) })
}

func iconButton(c *ui.Context, pal native.Palette, icon func() *ui.SVG, label string, disabled bool, on func()) {
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(22, 22).Radius(5).Center()
	if !disabled {
		b.Cursor(ui.CursorPointer).OnClick(on)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
	}
	col := pal.Muted
	if disabled {
		col = pal.Faint
	}
	b.Children(func() { ui.Icon(c, icon()).FontSize(11).TextColor(col) })
}

func textButton(c *ui.Context, pal native.Palette, icon func() *ui.SVG, label string, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(24).Padding(0, 6).Radius(5).Gap(5).AlignItems(ui.Center).
		Justify(ui.Start).Cursor(ui.CursorPointer).OnClick(on)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		ui.Icon(c, icon()).FontSize(11).TextColor(pal.Muted)
		ui.Text(c, label).FontSize(12).TextColor(pal.Muted)
	})
}
