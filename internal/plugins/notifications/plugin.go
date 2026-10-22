// Package notifications shows a desktop notification when a conversation the
// user isn't looking at finishes a turn or waits for an approval. Clicking it
// opens that conversation.
package notifications

import (
	"context"
	"path/filepath"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// noticePrefix marks a notice ID as naming a conversation.
const noticePrefix = "chat:"

// Plugin is the notifications plugin. The zero value is ready to install.
type Plugin struct {
	ctx       *kernel.Context
	agent     agent.Service
	chat      chat.Controller
	host      shell.Host
	finished  *kernel.Setting[bool]
	approvals *kernel.Setting[bool]
	// known holds the conversations notices were shown for, so a click
	// opens one with its title and folder.
	known map[agent.SessionID]agent.Session
	// waiting holds what came to notify before the workspace record was read.
	waiting []notice
}

type notice struct {
	id   agent.SessionID
	body string
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "notifications", Title: "Notifications",
		Description: "Tells you when a conversation in the background finishes or needs an approval.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.finished = kernel.NewSetting(c, "turn-finished", true)
	p.approvals = kernel.NewSetting(c, "approvals", true)
	p.known, p.waiting = map[agent.SessionID]agent.Session{}, nil
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, workspace.Changed, func(struct{}) { p.flush() })
	kernel.On(c, shell.NoticeClicked, p.clicked)
	return kernel.Contribute(c, settings.Sections, "notifications.section", 50, settings.Section{
		Title: "Notifications", Description: "Shown only while you look at another conversation or another app.",
		Render: p.section})
}

func (p *Plugin) events(batch []agent.Event) {
	for _, e := range batch {
		switch e.Kind {
		case agent.TurnSucceeded:
			if p.finished.Get() {
				p.notify(e.Session, "The turn finished.")
			}
		case agent.TurnFailed:
			if p.finished.Get() {
				p.notify(e.Session, "The turn stopped with an error.")
			}
		case agent.ApprovalRequested:
			if p.approvals.Get() && e.Approval != nil {
				p.notify(e.Session, "Waiting for your approval to "+action(*e.Approval)+".")
			}
		case agent.InteractionRequested:
			if p.approvals.Get() {
				p.notify(e.Session, "Waiting for your answer.")
			}
		case agent.SessionRenamed:
			if s, ok := p.known[e.Session]; ok {
				s.Title = e.Title
				p.known[e.Session] = s
			}
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.TurnStarted, agent.TurnInterrupted, agent.ModelChanged:
		}
	}
}

// seen reports that the user is looking at the conversation.
func (p *Plugin) seen(id agent.SessionID) bool {
	page := p.host.Page()
	return p.host.Foreground() && (page == "" || page == chat.PageID) && p.chat.Current().Session == id
}

// notify shows body for the conversation unless the user is looking at it.
// Only conversations started in this app are notified while the workspace
// record is active; until it has been read, notices wait for it. A
// conversation without a known title is looked up on the backend first.
func (p *Plugin) notify(id agent.SessionID, body string) {
	if id == "" || p.seen(id) {
		return
	}
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
		if !ws.Loaded() {
			p.waiting = append(p.waiting, notice{id, body})
			return
		}
		conv, known := ws.Lookup(id)
		if !known {
			return
		}
		if conv.Title != "" {
			p.show(conv.Session(), body)
			return
		}
	}
	if conv := p.chat.Current(); conv.Session == id {
		p.known[id] = agent.Session{ID: id, Title: conv.Title, Directory: conv.Directory, Model: conv.Model}
	}
	if s, ok := p.known[id]; ok && s.Title != "" {
		p.show(s, body)
		return
	}
	p.ctx.Spawn(func(ctx context.Context) {
		s := agent.Session{ID: id}
		if page, err := p.agent.Sessions(ctx, agent.SessionQuery{}); err == nil {
			for _, found := range page.Sessions {
				if found.ID == id {
					s = found
				}
			}
		}
		p.ctx.Post(func() {
			p.known[id] = s
			p.show(s, body)
		})
	})
}

// flush notifies what waited for the workspace record.
func (p *Plugin) flush() {
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); len(p.waiting) == 0 || ok && !ws.Loaded() {
		return
	}
	waiting := p.waiting
	p.waiting = nil
	for _, n := range waiting {
		p.notify(n.id, n.body)
	}
}

func (p *Plugin) show(s agent.Session, body string) {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = "New chat"
	}
	var folder string
	if s.Directory != "" {
		folder = filepath.Base(s.Directory)
	}
	p.host.Notify(shell.Notice{ID: noticePrefix + string(s.ID), Title: title, Subtitle: folder, Body: body})
}

func (p *Plugin) clicked(id string) {
	sid, ok := strings.CutPrefix(id, noticePrefix)
	if !ok || sid == "" {
		return
	}
	s, ok := p.known[agent.SessionID(sid)]
	if !ok {
		s = agent.Session{ID: agent.SessionID(sid)}
	}
	p.chat.Open(s)
}

// action words an approval for a sentence: "Waiting for your approval to …".
func action(a agent.Approval) string {
	switch a.Kind {
	case agent.ToolShell:
		return "run a command"
	case agent.ToolRead:
		return "read files"
	case agent.ToolSearch:
		return "search files"
	case agent.ToolEdit:
		return "change files"
	case agent.ToolFetch:
		return "fetch from the web"
	case agent.ToolOther:
	}
	if a.Action == "" {
		return "continue"
	}
	return "use " + a.Action
}

func (p *Plugin) section(c *ui.Context) {
	pal := native.PaletteFor(c)
	for i, s := range []struct {
		setting            *kernel.Setting[bool]
		label, description string
	}{{p.finished, "When a turn finishes", "Know when a response is ready."}, {p.approvals, "When an approval is needed", "Know when a conversation needs your attention."}} {
		if i > 0 {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		}
		c.Key(s.label)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Padding(4, 0).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, s.label).FontSize(13.5).FontWeight(500).TextColor(pal.Text)
				ui.Text(c, s.description).FontSize(12).TextColor(pal.Muted)
			})
			on := s.setting.Get()
			components.Switch(c, &on, "Notify: "+s.label, components.SwitchOptions{HideLabel: true})
			if on != s.setting.Get() {
				p.ctx.Post(func() { s.setting.Set(on) })
			}
		})
	}
}
