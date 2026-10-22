package workflow

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// ---- The run page

// page shows a run: its steps and result, and beside them what it changed
// and what it may do without asking.
func (p *Plugin) page(c *ui.Context, r *run) {
	pal := native.PaletteFor(c)
	theme := native.MarkdownTheme(pal)
	md := markdown.Options{Theme: &theme, ShareSelection: true, OnLink: p.openLink}
	ui.Row(c).Fill().MinHeight(0).Children(func() {
		ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(820).MinWidth(0).Gap(18).Padding(24, 40, 40, 40).Children(func() {
					p.header(c, pal, r)
					ui.Column(c).Key(r).FillWidth().Gap(10).Children(func() {
						for i, st := range r.steps {
							c.Key(i)
							p.stepView(c, pal, &md, r, st)
						}
					})
					p.outcome(c, pal, &md, r)
				})
			})
		})
		allowed, denied := permissions(r)
		changed := slices.ContainsFunc(r.steps, func(st *step) bool { return len(st.changes) > 0 })
		if len(allowed) == 0 && len(denied) == 0 && !changed {
			return
		}
		ui.Column(c).Width(280).FillHeight().Shrink(0).Background(pal.DrawerBg).Label("Run details").Children(func() {
			ui.Scroll(c).Fill().Children(func() {
				ui.Column(c).FillWidth().Padding(18, 16).Gap(22).Children(func() {
					if changed {
						c.Key("changes")
						side(c, pal, "Changes", func() { changesList(c, pal, r) })
					}
					if len(allowed) > 0 {
						c.Key("allowed")
						side(c, pal, "Allowed without asking", func() { wordList(c, pal, allowed) })
					}
					if len(denied) > 0 {
						c.Key("denied")
						side(c, pal, "Never allowed", func() { wordList(c, pal, denied) })
					}
				})
			})
		})
	})
}

func side(c *ui.Context, pal native.Palette, title string, body func()) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(pal.Faint).SingleLine()
		body()
	})
}

// changesList is each step's changed files, with their line counts.
func changesList(c *ui.Context, pal native.Palette, r *run) {
	for i, st := range r.steps {
		if len(st.changes) == 0 {
			continue
		}
		c.Key(i)
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, st.name).FontSize(12).FontWeight(500).TextColor(pal.Muted).SingleLine()
			for _, ch := range st.changes {
				c.Key(ch.Path)
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, ch.Path).FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
					ui.Text(c, "+"+strconv.Itoa(ch.Added)).FontSize(11.5).TextColor(pal.DiffAdd).Shrink(0)
					ui.Text(c, "−"+strconv.Itoa(ch.Removed)).FontSize(11.5).TextColor(pal.DiffDel).Shrink(0)
				})
			}
		})
	}
}

func wordList(c *ui.Context, pal native.Palette, words []string) {
	for i, w := range words {
		c.Key(i)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(5, 5).Radius(3).Background(pal.Faint).Shrink(0)
			ui.Text(c, w).FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0)
		})
	}
}

// permissions words what the run's steps may do without asking, by their
// policies and by what the user allowed for the run, and what they may never
// do: each kind once, with the steps it applies to.
func permissions(r *run) (allowed, denied []string) {
	type entry struct {
		words string
		where []string
	}
	add := func(list []entry, words, where string) []entry {
		i := slices.IndexFunc(list, func(e entry) bool { return e.words == words })
		if i < 0 {
			return append(list, entry{words, []string{where}})
		}
		if !slices.Contains(list[i].where, where) {
			list[i].where = append(list[i].where, where)
		}
		return list
	}
	var yes, no []entry
	for _, st := range r.steps {
		for _, k := range st.policy.Deny {
			no = add(no, kindWords(k, ""), st.name)
		}
		for _, k := range st.policy.Allow {
			if !slices.Contains(st.policy.Deny, k) {
				yes = add(yes, kindWords(k, ""), st.name)
			}
		}
	}
	keys := slices.SortedFunc(maps.Keys(r.allowed), func(a, b allowKey) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.action, b.action))
	})
	for _, k := range keys {
		yes = add(yes, kindWords(k.kind, k.action), "this run")
	}
	spell := func(list []entry) []string {
		out := make([]string, len(list))
		for i, e := range list {
			out[i] = e.words + " · " + strings.Join(e.where, ", ")
		}
		return out
	}
	return spell(yes), spell(no)
}

