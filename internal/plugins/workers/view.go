package workers

import (
	"cmp"
	"path"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
)

const (
	drawerWidth = 400
	patchLimit  = 300
)

func (p *Plugin) harnessPickerView(c *ui.Context) {
	if p.conv.Session != "" || len(p.harnesses) <= 1 {
		return
	}
	c.Key("harness")
	var options []onechat.Option
	selected := cmp.Or(p.selectedProvider, agent.EmbeddedHarness)
	label := "PiG"
	for _, h := range p.harnesses {
		desc := harnessKindLabel(h.Kind)
		if !h.Available {
			desc += " · not installed"
		} else if h.Description != "" {
			desc += " · " + h.Description
		}
		optLabel := cmp.Or(h.Name, h.ID)
		options = append(options, onechat.Option{
			ID:          h.ID,
			Label:       optLabel,
			Description: desc,
			Group:       "Agent harness",
		})
		if h.ID == selected {
			label = optLabel
		}
	}
	picked := onechat.Picker(c, &p.picker, onechat.PickerOptions{
		Label:    label,
		Options:  options,
		Selected: selected,
		Width:    260,
	})
	if chosen := picked.Chosen; chosen != "" {
		p.ctx.Post(func() { p.selectedProvider = chosen })
	}
}

func (p *Plugin) workPanelView(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if len(p.workers) > 0 {
			ui.Text(c, "Workers").FontSize(11).FontWeight(500).TextColor(pal.Faint)
			for _, w := range p.workers {
				worker := w
				c.Key("worker:" + string(worker.ID))
				p.workerRow(c, pal, worker)
			}
		}
		if p.secondaryWorktreeCount() > 0 {
			ui.Text(c, "Worktrees").FontSize(11).FontWeight(500).TextColor(pal.Faint).Padding(4, 0, 0, 0)
			for _, wt := range p.worktrees {
				if wt.IsMain {
					continue
				}
				tree := wt
				c.Key("wt:" + tree.Path)
				p.worktreeRow(c, pal, tree)
			}
		}
	})
}

func (p *Plugin) workerRow(c *ui.Context, pal native.Palette, w agent.Worker) {
	statusLabel, statusColor := workerStatusStyle(w.Status, pal)
	active := p.inspected == w.ID
	b := ui.ButtonBase(c).Label(cmp.Or(w.Name, w.Provider)).FillWidth().Radius(6).Padding(6, 8).Gap(6).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
	if active {
		b.Background(pal.Selected)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	id := w.ID
	b.OnClick(func() { p.openDrawer(id) })
	b.Children(func() {
		ui.Icon(c, lucide.Cpu()).FontSize(12).TextColor(statusColor).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, cmp.Or(w.Name, string(w.ID))).FontSize(12.5).FontWeight(500).TextColor(pal.Text).
				SingleLine().Ellipsis("…")
			sub := w.Provider
			if w.Branch != "" {
				sub += " · " + w.Branch
			}
			ui.Text(c, sub).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		ui.Box(c).Padding(2, 6).Radius(99).Background(statusColor.Alpha(.15)).Shrink(0).Children(func() {
			ui.Text(c, statusLabel).FontSize(10.5).FontWeight(500).TextColor(statusColor)
		})
		if w.Status == agent.WorkerRunning || w.Status == agent.WorkerWaitingApproval {
			c.Key("stop")
			sb := ui.ButtonBase(c).Label("Stop worker").Tooltip("Stop worker").Size(22, 22).Radius(5).
				Center().Cursor(ui.CursorPointer).OnClick(func() { p.stopWorker(id) })
			if sb.Hovered() {
				sb.Background(pal.Hover)
			}
			sb.Children(func() { ui.Icon(c, lucide.Square()).FontSize(11).TextColor(pal.Danger) })
		}
	})
}

func (p *Plugin) worktreeRow(c *ui.Context, pal native.Palette, wt agent.Worktree) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(4, 6).Radius(6).Background(pal.Raised).Children(func() {
		ui.Icon(c, lucide.GitPullRequest()).FontSize(11).TextColor(pal.Muted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, cmp.Or(wt.Branch, path.Base(wt.Path))).FontSize(12).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, wt.Path).FontSize(10.5).TextColor(pal.Faint).SingleLine().Ellipsis("…")
		})
		if wt.PR != nil {
			pr := wt.PR
			prCol := pal.Muted
			switch pr.Checks {
			case "pass":
				prCol = pal.GreenBadge
			case "fail":
				prCol = pal.DangerText
			case "pending":
				prCol = pal.Orange
			}
			c.Key("pr")
			label := "#" + strconv.Itoa(pr.Number)
			if pr.Checks != "" && pr.Checks != "none" {
				label += " · " + pr.Checks
			}
			pb := ui.ButtonBase(c).Label("Open PR "+label).Tooltip(cmp.Or(pr.Title, pr.URL)).Height(20).Padding(0, 6).Radius(99).
				Background(prCol.Alpha(.15)).Center().Cursor(ui.CursorPointer).Shrink(0).
				OnClick(func() {
					if ui.SafeToOpen(pr.URL) {
						p.host.OpenURL(pr.URL)
					}
				})
			pb.Children(func() {
				ui.Text(c, label).FontSize(10.5).FontWeight(500).TextColor(prCol).SingleLine()
			})
		}
		if wt.Dirty {
			ui.Text(c, "modified").FontSize(10.5).TextColor(pal.Orange).Shrink(0)
		}
		c.Key("remove")
		wtPath := wt.Path
		rb := ui.ButtonBase(c).Label("Remove worktree").Tooltip("Remove worktree").Size(22, 22).Radius(5).
			Center().Cursor(ui.CursorPointer).OnClick(func() { p.removeWorktree(wtPath) })
		if rb.Hovered() {
			rb.Background(pal.Hover)
		}
		rb.Children(func() { ui.Icon(c, lucide.X()).FontSize(11).TextColor(pal.Muted) })
	})
}

