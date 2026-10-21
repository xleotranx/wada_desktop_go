// Package sessions is the sidebar's conversation list: the conversations
// started in this app, grouped by folder and newest first, searchable by
// title, with rename, delete and an unread mark for conversations that
// finished a turn while another was open. The list comes from the workspace
// record, not from the backend.
package sessions

import (
	"cmp"
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Plugin is the conversation list. The zero value is ready to install.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller
	host  shell.Host
	ws    workspace.Service

	unread map[agent.SessionID]bool
	query  string

	renaming *workspace.Conversation
	title    string
	renameOn bool
	deleting *workspace.Conversation
	deleteOn bool
	removing string // the folder whose removal is being confirmed
	removeOn bool

	list      ui.ListState
	rows      []item // laid out for rowsQuery
	rowsQuery string
	rowsStale bool // the record changed since
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "sessions", Title: "Conversations",
		Description: "The conversation list in the sidebar, grouped by folder.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey, workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.unread, p.rows, p.rowsStale = map[agent.SessionID]bool{}, nil, true
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, workspace.Changed, func(struct{}) { p.rowsStale = true })
	kernel.On(c, chat.Changed, func(conv chat.Conversation) { delete(p.unread, conv.Session) })
	for _, err := range []error{
		kernel.Contribute(c, shell.Sidebar, "sessions.list", 10, shell.View{Render: p.view, Grow: true}),
		kernel.Contribute(c, shell.Overlays, "sessions.dialogs", 0, shell.View{Render: p.dialogs}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) events(batch []agent.Event) {
	current := p.chat.Current().Session
	for _, ev := range batch {
		switch ev.Kind {
		case agent.TurnSucceeded, agent.TurnFailed:
			if _, known := p.ws.Lookup(ev.Session); known && ev.Session != current {
				p.unread[ev.Session] = true
			}
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested, agent.TurnStarted,
			agent.TurnInterrupted, agent.ModelChanged, agent.SessionRenamed:
		}
	}
}

func (p *Plugin) open(conv workspace.Conversation) {
	delete(p.unread, conv.ID)
	p.chat.Open(conv.Session())
}

// newChat starts a chat in the folder of the one on show, as ⌘N does.
func (p *Plugin) newChat() { p.chat.NewChat(p.chat.Current().Directory) }

func (p *Plugin) openFolder() { p.ws.ChooseFolder(p.chat.NewChat) }

// ---- Rename, delete, remove

func (p *Plugin) startRename(conv workspace.Conversation) {
	p.renaming, p.title, p.renameOn = &conv, conv.Title, true
}

func (p *Plugin) saveRename() {
	conv, title := *p.renaming, strings.TrimSpace(p.title)
	p.renaming, p.renameOn = nil, false
	if title == "" || title == conv.Title {
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Rename(ctx, conv.ID, title)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not renamed", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.ws.Retitle(conv.ID, title)
	})
}

func (p *Plugin) startDelete(conv workspace.Conversation) { p.deleting, p.deleteOn = &conv, true }

func (p *Plugin) confirmDelete() {
	conv := *p.deleting
	p.deleting, p.deleteOn = nil, false
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Delete(ctx, conv.ID)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not deleted", Description: err.Error() +
				". It may already be gone; “Remove from List” drops it from the sidebar.", Type: components.ToastError})
			return
		}
		p.forget(conv)
	})
}

// forget drops a conversation from the list; the backend keeps whatever it has.
func (p *Plugin) forget(conv workspace.Conversation) {
	p.ws.Forget(conv.ID)
	delete(p.unread, conv.ID)
	if p.chat.Current().Session == conv.ID {
		p.chat.NewChat(conv.Folder)
	}
}

func (p *Plugin) startRemove(folder string) { p.removing, p.removeOn = folder, true }

func (p *Plugin) confirmRemove() {
	folder := p.removing
	p.removing, p.removeOn = "", false
	if p.chat.Current().Directory == folder {
		p.chat.NewChat("")
	}
	p.ws.RemoveFolder(folder)
}

// ---- Rows

type itemKind uint8

const (
	itemFolder itemKind = iota // a folder's header
	itemOther                  // the header over chats without a folder
	itemChat
	itemEmpty // a folder without conversations
)

type item struct {
	kind   itemKind
	folder workspace.Folder
	conv   workspace.Conversation
	nested bool // under a header
}

func (it item) key() any {
	switch it.kind {
	case itemFolder:
		return "folder:" + it.folder.Path
	case itemOther:
		return "other"
	case itemEmpty:
		return "empty:" + it.folder.Path
	case itemChat:
	}
	return it.conv.ID
}

func titleOf(c workspace.Conversation) string { return cmp.Or(c.Title, "Untitled chat") }

