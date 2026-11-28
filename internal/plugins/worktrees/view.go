package worktrees

import (
	"cmp"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

func (p *Plugin) dialogs(c *ui.Context) {
	pal := native.PaletteFor(c)
	if p.newOn {
		c.Key("new")
		native.Dialog(c, &p.newOn, 440, "New workspace", func() { p.newView(c, pal) })
	}
	if p.archiving.ID != "" {
		c.Key("archive")
		native.Dialog(c, &p.archiveOn, 400, "Archive workspace", func() { p.archiveView(c, pal) })
		if !p.archiveOn && !p.archiveBusy {
			p.archiving = agent.Workspace{}
		}
	}
	if p.commitOn {
		c.Key("commit")
		native.Dialog(c, &p.commitOn, 520, "Commit", func() { p.commitView(c, pal) })
		if !p.commitOn {
			p.closeWriting()
		}
	}
	if p.prOn {
		c.Key("pr")
		native.Dialog(c, &p.prOn, 560, "Create pull request", func() { p.prView(c, pal) })
		if !p.prOn {
			p.closeWriting()
		}
	}
	if p.trustOn {
		c.Key("trust")
		native.Dialog(c, &p.trustOn, 460, "Trust wada.json", func() { p.trustView(c, pal) })
	}
	if p.restoreOn {
		c.Key("restore")
		native.Dialog(c, &p.restoreOn, 440, "Restore workspace", func() { p.restoreView(c, pal) })
	}
}

func (p *Plugin) restoreView(c *ui.Context, pal native.Palette) {
	ui.Text(c, "Restore workspace").FontSize(14).FontWeight(600)
	ui.Text(c, "Archived workspaces of your projects. Restoring one makes its worktree again from its branch.").
		FontSize(12.5).TextColor(pal.Muted)
	switch {
	case p.goneState == "loading":
		ui.Text(c, "Reading archived workspaces…").FontSize(12).TextColor(pal.Faint)
	case p.goneState != "":
		ui.Text(c, p.goneState).FontSize(12).TextColor(pal.DangerText)
	case len(p.gone) == 0:
		ui.Text(c, "No workspace is archived.").FontSize(12.5).TextColor(pal.Faint)
	default:
		ui.Scroll(c.Key("archived")).FillWidth().MaxHeight(320).Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				for _, w := range p.gone {
					c.Key(string(w.ID))
					p.goneRow(c, pal, w)
				}
			})
		})
	}
	if p.restoreErr != "" {
		ui.Text(c, p.restoreErr).FontSize(12).TextColor(pal.DangerText)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Close", components.ButtonSecondary).OnClick(func() { p.restoreOn = false })
	})
}

func (p *Plugin) goneRow(c *ui.Context, pal native.Palette, w agent.Workspace) {
	detail := native.Tilde(w.Directory)
	if pr, ok := p.ws.Project(w.Project); ok {
		detail = pr.Name + " · " + detail
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(8, 10).Radius(8).
		Background(pal.Raised).Border(1, pal.Hairline).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, p.goneName(w)).FontSize(13).FontWeight(500).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, detail).FontSize(11).TextColor(pal.Faint).SingleLine().Ellipsis("…")
		})
		title := "Restore"
		if p.restoring == w.ID {
			title = "Restoring…"
		}
		components.Button(c, title, components.ButtonSecondary).Label("Restore " + p.goneName(w)).
			Disabled(p.restoring != "").OnClick(func() { p.restore(w.ID) })
	})
}