func (p *Plugin) drawerView(c *ui.Context) {
	if p.inspected == "" {
		return
	}
	pal := native.PaletteFor(c)
	w, found := p.inspectedWorker()
	title := cmp.Or(w.Name, string(p.inspected))
	if !found {
		title = string(p.inspected)
	}
	sub := cmp.Or(w.Provider, "worker")
	if w.Branch != "" {
		sub += " · " + w.Branch
	}

	ui.Row(c.Key("worker-drawer")).Width(drawerWidth).FillHeight().Shrink(0).Label("Worker inspector").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(pal.Bg).Children(func() {
			p.drawerHeader(c, pal, title, sub, w)
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			p.drawerTabs(c, pal)
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(12, 14).Children(func() {
				switch p.drawerTab {
				case tabTranscript:
					p.drawerTranscript(c, pal)
				case tabDiff:
					p.drawerDiffs(c, pal)
				}
			})
		})
	})
}

func (p *Plugin) drawerHeader(c *ui.Context, pal native.Palette, title, sub string, w agent.Worker) {
	statusLabel, statusColor := workerStatusStyle(w.Status, pal)
	ui.Row(c).FillWidth().Height(52).Shrink(0).Padding(0, 10, 0, 14).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, lucide.Cpu()).FontSize(14).TextColor(statusColor).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		ui.Box(c).Padding(2, 6).Radius(99).Background(statusColor.Alpha(.15)).Shrink(0).Children(func() {
			ui.Text(c, statusLabel).FontSize(10.5).FontWeight(500).TextColor(statusColor)
		})
		if w.Status == agent.WorkerRunning || w.Status == agent.WorkerWaitingApproval {
			id := w.ID
			sb := ui.ButtonBase(c.Key("stop")).Label("Stop").Tooltip("Stop worker").Size(26, 26).Radius(6).
				Center().Cursor(ui.CursorPointer).OnClick(func() { p.stopWorker(id) })
			if sb.Hovered() {
				sb.Background(pal.Hover)
			}
			sb.Children(func() { ui.Icon(c, lucide.Square()).FontSize(12).TextColor(pal.Danger) })
		}
		rb := ui.ButtonBase(c.Key("refresh")).Label("Refresh").Tooltip("Refresh").Size(26, 26).Radius(6).
			Center().Cursor(ui.CursorPointer).OnClick(func() { p.loadInspected(p.inspected) })
		if rb.Hovered() {
			rb.Background(pal.Hover)
		}
		rb.Children(func() { ui.Icon(c, lucide.RefreshCw()).FontSize(12).TextColor(pal.Muted) })

		cb := ui.ButtonBase(c.Key("close")).Label("Close worker inspector").Size(26, 26).Radius(6).
			Center().Cursor(ui.CursorPointer).OnClick(p.closeDrawer)
		if cb.Hovered() {
			cb.Background(pal.Hover)
		}
		cb.Children(func() { ui.Icon(c, lucide.X()).FontSize(13).TextColor(pal.Muted) })
	})
}

func (p *Plugin) drawerTabs(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Padding(6, 12).Gap(6).Children(func() {
		tabs := []struct {
			tab   drawerTab
			label string
		}{
			{tabTranscript, "Transcript"},
			{tabDiff, "Diff (" + strconv.Itoa(len(p.drawerChanges)) + ")"},
		}
		for _, item := range tabs {
			active := p.drawerTab == item.tab
			c.Key(item.label)
			b := ui.ButtonBase(c).Label(item.label).Height(26).Padding(0, 10).Radius(6).
				Cursor(ui.CursorPointer).Center().OnClick(func() { p.drawerTab = item.tab })
			if active {
				b.Background(pal.Selected)
			} else if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.Children(func() {
				col := pal.Muted
				if active {
					col = pal.Text
				}
				ui.Text(c, item.label).FontSize(12).FontWeight(500).TextColor(col)
			})
		}
	})
}

