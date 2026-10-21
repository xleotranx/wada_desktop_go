// Package sessions is the sidebar's conversation list: newest first, paged,
// searchable, with rename, delete and an unread mark for conversations that
// finished a turn while another was open.
package sessions

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
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

	sessions []agent.Session
	next     string
	loaded   bool
	failure  string
	unread   map[agent.SessionID]bool
	page     kernel.Task
	more     kernel.Task

	query    string
	results  []agent.Session
	search   kernel.Task
	searched string // the query results belong to

	renaming *agent.Session
	title    string
	renameOn bool
	deleting *agent.Session
	deleteOn bool

	list ui.ListState
	// searchDelay waits before a search goes out, so typing sends one.
	searchDelay time.Duration
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "sessions", Title: "Conversations",
		Description: "The conversation list in the sidebar.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.unread = map[agent.SessionID]bool{}
	if p.searchDelay == 0 {
		p.searchDelay = 200 * time.Millisecond
	}
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready {
			p.refresh()
		}
	})
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, chat.Changed, p.chatChanged)
	if p.agent.Status().State == agent.Ready {
		p.refresh()
	}
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

// refresh reloads the first page; pages already loaded beyond it are dropped.
func (p *Plugin) refresh() {
	kernel.Go(p.ctx, &p.page, func(ctx context.Context) (agent.SessionPage, error) {
		return p.agent.Sessions(ctx, agent.SessionQuery{})
	}, func(page agent.SessionPage, err error) {
		if err != nil {
			p.failure = "The conversations did not load: " + err.Error()
			return
		}
		p.more.Cancel()
		p.sessions, p.next, p.loaded, p.failure = page.Sessions, page.Next, true, ""
	})
}

func (p *Plugin) loadMore() {
	cursor := p.next
	if cursor == "" || p.more.Running() {
		return
	}
	kernel.Go(p.ctx, &p.more, func(ctx context.Context) (agent.SessionPage, error) {
		return p.agent.Sessions(ctx, agent.SessionQuery{Cursor: cursor})
	}, func(page agent.SessionPage, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "More conversations did not load", Description: err.Error(), Type: components.ToastError})
			return
		}
		for _, s := range page.Sessions {
			if !slices.ContainsFunc(p.sessions, func(x agent.Session) bool { return x.ID == s.ID }) {
				p.sessions = append(p.sessions, s)
			}
		}
		p.next = page.Next
	})
}

// searchFor replaces any search in flight; an empty query shows the list.
func (p *Plugin) searchFor(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		p.search.Cancel()
		p.results, p.searched = nil, ""
		return
	}
	delay := p.searchDelay
	kernel.Go(p.ctx, &p.search, func(ctx context.Context) (agent.SessionPage, error) {
		select {
		case <-ctx.Done():
			return agent.SessionPage{}, ctx.Err()
		case <-time.After(delay):
		}
		if err := ctx.Err(); err != nil { // replaced while waiting
			return agent.SessionPage{}, err
		}
		return p.agent.Sessions(ctx, agent.SessionQuery{Search: q})
	}, func(page agent.SessionPage, err error) {
		if err != nil {
			p.results, p.searched = nil, q
			return
		}
		p.results, p.searched = page.Sessions, q
	})
}

func (p *Plugin) events(batch []agent.Event) {
	current := p.chat.Current().Session
	refresh := false
	for _, ev := range batch {
		switch ev.Kind {
		case agent.TurnSucceeded, agent.TurnFailed:
			if ev.Session != current {
				p.unread[ev.Session] = true
			}
			refresh = true
		case agent.SessionRenamed:
			p.update(ev.Session, func(s *agent.Session) { s.Title = ev.Title })
		case agent.ModelChanged:
			p.update(ev.Session, func(s *agent.Session) { s.Model = ev.Model })
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.TurnStarted, agent.TurnInterrupted:
		}
	}
	if refresh {
		p.refresh() // the list is ordered by activity
	}
}

func (p *Plugin) chatChanged(conv chat.Conversation) {
	delete(p.unread, conv.Session)
	if conv.Session != "" && !slices.ContainsFunc(p.sessions, func(s agent.Session) bool { return s.ID == conv.Session }) {
		p.refresh()
	}
}

func (p *Plugin) update(id agent.SessionID, fn func(*agent.Session)) {
	for _, list := range [][]agent.Session{p.sessions, p.results} {
		for i := range list {
			if list[i].ID == id {
				fn(&list[i])
			}
		}
	}
}

func (p *Plugin) open(s agent.Session) {
	delete(p.unread, s.ID)
	p.chat.Open(s)
}

// ---- Rename and delete

func (p *Plugin) startRename(s agent.Session) {
	p.renaming, p.title, p.renameOn = &s, s.Title, true
}

func (p *Plugin) saveRename() {
	s, title := *p.renaming, strings.TrimSpace(p.title)
	p.renaming, p.renameOn = nil, false
	if title == "" || title == s.Title {
		return
	}
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Rename(ctx, s.ID, title)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not renamed", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.update(s.ID, func(x *agent.Session) { x.Title = title })
	})
}