func (p *Plugin) newView(c *ui.Context, pal native.Palette) {
	ui.Text(c, "New workspace").FontSize(14).FontWeight(600)
	ui.Text(c, "A workspace is a folder of its own for a task: a Git worktree on its own branch.").
		FontSize(12.5).TextColor(pal.Muted)

	// The project.
	var options []onechat.Option
	label := "Choose a project"
	for _, pr := range p.ws.Projects() {
		if pr.Inbox {
			continue
		}
		options = append(options, onechat.Option{ID: string(pr.ID), Label: pr.Name, Detail: native.Tilde(pr.Root)})
		if pr.ID == p.project {
			label = pr.Name
		}
	}
	if len(options) == 0 {
		ui.Text(c, "Add a project first: New Workspace makes worktrees of a project's repository.").FontSize(12.5).TextColor(pal.Muted)
		components.Button(c, "Close", components.ButtonSecondary).OnClick(func() { p.newOn = false })
		return
	}
	row(c, pal, "Project", func() {
		c.Key("project")
		picked := onechat.Picker(c, &p.pickers[0], onechat.PickerOptions{Label: label, SearchLabel: "Search projects",
			EmptyText: "No projects match", Icon: lucide.Folder, Options: options, Selected: string(p.project), Width: 320})
		if id := picked.Chosen; id != "" {
			p.ctx.Post(func() {
				p.project, p.start, p.path, p.problem = agent.ProjectID(id), "", "", ""
				p.loadRefs()
			})
		}
	})

	// New or existing worktree.
	row(c, pal, "Isolation", func() {
		toggle(c, pal, "New worktree", !p.existing, func() { p.existing = false })
		toggle(c, pal, "Existing worktree", p.existing, func() { p.existing = true })
	})
	switch {
	case p.refsState == "loading":
		ui.Text(c, "Reading the repository…").FontSize(12).TextColor(pal.Faint)
	case p.refsState != "":
		ui.Text(c, p.refsState).FontSize(12).TextColor(pal.DangerText)
	case p.existing:
		p.existingView(c, pal)
	default:
		p.startView(c, pal)
	}

	if !p.existing {
		row(c, pal, "Name", func() {
			components.TextBox(c, &p.name, components.TextBoxOptions{}).Grow(1).Label("Name").FontSize(13)
		})
		ui.Text(c, "Optional: names the workspace, its folder and a new branch.").FontSize(11.5).TextColor(pal.Faint)
	}
	if p.problem != "" {
		ui.Text(c, p.problem).FontSize(12).TextColor(pal.DangerText)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.newOn = false })
		title := "Create"
		if p.creating {
			title = "Creating…"
		}
		_, err := p.request()
		components.Button(c, title, components.ButtonPrimary).Disabled(err != nil || p.creating || p.refsState != "").OnClick(p.create)
	})
}

// startView picks what a new worktree starts from.
func (p *Plugin) startView(c *ui.Context, pal native.Palette) {
	var options []onechat.Option
	label := "Choose a branch"
	add := func(id, name, detail string) {
		if id == p.start {
			label = name
		}
		options = append(options, onechat.Option{ID: id, Label: name, Detail: detail})
	}
	seen := map[string]bool{}
	ref := func(name, detail string) {
		if name != "" && !seen[name] {
			seen[name] = true
			add("ref:"+name, name, detail)
		}
	}
	ref(p.refs.Default, "Default")
	for _, pr := range p.refs.PRs {
		detail := "Pull request · " + pr.Branch
		if pr.Fork {
			detail = "Pull request from a fork"
		}
		add("pr:"+strconv.Itoa(pr.Number), "#"+strconv.Itoa(pr.Number)+" "+pr.Title, detail)
	}
	for _, r := range p.refs.Remotes {
		ref(r, "Remote branch")
	}
	for _, b := range p.refs.Branches {
		ref(b, "Branch")
	}
	row(c, pal, "Start from", func() {
		c.Key("start")
		picked := onechat.Picker(c, &p.pickers[1], onechat.PickerOptions{Label: label, SearchLabel: "Search branches and pull requests",
			EmptyText: "Nothing matches", Icon: lucide.GitBranch, Options: options, Selected: p.start, Width: 340, Searchable: 1})
		if id := picked.Chosen; id != "" {
			p.ctx.Post(func() { p.start = id })
		}
	})
	if strings.HasPrefix(p.start, "pr:") {
		ui.Text(c, "Checks out the pull request's branch. One from a fork gets a local copy that pushes nowhere.").
			FontSize(11.5).TextColor(pal.Faint)
		return
	}
	row(c, pal, "", func() {
		toggle(c, pal, "Branch off", !p.checkout, func() { p.checkout = false })
		toggle(c, pal, "Check out", p.checkout, func() { p.checkout = true })
	})
	if p.checkout {
		ui.Text(c, "Works on the branch itself; a branch another worktree has is opened from there instead.").
			FontSize(11.5).TextColor(pal.Faint)
	} else {
		ui.Text(c, "Starts a new branch from it.").FontSize(11.5).TextColor(pal.Faint)
	}
}

