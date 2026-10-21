// Package chat is the conversation page: the transcript of the open
// conversation, the composer, approvals and stop. It talks to whatever
// backend provides agent.Service, and lets other plugins extend the
// outgoing message and the composer through contract/chat.
package chat

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
)

// Plugin is the chat page. The zero value is ready to install.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	host  shell.Host

	conv            chat.Conversation
	scope           *kernel.Scope // the open conversation's work and nothing else
	t               transcript
	loading         kernel.Task
	version         uint64 // live events received for the open conversation
	reloadAfterTurn bool
	sending         bool   // a message is on its way and its turn hasn't started
	sendSeq         uint64 // distinguishes acknowledgements from consecutive sends
	attempt         uint64 // the backend connection the transcript was read from

	// View state.
	draft    string
	focus    bool
	comp     completion
	list     ui.ListState
	expanded map[uint64]bool
	md       markdown.Options
	mdTheme  markdown.Theme
}

var _ chat.Controller = (*Plugin)(nil)

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "chat", Title: "Chat", Locked: true,
		Description: "The conversation: messages, the composer and approvals.",
		Requires:    []kernel.Named{agent.ServiceKey, shell.HostKey},
		Provides:    []kernel.Named{chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.expanded = map[uint64]bool{}
	p.comp = newCompletion()
	p.attempt = p.agent.Status().Attempt
	p.switchTo(chat.Conversation{})
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, agent.StatusChanged, p.statusChanged)
	for _, err := range []error{
		kernel.Provide[chat.Controller](c, chat.ControllerKey, p),
		kernel.Contribute(c, shell.Pages, chat.PageID, 0, shell.Page{Title: p.title, Render: p.view}),
		kernel.Contribute(c, kernel.Commands, "chat.new", 0, kernel.Command{
			Title: "New Chat", Menu: "File", Keys: []string{"cmd+n"},
			Run: func() { p.NewChat(p.conv.Directory) }}),
		kernel.Contribute(c, kernel.Commands, "chat.stop", 10, kernel.Command{
			Title: "Stop the Agent", Menu: "Chat", Keys: []string{"cmd+."},
			Enabled: p.running, Run: p.stop}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) title() string {
	if p.conv.Session == "" {
		return "New chat"
	}
	return cmp.Or(p.conv.Title, "Untitled chat")
}

// ---- chat.Controller

func (p *Plugin) Current() chat.Conversation { return p.conv }

func (p *Plugin) Open(s agent.Session) {
	if s.ID == p.conv.Session && s.ID != "" {
		return
	}
	p.switchTo(chat.Conversation{Session: s.ID, Title: s.Title, Directory: s.Directory, Model: s.Model})
	p.load()
}

func (p *Plugin) NewChat(directory string) {
	p.switchTo(chat.Conversation{Directory: directory})
	p.focus = true
}

func (p *Plugin) SetDirectory(directory string) {
	if p.conv.Session != "" || p.conv.Starting || p.conv.Directory == directory {
		return
	}
	p.conv.Directory = directory
	kernel.Emit(p.ctx, chat.Changed, p.conv)
}

// switchTo replaces the open conversation. Its work is cancelled with its
// scope, so nothing it started can land in the next one.
func (p *Plugin) switchTo(conv chat.Conversation) {
	p.loading.Cancel()
	p.reloadAfterTurn = false
	if p.scope != nil {
		p.scope.Dispose()
	}
	p.scope = p.ctx.NewScope()
	p.conv, p.sending, p.draft = conv, false, ""
	p.comp = newCompletion()
	p.t.reset()
	clear(p.expanded)
	p.list = ui.ListState{FollowEnd: true}
	p.host.ShowPage(chat.PageID)
	kernel.Emit(p.ctx, chat.Changed, conv)
}

func (p *Plugin) load() {
	if p.running() {
		p.reloadAfterTurn = true
		return
	}
	p.reloadAfterTurn = false
	id := p.conv.Session
	version, backend := p.version, p.agent
	kernel.Go(p.scope, &p.loading, func(ctx context.Context) ([]agent.Message, error) {
		return backend.Messages(ctx, id)
	}, func(messages []agent.Message, err error) {
		if err != nil {
			p.t.note("The conversation did not load: " + err.Error())
			return
		}
		if p.version != version {
			// Events have overtaken this snapshot. Keep the live transcript
			// and read history again once the turn is quiet.
			p.load()
			return
		}
		var notes []*row
		for _, r := range p.t.rows {
			if r.kind == rowNote {
				notes = append(notes, r)
			}
		}
		p.t.load(messages)
		p.t.rows = append(p.t.rows, notes...)
		p.attempt = p.agent.Status().Attempt
	})
}

// ---- Sending

func (p *Plugin) running() bool { return p.sending || p.t.busy }

// send hands the composer's words to the backend: through the Outgoing
// pipeline, then creating the conversation if this is its first message.
// A failed send is reported, never retried: it may have reached the agent.
func (p *Plugin) send() {
	text := strings.TrimSpace(p.draft)
	if text == "" || p.running() || p.loading.Running() {
		return
	}
	d, err := kernel.Run(p.ctx, chat.Outgoing, chat.Draft{Session: p.conv.Session, Directory: p.conv.Directory, Text: text})
	if err != nil {
		p.host.Toast(ui.Toast{Title: "The message was not sent", Description: err.Error()})
		return
	}
	if strings.TrimSpace(d.Text) == "" {
		return
	}
	p.draft, p.sending = "", true
	p.sendSeq++
	p.t.addUser(d.Text, d.Files)
	p.list.ScrollToEnd()
	if d.Session != "" {
		p.deliver(d.Session, d)
		return
	}
	// The conversation is created on the plugin's lifetime, not the open
	// conversation's: once the backend has it, it is recorded and gets its
	// message even if the user has moved on meanwhile.
	p.conv.Starting = true
	scope, backend := p.scope, p.agent
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.Session, error) {
		return backend.Create(ctx, agent.CreateSession{Directory: d.Directory, Model: d.Model, Mode: d.Mode})
	}, func(s agent.Session, err error) {
		here := p.scope == scope
		if s.ID == "" {
			if here {
				p.sending, p.conv.Starting = false, false
				p.t.note("The chat did not start: " + errText(err))
			} else {
				p.host.Toast(ui.Toast{Title: "The chat did not start", Description: errText(err), Type: components.ToastError})
			}
			return
		}
		started := chat.Conversation{Session: s.ID, Title: s.Title, Directory: d.Directory, Model: s.Model}
		if !here {
			kernel.Emit(p.ctx, chat.Started, started)
			p.sendAway(s.ID, d)
			return
		}
		// The folder stays the one chosen, so a next chat and the sidebar
		// keep to it even when the backend names its default.
		p.conv.Session, p.conv.Model, p.conv.Starting = s.ID, s.Model, false
		kernel.Emit(p.ctx, chat.Changed, p.conv)
		kernel.Emit(p.ctx, chat.Started, started)
		if err != nil {
			p.t.note(err.Error())
		}
		p.deliver(s.ID, d)
	})
}

