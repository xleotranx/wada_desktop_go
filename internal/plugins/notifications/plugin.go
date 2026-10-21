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
	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
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
	p.known = map[agent.SessionID]agent.Session{}
	kernel.On(c, agent.Events, p.events)
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
				p.notify(e.Session, "Waiting for your approval to "+action(e.Approval.Action)+".")
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
// A conversation without a known title is looked up on the backend first.
func (p *Plugin) notify(id agent.SessionID, body string) {
	if id == "" || p.seen(id) {
		return
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

// action words an approval's action for a sentence: "shell" → "run a command".
func action(a string) string {
	switch a {
	case "bash", "shell":
		return "run a command"
	case "edit", "write":
		return "change files"
	case "":
		return "continue"
	}
	return "use " + a
}

func (p *Plugin) section(c *ui.Context) {
	for _, s := range []struct {
		setting *kernel.Setting[bool]
		label   string
	}{{p.finished, "When a turn finishes"}, {p.approvals, "When an approval is needed"}} {
		c.Key(s.label)
		on := s.setting.Get()
		if components.Switch(c, &on, s.label); on != s.setting.Get() {
			p.ctx.Post(func() { s.setting.Set(on) }) // after the frame, not while rendering
		}
	}
}