// existingView picks a worktree already on disk that is no workspace yet.
func (p *Plugin) existingView(c *ui.Context, pal native.Palette) {
	var options []onechat.Option
	label := "Choose a worktree"
	for _, wt := range p.refs.Worktrees {
		if _, taken := p.ws.At(wt.Path); taken || wt.IsMain {
			continue
		}
		name := cmp.Or(wt.Branch, "detached")
		if wt.Path == p.path {
			label = name
		}
		options = append(options, onechat.Option{ID: wt.Path, Label: name, Detail: native.Tilde(wt.Path)})
	}
	if len(options) == 0 {
		ui.Text(c, "The repository has no other worktree that isn't a workspace already.").FontSize(12).TextColor(pal.Faint)
		return
	}
	row(c, pal, "Worktree", func() {
		c.Key("worktree")
		picked := onechat.Picker(c, &p.pickers[2], onechat.PickerOptions{Label: label, SearchLabel: "Search worktrees",
			EmptyText: "No worktrees match", Icon: lucide.FolderGit2, Options: options, Selected: p.path, Width: 340})
		if id := picked.Chosen; id != "" {
			p.ctx.Post(func() { p.path = id })
		}
	})
}

func (p *Plugin) archiveView(c *ui.Context, pal native.Palette) {
	name := p.ws.Name(p.archiving.Directory)
	ui.Text(c, "Archive “"+name+"”?").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
	if p.unsaved != "" {
		ui.Text(c, strings.TrimPrefix(p.unsaved, agent.ErrWorkUnsaved.Error()+": ")+
			". Archiving discards it; the branch stays.").FontSize(12.5).TextColor(pal.DangerText)
	} else {
		detail := "Its conversations stop and its tabs close; they stay with the agent. Its branch stays."
		if p.archiving.Isolation == agent.IsolateWorktree {
			detail = "Its worktree is removed, its conversations stop and its tabs close. Its branch stays, so it can be restored."
		}
		ui.Text(c, detail).FontSize(12.5).TextColor(pal.Muted)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.archiveOn = false })
		if p.unsaved != "" {
			components.Button(c, "Discard and archive", components.ButtonPrimary).Disabled(p.archiveBusy).OnClick(func() { p.archive(true) })
			return
		}
		components.Button(c, "Archive", components.ButtonPrimary).Disabled(p.archiveBusy).OnClick(func() { p.archive(false) })
	})
}

// section is the work panel's Workspace section: a worktree's branch, base
// and pull request, and the wada.json's setup and services.
func (p *Plugin) section(c *ui.Context) {
	w, ok := p.here()
	if !ok {
		return
	}
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if w.Isolation != agent.IsolateProject {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, lucide.GitBranch()).FontSize(12).TextColor(pal.Muted).Shrink(0)
				ui.Text(c, cmp.Or(w.Branch, "detached")).FontSize(12.5).FontWeight(500).TextColor(pal.Text).
					Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if p.pr != nil {
					p.prBadge(c, pal, p.pr)
				}
			})
			if w.BaseRef != "" {
				ui.Text(c, "from "+w.BaseRef).FontSize(11).TextColor(pal.Faint).SingleLine().Ellipsis("…")
			}
			ui.Text(c, native.Tilde(w.Directory)).FontSize(11).TextColor(pal.Faint).SingleLine().Ellipsis("…")
		}
		if w.ID == p.cfgFor && p.cfg.Found {
			c.Key("config")
			p.configView(c, pal, w)
		}
		if w.Isolation != agent.IsolateProject {
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				c.Key("commit")
				components.Button(c, "Commit…", components.ButtonSecondary).OnClick(func() { p.openCommit(w) })
				if p.pr == nil && w.BaseRef != "" {
					c.Key("pr")
					components.Button(c, "Create PR…", components.ButtonSecondary).OnClick(func() { p.openPR(w) })
				}
			})
			c.Key("archive")
			components.Button(c, "Archive workspace…", components.ButtonSecondary).OnClick(func() { p.askArchive(w) })
		}
	})
}

// configView is the wada.json's setup and services.
func (p *Plugin) configView(c *ui.Context, pal native.Palette, w agent.Workspace) {
	cfg := p.cfg
	ui.Column(c).FillWidth().Gap(4).Padding(4, 0).Children(func() {
		if cfg.Problem != "" {
			ui.Text(c, cfg.Problem).FontSize(11.5).TextColor(pal.DangerText)
			return
		}
		if !cfg.Trusted {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, "wada.json isn’t trusted yet").FontSize(11.5).TextColor(pal.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				c.Key("trust")
				link(c, pal, "Review…", "Review wada.json", func() { p.askTrust(w, cfg, false, "") })
			})
		}
		if len(cfg.Setup) > 0 {
			c.Key("setup")
			p.setupRow(c, pal, w, cfg.SetupRun)
		}
		for _, svc := range cfg.Services {
			c.Key("service:" + svc.Name)
			p.serviceRow(c, pal, w, svc)
		}
	})
}