// items lays the list out: each folder's header and, unless it is collapsed,
// its conversations; then the conversations without a folder. A search shows
// only matching conversations, under their folders, whether collapsed or not.
func (p *Plugin) items() []item {
	q := strings.ToLower(strings.TrimSpace(p.query))
	folders := p.ws.Folders()
	byFolder := map[string][]workspace.Conversation{}
	var other []workspace.Conversation
	known := map[string]bool{}
	for _, f := range folders {
		known[f.Path] = true
	}
	for _, c := range p.ws.Conversations() {
		if q != "" && !strings.Contains(strings.ToLower(titleOf(c)), q) {
			continue
		}
		if known[c.Folder] {
			byFolder[c.Folder] = append(byFolder[c.Folder], c)
		} else {
			other = append(other, c)
		}
	}
	var out []item
	for _, f := range folders {
		convs := byFolder[f.Path]
		if q != "" && len(convs) == 0 {
			continue
		}
		out = append(out, item{kind: itemFolder, folder: f})
		switch {
		case f.Collapsed && q == "":
		case len(convs) == 0:
			out = append(out, item{kind: itemEmpty, folder: f, nested: true})
		default:
			for _, c := range convs {
				out = append(out, item{kind: itemChat, conv: c, nested: true})
			}
		}
	}
	if len(other) > 0 && len(out) > 0 {
		out = append(out, item{kind: itemOther})
	}
	nested := len(out) > 0
	for _, c := range other {
		out = append(out, item{kind: itemChat, conv: c, nested: nested})
	}
	return out
}

// layout is the list's rows, laid out again only after the record or the
// search changed.
func (p *Plugin) layout() []item {
	if p.rowsStale || p.query != p.rowsQuery {
		p.rows, p.rowsQuery, p.rowsStale = p.items(), p.query, false
	}
	return p.rows
}

// ---- View

func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).Fill().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(2).AlignItems(ui.Center).Children(func() {
			newChat := ui.ButtonBase(c).Label("New chat").Height(34).Radius(8).Padding(0, 8).Grow(1).MinWidth(0).
				Justify(ui.Start).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer)
			if newChat.Hovered() {
				newChat.Background(pal.Hover)
			}
			newChat.OnClick(p.newChat)
			newChat.Children(func() {
				ui.Icon(c, lucide.SquarePen()).FontSize(14).TextColor(pal.Text)
				ui.Text(c, "New chat").FontSize(13.5).TextColor(pal.Text)
			})
			add := ui.ButtonBase(c).Label("Open folder…").Size(34, 34).Radius(8).Center().Shrink(0).Cursor(ui.CursorPointer)
			if add.Hovered() {
				add.Background(pal.Hover)
			}
			add.Tooltip("Open folder…")
			add.OnClick(p.openFolder)
			add.Children(func() { ui.Icon(c, lucide.FolderPlus()).FontSize(15).TextColor(pal.Muted) })
		})
		components.TextBox(c, &p.query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).
			FillWidth().Label("Search conversations").Placeholder("Search").FontSize(13)

		if !p.ws.Loaded() {
			ui.Text(c, "Loading conversations…").FontSize(12).TextColor(pal.Faint).Padding(8, 8)
			return
		}
		rows := p.layout()
		if len(rows) == 0 {
			empty := "No conversations yet. Open a folder to keep its chats together."
			if strings.TrimSpace(p.query) != "" {
				empty = "No matches"
			}
			ui.Text(c, empty).FontSize(12).TextColor(pal.Faint).Padding(8, 8)
			return
		}
		ui.List(c, &p.list, len(rows)).Grow(1).MinHeight(0).FillWidth().Gap(1).
			ItemKey(func(i int) any { return rows[i].key() }).
			Rows(func(r ui.ListRow) {
				it := rows[r.Index]
				switch it.kind {
				case itemFolder:
					p.folderRow(r.Context, pal, it.folder)
				case itemOther:
					ui.Text(r.Context, "Other chats").FontSize(12).FontWeight(600).TextColor(pal.Faint).
						Padding(10, 8, 4, 8).SingleLine()
				case itemEmpty:
					ui.Text(r.Context, "No conversations").FontSize(12).TextColor(pal.Faint).Padding(6, 8, 6, 30)
				case itemChat:
					p.row(r.Context, pal, it.conv, it.nested)
				}
			})
	})
}