func (p *Plugin) startDelete(s agent.Session) { p.deleting, p.deleteOn = &s, true }

func (p *Plugin) confirmDelete() {
	s := *p.deleting
	p.deleting, p.deleteOn = nil, false
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Delete(ctx, s.ID)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The conversation was not deleted", Description: err.Error() + ". It may already be gone; the list will refresh.", Type: components.ToastError})
			p.refresh()
			return
		}
		drop := func(x agent.Session) bool { return x.ID == s.ID }
		p.sessions = slices.DeleteFunc(p.sessions, drop)
		p.results = slices.DeleteFunc(p.results, drop)
		delete(p.unread, s.ID)
		if p.chat.Current().Session == s.ID {
			p.chat.NewChat(s.Directory)
		}
	})
}

// ---- View

func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).Fill().Gap(4).Children(func() {
		newChat := ui.ButtonBase(c).Label("New chat").Height(34).Radius(8).Padding(0, 8).FillWidth().
			Justify(ui.Start).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer)
		if newChat.Hovered() {
			newChat.Background(pal.Hover)
		}
		newChat.OnClick(func() { p.chat.NewChat("") })
		newChat.Children(func() {
			ui.Icon(c, lucide.SquarePen()).FontSize(14).TextColor(pal.Text)
			ui.Text(c, "New chat").FontSize(13.5).TextColor(pal.Text)
		})
		search := components.TextBox(c, &p.query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).
			FillWidth().Label("Search conversations").Placeholder("Search").FontSize(13)
		search.OnChange(func() { p.searchFor(p.query) })

		rows := p.sessions
		empty := "No conversations yet"
		if strings.TrimSpace(p.query) != "" {
			rows, empty = p.results, "No matches"
			if p.searched != strings.TrimSpace(p.query) {
				empty = "Searching…"
			}
		}
		switch {
		case p.failure != "" && len(rows) == 0:
			ui.Text(c, p.failure).FontSize(12).TextColor(pal.Danger).Padding(8, 8)
		case !p.loaded && strings.TrimSpace(p.query) == "":
			ui.Text(c, "Loading conversations…").FontSize(12).TextColor(pal.Faint).Padding(8, 8)
		case len(rows) == 0:
			ui.Text(c, empty).FontSize(12).TextColor(pal.Faint).Padding(8, 8)
		default:
			more := p.next != "" && strings.TrimSpace(p.query) == ""
			count := len(rows)
			if more {
				count++
			}
			ui.List(c, &p.list, count).Grow(1).MinHeight(0).FillWidth().Gap(1).
				ItemKey(func(i int) any {
					if i == len(rows) {
						return "more"
					}
					return rows[i].ID
				}).
				Rows(func(r ui.ListRow) {
					if r.Index == len(rows) {
						p.moreRow(r.Context, pal)
						return
					}
					p.row(r.Context, pal, rows[r.Index])
				})
		}
	})
}

func (p *Plugin) moreRow(c *ui.Context, pal native.Palette) {
	label := "Show more"
	if p.more.Running() {
		label = "Loading…"
	}
	b := ui.ButtonBase(c).Label(label).Height(30).Radius(8).Padding(0, 8).FillWidth().Justify(ui.Start)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(p.loadMore)
	b.Children(func() { ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted) })
}

func (p *Plugin) row(c *ui.Context, pal native.Palette, s agent.Session) {
	title := s.Title
	if title == "" {
		title = "Untitled chat"
	}
	current := s.ID == p.chat.Current().Session
	b := ui.ButtonBase(c).Label(title).Height(34).Radius(8).Padding(0, 4, 0, 8).FillWidth().
		Justify(ui.Start).AlignItems(ui.Center).Gap(6).Cursor(ui.CursorPointer)
	switch {
	case current:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.open(s) })
	b.Children(func() {
		ui.Text(c, title).FontSize(13.5).TextColor(pal.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if s.Directory != "" {
			ui.Text(c, filepath.Base(s.Directory)).FontSize(10.5).TextColor(pal.Faint).Shrink(0).SingleLine()
		}
		if p.unread[s.ID] {
			ui.Box(c).Size(7, 7).Radius(4).Background(pal.Orange).Shrink(0).Label("Unread")
		}
		kebab := ui.ButtonBase(c).Label("Options for "+title).Size(22, 22).Radius(6).Center().Shrink(0)
		kebab.Menu(func(m *ui.Menu) {
			if m.Item("Rename…").Chosen() {
				p.startRename(s)
			}
			if m.Item("Delete…").Chosen() {
				p.startDelete(s)
			}
		})
		if kebab.Hovered() {
			kebab.Background(pal.Hover)
		}
		kebab.Children(func() { ui.Icon(c, lucide.Ellipsis()).FontSize(13).TextColor(pal.Faint) })
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
		title := p.deleting.Title
		if title == "" {
			title = "Untitled chat"
		}
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
}