func (p *Plugin) setupRow(c *ui.Context, pal native.Palette, w agent.Workspace, run *agent.Run) {
	state, color := "Setup not run", pal.Faint
	switch {
	case run == nil:
	case run.Running:
		state, color = "Setup running…", pal.Orange
	case run.ExitCode == 0:
		state, color = "Setup finished", pal.GreenBadge
	default:
		state, color = "Setup failed (exit "+strconv.Itoa(run.ExitCode)+")", pal.DangerText
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Height(24).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0)
		ui.Text(c, state).FontSize(12).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if run != nil {
			c.Key("show")
			link(c, pal, "Output", "Show the setup’s output", func() { p.showTerminal(w, run.Terminal) })
		}
		if run == nil || !run.Running {
			label := "Run"
			if run != nil {
				label = "Run again"
			}
			c.Key("run")
			link(c, pal, label, label+" setup", func() { p.runSetup(w) })
		}
	})
}

func (p *Plugin) serviceRow(c *ui.Context, pal native.Palette, w agent.Workspace, svc agent.WorkspaceService) {
	running := svc.Run != nil && svc.Run.Running
	color := pal.Faint
	if running {
		color = pal.GreenBadge
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Height(24).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0)
		ui.Text(c, svc.Name).FontSize(12).TextColor(pal.Text).Tooltip(svc.Command).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		ui.Text(c, ":"+strconv.Itoa(svc.Port)).FontSize(11).TextColor(pal.Faint).FontFeatures("tnum").Grow(1).SingleLine()
		if running {
			url := "http://localhost:" + strconv.Itoa(svc.Port)
			c.Key("open")
			link(c, pal, "Open", "Open "+url+" in a tab", func() { p.openService(w, url) })
		}
		if svc.Run != nil {
			c.Key("show")
			link(c, pal, "Output", "Show "+svc.Name+"’s output", func() { p.showTerminal(w, svc.Run.Terminal) })
		}
		c.Key("toggle")
		if running {
			link(c, pal, "Stop", "Stop "+svc.Name, func() { p.stopService(w, svc.Name) })
		} else {
			link(c, pal, "Start", "Start "+svc.Name, func() { p.startService(w, svc.Name) })
		}
	})
}

// trustView shows what a wada.json runs before the user allows it.
func (p *Plugin) trustView(c *ui.Context, pal native.Palette) {
	cfg := p.trustCfg
	title := "Trust this wada.json?"
	if p.trustThen == "setup" {
		title = "Run this workspace’s setup?"
	}
	ui.Text(c, title).FontSize(14).FontWeight(600)
	name := p.ws.Name(p.trustWs.Directory)
	ui.Text(c, "wada.json in “"+name+"” runs these commands on this computer, as you:").FontSize(12.5).TextColor(pal.Muted)
	if p.trustFork {
		ui.Text(c, "This pull request comes from a fork: its wada.json, and the scripts its commands run, come from someone else.").
			FontSize(12.5).TextColor(pal.DangerText)
	}
	ui.Column(c).FillWidth().Gap(2).Padding(8, 10).Radius(6).Background(pal.DetailBg).Children(func() {
		for i, cmd := range cfg.Setup {
			c.Key("setup:" + strconv.Itoa(i))
			ui.Text(c, cmd).Font("monospace").FontSize(11.5).TextColor(pal.Text)
		}
		for _, svc := range cfg.Services {
			c.Key("service:" + svc.Name)
			ui.Text(c, svc.Name+": "+svc.Command).Font("monospace").FontSize(11.5).TextColor(pal.Muted)
		}
		for i, cmd := range cfg.Teardown {
			c.Key("teardown:" + strconv.Itoa(i))
			ui.Text(c, "before archiving: "+cmd).Font("monospace").FontSize(11.5).TextColor(pal.Muted)
		}
	})
	ui.Text(c, "Trust lasts until the file changes.").FontSize(11.5).TextColor(pal.Faint)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Not now", components.ButtonSecondary).OnClick(func() { p.trustOn = false })
		label := "Trust"
		if p.trustThen != "" {
			label = "Trust and run"
		}
		components.Button(c, label, components.ButtonPrimary).OnClick(p.trust)
	})
}