// sendAway sends the first message of a conversation the user left while it
// was being created. Nothing of it is on show, so a failure is a toast.
func (p *Plugin) sendAway(id agent.SessionID, d chat.Draft) {
	backend := p.agent
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.SendDisposition, error) {
		return backend.Send(ctx, agent.Send{Session: id, Text: d.Text, Files: d.Files})
	}, func(_ agent.SendDisposition, err error) {
		switch {
		case err == nil:
		case errors.Is(err, agent.ErrNotSent):
			p.host.Toast(ui.Toast{Title: "The message was not sent", Description: err.Error(), Type: components.ToastError})
		default:
			p.host.Toast(ui.Toast{Title: "The message may not have reached the agent", Description: err.Error() +
				". Check the conversation before sending it again.", Type: components.ToastError})
		}
	})
}

func (p *Plugin) deliver(id agent.SessionID, d chat.Draft) {
	seq, backend := p.sendSeq, p.agent
	var task kernel.Task
	kernel.Go(p.scope, &task, func(ctx context.Context) (agent.SendDisposition, error) {
		return backend.Send(ctx, agent.Send{Session: id, Text: d.Text, Files: d.Files})
	}, func(disposition agent.SendDisposition, err error) {
		finish := func() {
			if p.sendSeq == seq {
				p.sending = false
			}
		}
		switch {
		case err == nil:
			if disposition == agent.SendHandled {
				finish()
			}
		case errors.Is(err, agent.ErrNotSent):
			finish()
			p.t.note("The message was not sent" + strings.TrimPrefix(err.Error(), agent.ErrNotSent.Error()) + ".")
		default:
			finish()
			p.t.note("The message may not have reached the agent: " + err.Error() + ". Check the conversation before sending it again.")
		}
	})
}

func (p *Plugin) stop() {
	id := p.conv.Session
	if id == "" || !p.running() {
		return
	}
	var task kernel.Task
	kernel.Go(p.scope, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Abort(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.t.note("The agent did not stop: " + err.Error())
		}
	})
}

func (p *Plugin) reply(a agent.Approval, d agent.Decision) {
	id := p.conv.Session
	var task kernel.Task
	kernel.Go(p.scope, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.agent.Reply(ctx, agent.Reply{Session: id, Approval: a.ID, Decision: d})
	}, func(_ struct{}, err error) {
		if err != nil {
			p.t.note("The answer did not reach the agent: " + err.Error())
			return
		}
		p.t.removeApproval(a.ID)
	})
}

// ---- Backend events

func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		if ev.Session != p.conv.Session || ev.Session == "" {
			continue
		}
		p.version++
		switch ev.Kind {
		case agent.SessionRenamed:
			p.conv.Title = ev.Title
			kernel.Emit(p.ctx, chat.Changed, p.conv)
		case agent.ModelChanged:
			p.conv.Model = ev.Model
			kernel.Emit(p.ctx, chat.Changed, p.conv)
		case agent.TurnStarted, agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			p.sending = false
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested:
		}
		p.t.apply(ev)
	}
	if p.reloadAfterTurn && !p.running() {
		p.load()
	}
}

// statusChanged settles a turn the connection dropped under, and reloads the
// conversation from a new connection, since events may have been missed.
func (p *Plugin) statusChanged(st agent.Status) {
	switch st.State {
	case agent.Down:
		if p.running() {
			p.sending = false
			p.t.settle()
			p.t.note("The connection dropped while the agent was working. Check the conversation before sending again.")
		}
	case agent.Ready:
		if st.Attempt != p.attempt && p.conv.Session != "" {
			p.load()
		}
		p.attempt = st.Attempt
	case agent.Stopped, agent.Connecting:
	}
}

func errText(err error) string {
	if err == nil {
		return "the backend returned no conversation"
	}
	return err.Error()
}