// kindWords says what a kind of permission lets the agent do.
func kindWords(k agent.ToolKind, action string) string {
	switch k {
	case agent.ToolShell:
		return "Run commands"
	case agent.ToolRead:
		return "Read files"
	case agent.ToolSearch:
		return "Search files"
	case agent.ToolEdit:
		return "Edit files"
	case agent.ToolFetch:
		return "Fetch from the web"
	case agent.ToolOther:
	}
	return cmp.Or(action, "Other tools")
}

func (p *Plugin) openLink(url string) {
	url = strings.TrimSpace(url)
	if ui.SafeToOpen(url) {
		p.host.OpenURL(url)
	}
}

func (p *Plugin) header(c *ui.Context, pal native.Palette, r *run) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Icon(c, lucide.Workflow()).FontSize(20).TextColor(pal.Orange).Shrink(0)
			ui.Text(c, r.flow.Title).FontSize(22).FontWeight(600).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			badge(c, pal, r)
			if r.state == runRunning {
				stop := components.Button(c, "Stop", components.ButtonSecondary).Disabled(r.stopping)
				stop.OnClick(func() { p.stop(r) })
			}
		})
		ui.Text(c, r.in.Brief).FontSize(14).LineHeight(1.5).TextColor(pal.Text)
		ui.Text(c, "Working in "+native.Tilde(folderName(r.in.Directory))).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
	})
}

// badge is the run's state in one word.
func badge(c *ui.Context, pal native.Palette, r *run) {
	label, tone := "Running", pal.Orange
	switch {
	case r.state == runRunning && r.stopping:
		label, tone = "Stopping…", pal.Muted
	case r.state == runRunning && slices.ContainsFunc(r.steps, (*step).needsYou):
		label = "Needs you"
	case r.state == runRunning:
	case r.state == runDone:
		label, tone = "Done", pal.GreenBadge
	case r.state == runFailed:
		label, tone = "Failed", pal.Danger
	case r.state == runStopped:
		label, tone = "Stopped", pal.Muted
	}
	ui.Box(c).Radius(999).Padding(3, 10).Background(tone.Alpha(.12)).Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(tone).SingleLine()
	})
}

// ---- Steps

func (p *Plugin) stepView(c *ui.Context, pal native.Palette, md *markdown.Options, r *run, st *step) {
	_, canOpen := kernel.Use(p.ctx, chat.ControllerKey)
	card := ui.Column(c).FillWidth().Radius(12).Clip().Border(1, pal.Hairline).Background(pal.DetailBg)
	card.Children(func() {
		ui.Row(c).FillWidth().Padding(11, 14).AlignItems(ui.Center).Gap(11).Children(func() {
			p.stepIcon(c, pal, st)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, st.name).FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…")
				detail, tone := stepDetail(st, pal)
				if detail != "" {
					ui.Text(c, detail).FontSize(12).TextColor(tone).SingleLine().Ellipsis("…")
				}
			})
			if st.state == stepDone && st.answer != "" {
				c.Key("answer")
				label, icon := "Show answer", lucide.ChevronRight()
				if st.open {
					label, icon = "Hide answer", lucide.ChevronDown()
				}
				quiet(c, pal, label, label, icon, func() { st.open = !st.open })
			}
			if canOpen && st.session != "" {
				c.Key("chat")
				quiet(c, pal, "Open "+st.name+" in chat", "Open in chat", lucide.MessageSquare(), func() { p.openStep(r, st) })
			}
		})
		if st.note != "" {
			ui.Text(c, st.note).FontSize(12).TextColor(pal.Warning).Padding(0, 14, 10, 14)
		}
		for i, a := range st.pending {
			c.Key(fmt.Sprint("approval", i, a.ID))
			p.approvalView(c, pal, st, a)
		}
		if st.retry != nil {
			p.decision(c, pal, r, st)
		}
		if st.open && st.answer != "" {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
			ui.Column(c).FillWidth().Padding(12, 16, 14, 16).Children(func() {
				markdown.View(c, st.answer, md)
			})
		}
	})
}