func (p *Plugin) commitView(c *ui.Context, pal native.Palette) {
	ui.Text(c, "Commit “"+p.ws.Name(p.commitWs.Directory)+"”").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
	ui.Text(c, "Commits every change in the workspace, new files included.").FontSize(12.5).TextColor(pal.Muted)
	c.Key("message")
	box := components.TextBox(c, &p.commitMsg, components.TextBoxOptions{Multiline: true}).FillWidth().Height(160).FontSize(13)
	if p.drafting && p.commitMsg == "" {
		box.Placeholder("Writing a message…")
	} else {
		box.Placeholder("Commit message")
	}
	p.writeFooter(c, pal, "Commit", strings.TrimSpace(p.commitMsg) != "", p.commit)
}

func (p *Plugin) prView(c *ui.Context, pal native.Palette) {
	w := p.prWs
	ui.Text(c, "Create a pull request").FontSize(14).FontWeight(600)
	ui.Text(c, "Pushes "+cmp.Or(w.Branch, "the branch")+" and opens a pull request into "+strings.TrimPrefix(w.BaseRef, "origin/")+".").
		FontSize(12.5).TextColor(pal.Muted)
	placeholder := "Title"
	if p.drafting && p.prTitle == "" {
		placeholder = "Writing a title…"
	}
	c.Key("title")
	components.TextBox(c, &p.prTitle, components.TextBoxOptions{}).FillWidth().Placeholder(placeholder).FontSize(13)
	c.Key("body")
	components.TextBox(c, &p.prBody, components.TextBoxOptions{Multiline: true}).FillWidth().Height(200).
		Placeholder("Description").FontSize(13)
	row(c, pal, "", func() {
		toggle(c, pal, "Ready for review", !p.prDraft, func() { p.prDraft = false })
		toggle(c, pal, "Draft", p.prDraft, func() { p.prDraft = true })
	})
	p.writeFooter(c, pal, "Create pull request", strings.TrimSpace(p.prTitle) != "", p.createPR)
}

// writeFooter is a writing dialog's problem and buttons.
func (p *Plugin) writeFooter(c *ui.Context, pal native.Palette, action string, ready bool, run func()) {
	if p.writeProblem != "" {
		ui.Text(c, p.writeProblem).FontSize(12).TextColor(pal.DangerText)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		components.Button(c, "Cancel", components.ButtonSecondary).OnClick(p.closeWriting)
		label := action
		if p.submitting {
			label += "…"
		}
		components.Button(c, label, components.ButtonPrimary).Disabled(!ready || p.submitting).OnClick(run)
	})
}

// link is a small text button.
func link(c *ui.Context, pal native.Palette, label, name string, on func()) {
	b := ui.ButtonBase(c).Label(name).Height(20).Padding(0, 5).Radius(4).Center().Shrink(0).Cursor(ui.CursorPointer).OnClick(on)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() { ui.Text(c, label).FontSize(11.5).TextColor(pal.Muted).SingleLine() })
}

func (p *Plugin) prBadge(c *ui.Context, pal native.Palette, pr *agent.PRStatus) {
	col := pal.Muted
	switch pr.Checks {
	case "pass":
		col = pal.GreenBadge
	case "fail":
		col = pal.DangerText
	case "pending":
		col = pal.Orange
	}
	label := "#" + strconv.Itoa(pr.Number)
	if pr.Checks != "" && pr.Checks != "none" {
		label += " · " + pr.Checks
	}
	c.Key("pr")
	b := ui.ButtonBase(c).Label("Open PR "+label).Tooltip(cmp.Or(pr.Title, pr.URL)).Height(20).Padding(0, 6).Radius(99).
		Background(col.Alpha(.15)).Center().Cursor(ui.CursorPointer).Shrink(0).
		OnClick(func() {
			if ui.SafeToOpen(pr.URL) {
				p.host.OpenURL(pr.URL)
			}
		})
	b.Children(func() { ui.Text(c, label).FontSize(10.5).FontWeight(500).TextColor(col).SingleLine() })
}

// row lays out a labeled line of the dialog.
func row(c *ui.Context, pal native.Palette, label string, fn func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted).Width(80).Shrink(0)
		fn()
	})
}

// toggle is one of a pair of buttons, filled while on.
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
