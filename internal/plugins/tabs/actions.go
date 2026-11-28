package tabs

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/tabs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// What a tab's menu and the space switcher do: rename and delete a
// conversation; remove a project.

// tabMenu fills a tab's context menu. A menu is built while rendering, so
// anything that navigates or changes the record waits for the frame to end.
func (p *Plugin) tabMenu(m *ui.Menu, t tabs.Tab) {
	if t.Kind == tabs.Conversation {
		if m.Item("Rename…").Chosen() {
			p.renaming, p.title, p.renameOn = t, t.Title, true
		}
	}
	if t.Kind == tabs.Pane {
		if p.beside[t.Folder] == t.ID {
			if m.Item("Remove from Split").Chosen() {
				p.ctx.Post(func() { p.closeSplit(t.Folder) })
			}
		} else if p.chatFor(t.Folder) != "" && m.Item("Show Beside Chat").Chosen() {
			p.ctx.Post(func() { p.showBeside(t) })
		}
	}
	if m.Item("Close Tab").Chosen() {
		p.ctx.Post(func() { p.Close(t.ID) })
	}
	if t.Kind != tabs.Conversation {
		return
	}
	m.Separator()
	if m.Item("Delete…").Chosen() {
		p.deleting, p.deleteOn = t, true
	}
}

// tabItems lists tabs in a menu; choosing one selects it.
func (p *Plugin) tabItems(m *ui.Menu, list []tabs.Tab) {
	for _, t := range list {
		if m.Item(t.Title).Chosen() {
			p.ctx.Post(func() { p.Select(t.ID) })
		}
	}
}

func (p *Plugin) saveRename() {
	t, title := p.renaming, strings.TrimSpace(p.title)
	p.renaming, p.renameOn = tabs.Tab{}, false
	if title == "" || title == t.Title {
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Rename(ctx, t.Session, title)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not renamed", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.ws.Retitle(t.Session, title)
	})
}

func (p *Plugin) confirmDelete() {
	t := p.deleting
	p.deleting, p.deleteOn = tabs.Tab{}, false
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Delete(ctx, t.Session)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not deleted", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.forget(t)
	})
}

// forget closes a deleted conversation's tab and drops it from the list.
func (p *Plugin) forget(t tabs.Tab) {
	p.Close(t.ID)
	p.ws.Forget(t.Session)
}

// confirmRemove archives the space's project, then closes the tabs of its
// workspaces.
func (p *Plugin) confirmRemove() {
	space := p.removing
	p.removing, p.removeOn = "", false
	w, ok := p.ws.At(space)
	if !ok {
		return
	}
	var dirs []string
	for _, x := range p.ws.Workspaces() {
		if x.Project == w.Project {
			dirs = append(dirs, x.Directory)
		}
	}
	p.ws.ArchiveProject(w.Project, func() {
		var ids []string
		for _, t := range p.tabs {
			if t.Kind != tabs.Page && slices.Contains(dirs, t.Folder) {
				ids = append(ids, t.ID)
			}
		}
		for _, id := range ids {
			p.Close(id)
		}
	})
}

func (p *Plugin) dialogs(c *ui.Context) {
	pal := native.PaletteFor(c)
	if p.renaming.ID != "" {
		native.Dialog(c, &p.renameOn, 360, "Rename conversation", func() {
			ui.Text(c, "Rename conversation").FontSize(14).FontWeight(600)
			field := components.TextBox(c, &p.title, components.TextBoxOptions{}).FillWidth().Label("Title").FontSize(13)
			field.OnSubmit(p.saveRename)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.renaming, p.renameOn = tabs.Tab{}, false })
				components.Button(c, "Save", components.ButtonPrimary).OnClick(p.saveRename)
			})
		})
		if !p.renameOn {
			p.renaming = tabs.Tab{}
		}
	}
	if p.deleting.ID != "" {
		native.Dialog(c, &p.deleteOn, 360, "Delete conversation", func() {
			ui.Text(c, "Delete “"+p.deleting.Title+"”?").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, "The conversation is removed from the agent for good.").FontSize(12.5).TextColor(pal.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.deleting, p.deleteOn = tabs.Tab{}, false })
				components.Button(c, "Delete", components.ButtonPrimary).OnClick(p.confirmDelete)
			})
		})
		if !p.deleteOn {
			p.deleting = tabs.Tab{}
		}
	}
	if p.removing != "" {
		name := p.spaceName(p.removing)
		native.Dialog(c, &p.removeOn, 380, "Remove project", func() {
			ui.Text(c, "Remove “"+name+"” from Wada?").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, p.removeDetail()).FontSize(12.5).TextColor(pal.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.removing, p.removeOn = "", false })
				components.Button(c, "Remove", components.ButtonPrimary).OnClick(p.confirmRemove)
			})
		})
		if !p.removeOn {
			p.removing = ""
		}
	}
}

func (p *Plugin) removeDetail() string {
	count := 0
	for _, conv := range p.ws.Conversations() {
		if conv.Directory == p.removing {
			count++
		}
	}
	detail := "Its tabs close. Nothing on disk changes, and adding the folder again brings it back."
	switch count {
	case 0:
	case 1:
		detail = "Its conversation stays with the agent but is no longer listed here. " + detail
	default:
		detail = "Its " + strconv.Itoa(count) + " conversations stay with the agent but are no longer listed here. " + detail
	}
	return detail
}