func (p *Plugin) stepIcon(c *ui.Context, pal native.Palette, st *step) {
	ui.Box(c).Size(20, 20).Center().Shrink(0).Children(func() {
		switch st.state {
		case stepRunning:
			if st.needsYou() {
				ui.Icon(c, lucide.ShieldCheck()).FontSize(18).TextColor(pal.Orange)
				return
			}
			components.Spinner(c, components.SpinnerSpokes).Size(16, 16).TextColor(pal.Orange)
		case stepDone:
			ui.Icon(c, lucide.CircleCheck()).FontSize(18).TextColor(pal.GreenBadge)
		case stepFailed:
			ui.Icon(c, lucide.CircleAlert()).FontSize(18).TextColor(pal.Danger)
		case stepStopped:
			ui.Icon(c, lucide.CircleSlash()).FontSize(18).TextColor(pal.Muted)
		}
	})
}

// stepDetail is the quiet line under a step's name.
func stepDetail(st *step, pal native.Palette) (string, ui.Color) {
	switch st.state {
	case stepRunning:
		if len(st.pending) > 0 {
			return "Waiting for your permission", pal.Orange
		}
		detail := st.activity
		if st.tools > 0 {
			detail += fmt.Sprintf(" · %d tool %s", st.tools, plural(st.tools, "call", "calls"))
		}
		return detail, pal.Muted
	case stepDone:
		return preview(st.answer, st.name), pal.Muted
	case stepFailed:
		if st.retry != nil {
			return "Needs your decision", pal.Orange
		}
		return st.err, pal.Danger
	case stepStopped:
		return "Stopped", pal.Muted
	}
	return "", pal.Muted
}

func (p *Plugin) approvalView(c *ui.Context, pal native.Palette, st *step, a agent.Approval) {
	ui.Column(c).FillWidth().Gap(9).Padding(10, 14, 12, 14).Border(0, pal.Hairline).Children(func() {
		ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, lucide.ShieldCheck()).FontSize(14).TextColor(pal.Orange)
			ui.Text(c, "The agent wants to "+approvalAsk(a)).FontSize(13).FontWeight(600).SingleLine()
		})
		for _, res := range a.Resources {
			ui.Box(c).FillWidth().Radius(7).Padding(6, 10).Background(pal.Bg).Border(1, pal.Hairline).Children(func() {
				ui.Text(c, res).Font("monospace").FontSize(12).LineHeight(1.5).TextColor(pal.Text).SingleLine().Ellipsis("…")
			})
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			components.Button(c, "Allow once", components.ButtonPrimary).OnClick(func() { p.decide(st, a, agent.Once, false) })
			components.Button(c, "Allow for this run", components.ButtonSecondary).OnClick(func() { p.decide(st, a, agent.Once, true) })
			components.Button(c, "Deny", components.ButtonGhost).OnClick(func() { p.decide(st, a, agent.Reject, false) })
		})
	})
}

// approvalAsk words what a permission request asks to do.
func approvalAsk(a agent.Approval) string {
	switch a.Kind {
	case agent.ToolShell:
		return "run a shell command"
	case agent.ToolRead:
		return "read files"
	case agent.ToolSearch:
		return "search files"
	case agent.ToolEdit:
		return "edit files"
	case agent.ToolFetch:
		return "fetch from the web"
	case agent.ToolOther:
	}
	if a.Action == "" {
		return "continue"
	}
	return "use " + a.Action
}

// decision is what a failed attempt asks of the user. The attempt may have
// done part of its work, so the app does not repeat it on its own.
func (p *Plugin) decision(c *ui.Context, pal native.Palette, r *run, st *step) {
	ui.Column(c).FillWidth().Gap(9).Padding(0, 14, 12, 14).Children(func() {
		ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		ui.Text(c, st.err).FontSize(12.5).LineHeight(1.5).TextColor(pal.Danger)
		ui.Text(c, "The failed attempt may have changed files. Try the step again, or stop the run.").FontSize(12).TextColor(pal.Muted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			components.Button(c, "Try again", components.ButtonPrimary).OnClick(func() { p.retry(st) })
			components.Button(c, "Stop run", components.ButtonSecondary).OnClick(func() { p.stop(r) })
		})
	})
}

