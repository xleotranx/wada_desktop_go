package tabs

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// What a tab's menu and the space switcher do: rename, forget and delete a
// conversation; remove a folder.

// tabMenu fills a tab's context menu. A menu is built while rendering, so
// anything that navigates or changes the record waits for the frame to end.
func (p *Plugin) tabMenu(m *ui.Menu, t tabs.Tab) {
	if t.Kind == tabs.Conversation {
		if m.Item("Rename…").Chosen() {
			p.renaming, p.title, p.renameOn = t, t.Title, true
		}
	}
	if m.Item("Close Tab").Chosen() {
		p.ctx.Post(func() { p.Close(t.ID) })
	}
	if t.Kind != tabs.Conversation {
		return
	}
	if m.Item("Remove from List").Chosen() {
		p.ctx.Post(func() { p.forget(t) })
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
			p.host.Toast(ui.Toast{Title: "The conversation was not deleted", Description: err.Error() +
				". It may already be gone; “Remove from List” drops it from All chats.", Type: components.ToastError})
			return
		}
		p.forget(t)
	})
}

// forget closes a conversation's tab and drops it from the record; the
// backend keeps whatever it has.
func (p *Plugin) forget(t tabs.Tab) {
	p.Close(t.ID)
	p.ws.Forget(t.Session)
}

// confirmRemove closes the folder's tabs and forgets the folder and its
// conversations.
func (p *Plugin) confirmRemove() {
	folder := p.removing
	p.removing, p.removeOn = "", false
	var ids []string
	for _, t := range p.tabs {
		if t.Folder == folder {
			ids = append(ids, t.ID)
		}
	}
	for _, id := range ids {
		p.Close(id)
	}
	p.ws.RemoveFolder(folder)
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
		name := filepath.Base(p.removing)
		native.Dialog(c, &p.removeOn, 380, "Remove folder", func() {
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
		if conv.Folder == p.removing {
			count++
		}
	}
	detail := "Its tabs close. Nothing on disk changes."
	switch count {
	case 0:
	case 1:
		detail = "Its conversation stays with the agent but is no longer listed here. " + detail
	default:
		detail = "Its " + strconv.Itoa(count) + " conversations stay with the agent but are no longer listed here. " + detail
	}
	return detail
}