// folderRow is a folder's header: it collapses the folder, and its buttons
// start a chat there or remove it.
func (p *Plugin) folderRow(c *ui.Context, pal native.Palette, f workspace.Folder) {
	name := filepath.Base(f.Path)
	b := ui.ButtonBase(c).Label(name).Height(30).Radius(8).Padding(0, 4, 0, 8).FillWidth().
		Justify(ui.Start).AlignItems(ui.Center).Gap(6).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Tooltip(native.Tilde(f.Path))
	b.OnClick(func() { p.ws.SetCollapsed(f.Path, !f.Collapsed) })
	b.Children(func() {
		chevron, icon := lucide.ChevronDown, lucide.FolderOpen
		if f.Collapsed {
			chevron, icon = lucide.ChevronRight, lucide.Folder
		}
		ui.Icon(c, chevron()).FontSize(12).TextColor(pal.Faint).Shrink(0)
		ui.Icon(c, icon()).FontSize(14).TextColor(pal.Muted).Shrink(0)
		ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(pal.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		show := b.Hovered() || b.FocusWithin()

		c.Key("new")
		add := ui.ButtonBase(c).Label("New chat in "+name).Size(22, 22).Radius(6).Center().Shrink(0)
		add.OnClick(func() { p.chat.NewChat(f.Path) })
		add.Tooltip("New chat in " + name)
		c.Key("options")
		kebab := ui.ButtonBase(c).Label("Options for "+name).Size(22, 22).Radius(6).Center().Shrink(0)
		kebab.Menu(func(m *ui.Menu) {
			// A menu is drawn while rendering; act once the frame is done.
			if m.Item("New Chat").Chosen() {
				p.ctx.Post(func() { p.chat.NewChat(f.Path) })
			}
			m.Separator()
			if m.Item("Remove Folder…").Chosen() {
				p.startRemove(f.Path)
			}
		})
		kebab.Tooltip("Folder options")
		show = show || add.Hovered() || add.Focused() || kebab.Hovered() || kebab.Focused()
		for _, btn := range []ui.Element{add, kebab} {
			if btn.Hovered() {
				btn.Background(pal.Hover)
			}
			// Keep the controls mounted so Tab can reveal them.
			if !show {
				btn.Opacity(0)
			}
		}
		add.Children(func() { ui.Icon(c, lucide.Plus()).FontSize(13).TextColor(pal.Muted) })
		kebab.Children(func() { ui.Icon(c, lucide.Ellipsis()).FontSize(13).TextColor(pal.Muted) })
	})
}

func (p *Plugin) row(c *ui.Context, pal native.Palette, conv workspace.Conversation, nested bool) {
	title := titleOf(conv)
	current := conv.ID == p.chat.Current().Session
	left := float32(8)
	if nested {
		left = 30
	}
	b := ui.ButtonBase(c).Label(title).Height(32).Radius(8).Padding(0, 4, 0, left).FillWidth().
		Justify(ui.Start).AlignItems(ui.Center).Gap(6).Cursor(ui.CursorPointer)
	switch {
	case current:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.Tooltip(title)
	b.OnClick(func() { p.open(conv) })
	b.Children(func() {
		ui.Text(c, title).FontSize(13.5).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if p.unread[conv.ID] {
			ui.Box(c).Size(7, 7).Radius(4).Background(pal.Orange).Shrink(0).Label("Unread")
		}
		c.Key("options")
		kebab := ui.ButtonBase(c).Label("Options for "+title).Size(22, 22).Radius(6).Center().Shrink(0)
		kebab.Menu(func(m *ui.Menu) {
			if m.Item("Rename…").Chosen() {
				p.startRename(conv)
			}
			if m.Item("Remove from List").Chosen() {
				p.ctx.Post(func() { p.forget(conv) }) // not while rendering
			}
			m.Separator()
			if m.Item("Delete…").Chosen() {
				p.startDelete(conv)
			}
		})
		if kebab.Hovered() {
			kebab.Background(pal.Hover)
		}
		// Keep the control mounted so Tab can reveal it without moving the title.
		if !current && !b.Hovered() && !b.FocusWithin() && !kebab.Focused() && !kebab.Hovered() {
			kebab.Opacity(0)
		}
		kebab.Tooltip("Conversation options")
		kebab.Children(func() { ui.Icon(c, lucide.Ellipsis()).FontSize(13).TextColor(pal.Muted) })
	})
}

func (p *Plugin) dialogs(c *ui.Context) {
	pal := native.PaletteFor(c)
	if p.renaming != nil {
		native.Dialog(c, &p.renameOn, 360, "Rename conversation", func() {
			ui.Text(c, "Rename conversation").FontSize(14).FontWeight(600)
			field := components.TextBox(c, &p.title, components.TextBoxOptions{}).FillWidth().Label("Title").FontSize(13)
			field.OnSubmit(p.saveRename)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.renaming, p.renameOn = nil, false })
				components.Button(c, "Save", components.ButtonPrimary).OnClick(p.saveRename)
			})
		})
		if !p.renameOn {
			p.renaming = nil
		}
	}
	if p.deleting != nil {
		title := titleOf(*p.deleting)
		native.Dialog(c, &p.deleteOn, 360, "Delete conversation", func() {
			ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, "The conversation is removed from the agent for good.").FontSize(12.5).TextColor(pal.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				components.Button(c, "Cancel", components.ButtonSecondary).OnClick(func() { p.deleting, p.deleteOn = nil, false })
				components.Button(c, "Delete", components.ButtonPrimary).OnClick(p.confirmDelete)
			})
		})
		if !p.deleteOn {
			p.deleting = nil
		}
	}
	if p.removing != "" {
		name, count := filepath.Base(p.removing), 0
		for _, conv := range p.ws.Conversations() {
			if conv.Folder == p.removing {
				count++
			}
		}
		detail := "Nothing on disk changes."
		switch count {
		case 0:
		case 1:
			detail = "Its conversation stays with the agent but is no longer listed here. " + detail
		default:
			detail = "Its " + strconv.Itoa(count) + " conversations stay with the agent but are no longer listed here. " + detail
		}
		native.Dialog(c, &p.removeOn, 380, "Remove folder", func() {
			ui.Text(c, "Remove “"+name+"” from the sidebar?").FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, detail).FontSize(12.5).TextColor(pal.Muted)
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
