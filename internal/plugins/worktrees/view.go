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

// section is the work panel's Workspace section.
func (p *Plugin) section(c *ui.Context) {
	w, ok := p.current()
	if !ok {
		return
	}
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
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
		c.Key("archive")
		components.Button(c, "Archive workspace…", components.ButtonSecondary).OnClick(func() { p.askArchive(w) })
	})
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