func (p *Plugin) drawerTranscript(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		if p.drawerLoading && len(p.drawerMessages) == 0 {
			ui.Text(c, "Loading worker transcript…").FontSize(12).TextColor(pal.Muted)
			return
		}
		if len(p.drawerMessages) == 0 && p.drawerLive.Len() == 0 {
			ui.Text(c, "No messages yet").FontSize(12).TextColor(pal.Faint)
			return
		}
		for i, m := range p.drawerMessages {
			c.Key(i)
			switch m.Role {
			case agent.RoleUser:
				ui.Column(c).FillWidth().Padding(8, 10).Radius(8).Background(pal.Raised).Children(func() {
					ui.Text(c, m.Text).FontSize(12.5).TextColor(pal.Text)
				})
			case agent.RoleAssistant:
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for j, part := range m.Parts {
						c.Key(j)
						switch part.Kind {
						case agent.PartText:
							if part.Text != "" {
								ui.Text(c, part.Text).FontSize(12.5).LineHeight(1.45).TextColor(pal.Text)
							}
						case agent.PartTool:
							if part.Tool != nil {
								ui.Row(c).FillWidth().Padding(4, 8).Radius(6).Background(pal.Raised).Gap(6).Children(func() {
									ui.Icon(c, lucide.Terminal()).FontSize(11).TextColor(pal.Muted).Shrink(0)
									label := cmp.Or(part.Tool.Command, part.Tool.Name)
									ui.Text(c, label).Font("monospace").FontSize(11.5).TextColor(pal.Text).
										Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
									if part.Tool.Status != "" {
										ui.Text(c, part.Tool.Status).FontSize(11).TextColor(pal.Muted).Shrink(0)
									}
								})
							}
						case agent.PartReasoning, agent.PartOther:
						}
					}
				})
			case agent.RoleOther:
			}
		}
		if live := p.drawerLive.String(); live != "" {
			ui.Text(c.Key("live"), live).FontSize(12.5).LineHeight(1.45).TextColor(pal.Text)
		}
	})
}

func (p *Plugin) drawerDiffs(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if len(p.drawerChanges) == 0 {
			ui.Text(c, "No worktree changes").FontSize(12).TextColor(pal.Faint)
			return
		}
		for _, ch := range p.drawerChanges {
			change := ch
			open := p.drawerOpenDiff[change.Path]
			status, color := "M", pal.Faint
			switch change.Status {
			case agent.FileAdded:
				status, color = "A", pal.DiffAdd
			case agent.FileDeleted:
				status, color = "D", pal.DiffDel
			case agent.FileModified:
			}
			ui.Column(c.Key(change.Path)).FillWidth().Gap(2).Children(func() {
				b := ui.ButtonBase(c).Label(change.Path).FillWidth().Height(28).Radius(6).Padding(4, 6).Gap(6).
					AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).
					OnClick(func() { p.drawerOpenDiff[change.Path] = !open })
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.Children(func() {
					chev := lucide.ChevronRight
					if open {
						chev = lucide.ChevronDown
					}
					ui.Icon(c, chev()).FontSize(11).TextColor(pal.Faint).Shrink(0)
					ui.Text(c, change.Path).FontSize(12).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
					ui.Text(c, status).FontSize(10.5).FontWeight(600).TextColor(color).Shrink(0)
					ui.Text(c, "+"+strconv.Itoa(change.Added)+" −"+strconv.Itoa(change.Removed)).
						FontSize(11).TextColor(pal.Muted).Shrink(0)
				})
				if open && change.Patch != "" {
					renderPatch(c, pal, change.Patch)
				}
			})
		}
	})
}

func renderPatch(c *ui.Context, pal native.Palette, text string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	ui.Column(c).FillWidth().Radius(6).Clip().Background(pal.Raised).Padding(4, 0).Children(func() {
		for i, line := range lines {
			if i == patchLimit {
				ui.Text(c, strconv.Itoa(len(lines)-i)+" more lines").FontSize(11).TextColor(pal.Faint).Padding(2, 8)
				break
			}
			row := ui.Text(c, line).Font("monospace").FontSize(11).LineHeight(1.45).TextColor(pal.Text).
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

func harnessKindLabel(k agent.HarnessKind) string {
	switch k {
	case agent.HarnessEmbedded:
		return "Embedded"
	case agent.HarnessACP:
		return "ACP"
	case agent.HarnessClaude:
		return "Claude Code"
	case agent.HarnessCodex:
		return "Codex"
	}
	return "External"
}

func workerStatusStyle(st agent.WorkerStatus, pal native.Palette) (string, ui.Color) {
	switch st {
	case agent.WorkerIdle:
		return "Idle", pal.Muted
	case agent.WorkerRunning:
		return "Running", pal.Orange
	case agent.WorkerWaitingApproval:
		return "Waiting", pal.Waiting
	case agent.WorkerCompleted:
		return "Done", pal.GreenBadge
	case agent.WorkerFailed:
		return "Failed", pal.Danger
	case agent.WorkerStopped:
		return "Stopped", pal.Muted
	}
	return "Idle", pal.Muted
}