// ---- The outcome

func (p *Plugin) outcome(c *ui.Context, pal native.Palette, md *markdown.Options, r *run) {
	switch r.state {
	case runRunning:
	case runDone:
		ui.Column(c).FillWidth().Radius(12).Padding(14, 18, 18, 18).Gap(10).Background(pal.DetailBg).Border(1, pal.Hairline).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, lucide.Sparkles()).FontSize(15).TextColor(pal.Orange)
				ui.Text(c, "Result").FontSize(13.5).FontWeight(600).Grow(1)
				quiet(c, pal, "Copy result", "Copy", lucide.Copy(), func() { p.copyResult(r) })
			})
			markdown.View(c, r.result, md)
		})
	case runFailed:
		ui.Column(c).FillWidth().Radius(12).Padding(14, 18).Gap(6).Background(pal.Danger.Alpha(.08)).Border(1, pal.Danger.Alpha(.3)).Children(func() {
			ui.Text(c, "The run failed").FontSize(13.5).FontWeight(600).TextColor(pal.Danger)
			ui.Text(c, r.err).FontSize(13).LineHeight(1.5).TextColor(pal.Text)
		})
	case runStopped:
		ui.Text(c, "Stopped. The steps so far keep their conversations; open one in chat to see what the agent did.").
			FontSize(12.5).TextColor(pal.Muted)
	}
}

func (p *Plugin) copyResult(r *run) {
	if err := p.host.Copy(r.result); err != nil {
		p.host.Toast(ui.Toast{Title: "Could not copy the result", Description: err.Error(), Type: components.ToastError})
		return
	}
	p.host.Toast(ui.Toast{Title: "Result copied", Type: components.ToastSuccess})
}

// ---- The brief dialog

func (p *Plugin) promptView(c *ui.Context) {
	pal := native.PaletteFor(c)
	ask := &p.ask
	native.Dialog(c, &ask.open, 480, "Start a workflow", func() {
		ui.Text(c, ask.flow.Title).FontSize(15).FontWeight(600).SingleLine().Ellipsis("…")
		if ask.flow.Summary != "" {
			ui.Text(c, ask.flow.Summary).FontSize(12.5).LineHeight(1.5).TextColor(pal.Muted)
		}
		components.TextBox(c, &ask.brief, components.TextBoxOptions{Multiline: true}).FillWidth().
			Label("What should it do?").Placeholder("Describe what you want done").FontSize(13).Lines(4, 10).AutoFocus()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, lucide.FolderOpen()).FontSize(14).TextColor(pal.Muted).Shrink(0)
			ui.Text(c, folderLabel(ask.folder)).FontSize(12.5).TextColor(pal.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if _, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
				components.Button(c, "Choose…", components.ButtonGhost).OnClick(p.chooseFolder)
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { ask.open = false })
			components.Button(c, "Start", components.ButtonPrimary).Disabled(strings.TrimSpace(ask.brief) == "").OnClick(p.submit)
		})
	})
}

func folderLabel(dir string) string {
	if dir == "" {
		return "In the agent's default folder"
	}
	return "In " + native.Tilde(dir)
}

// ---- Small pieces

// quiet is a small borderless button.
func quiet(c *ui.Context, pal native.Palette, label, text string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(26).Radius(7).Padding(0, 8).Center().Gap(5).Cursor(ui.CursorPointer).Shrink(0)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(on)
	b.Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(pal.Muted)
		ui.Text(c, text).FontSize(12).TextColor(pal.Muted).SingleLine()
	})
}

// preview is the first line of an answer that says something besides the
// step's own name, without the markdown marks that start it.
func preview(text, name string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "#>*-` ")
		if line != "" && !strings.EqualFold(strings.TrimRight(line, ":"), name) {
			return line
		}
	}
	return ""
}

// clip shortens the first line of text to n characters.
func clip(text string, n int) string {
	line := preview(text, "")
	if r := []rune(line); len(r) > n {
		return string(r[:n]) + "…"
	}
	return line
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
