// Package chat is the conversation page: the transcript of the open
// conversation, the composer, approvals and stop. It talks to whatever
// backend provides agent.Service, and lets other plugins extend the
// outgoing message and the composer through contract/chat.
package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin is the chat page.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	host  shell.Host
	// workOn is whether the work panel shows beside a stored conversation.
	workOn *kernel.Setting[bool]

	conv            chat.Conversation
	scope           *kernel.Scope // the open conversation's work and nothing else
	t               transcript
	loading         kernel.Task
	version         uint64 // live events received for the open conversation
	reloadAfterTurn bool
	sending         bool   // a message is on its way and its turn hasn't started
	sendSeq         uint64 // distinguishes acknowledgements from consecutive sends
	attempt         uint64 // the backend connection the transcript was read from
	kept            keptSet
	// replacing is set while the composer edits the last message: sending
	// takes that turn back and sends the draft in its place.
	replacing      bool
	replacingFiles bool // the message being edited had files, which can't go again

	// View state.
	draft    string
	focus    bool
	comp     completion
	list     ui.ListState
	expanded map[uint64]bool // tool calls opened to their detail
	worked   map[uint64]bool // turns' folded work opened, by its first row
	md       markdown.Options
	mdTheme  markdown.Theme
}

var _ chat.Controller = (*Plugin)(nil)

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

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
	p.workOn = kernel.NewSetting(c, "work-panel", true)
	p.comp = newCompletion()
	p.attempt = p.agent.Status().Attempt
	p.switchTo(chat.Conversation{})
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, agent.StatusChanged, p.statusChanged)
	kernel.Provide[chat.Controller](c, chat.ControllerKey, p)
	kernel.Contribute(c, shell.Pages, chat.PageID, 0, shell.Page{Title: p.title, Render: p.view})
	kernel.Contribute(c, settings.Sections, "chat.settings", 20, settings.Section{
		Title: "Conversation", Description: "What shows beside a conversation.", Render: p.settingsSection})
	kernel.Contribute(c, kernel.Commands, "chat.work", 5, kernel.Command{
		Title: "Toggle Work Panel", Menu: "View", Keys: []string{"cmd+b"},
		Run: func() { p.workOn.Set(!p.workOn.Get()) }})
	kernel.Contribute(c, kernel.Commands, "chat.stop", 10, kernel.Command{
		Title: "Stop the Agent", Menu: "Chat", Keys: []string{"cmd+."},
		Enabled: p.running, Run: p.stop})
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
	if k := p.kept.take(s.ID); k != nil {
		next := p.t.nextID
		p.t, p.list, p.expanded, p.worked = k.t, k.list, k.expanded, k.worked
		p.t.nextID = max(p.t.nextID, next)
		if p.t.busy {
			// Its events kept it current while it was away; read it again
			// once the turn is over.
			p.reloadAfterTurn = true
			return
		}
	}
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

func (p *Plugin) SetDraft(text string) {
	p.draft, p.replacing, p.replacingFiles, p.focus = text, false, false, true
}

// switchTo replaces the open conversation. Its work is cancelled with its
// scope, so nothing it started can land in the next one.
func (p *Plugin) switchTo(conv chat.Conversation) {
	if p.scope != nil {
		left := chat.Unsent{Conversation: p.conv}
		if !p.replacing {
			left.Text = p.draft
		}
		kernel.Emit(p.ctx, chat.Leaving, left)
	}
	p.loading.Cancel()
	p.reloadAfterTurn = false
	if p.scope != nil {
		p.scope.Dispose()
	}
	if p.conv.Session != "" && p.t.loaded {
		p.kept.put(&kept{id: p.conv.Session, t: p.t, list: p.list, expanded: p.expanded, worked: p.worked})
	}
	p.scope = p.ctx.NewScope()
	p.conv, p.sending, p.draft = conv, false, ""
	p.replacing, p.replacingFiles = false, false
	p.comp = newCompletion()
	p.t.reset()
	p.expanded, p.worked = map[uint64]bool{}, map[uint64]bool{}
	p.list = ui.ListState{FollowEnd: true}
	p.host.ShowPage(chat.PageID)
	kernel.Emit(p.ctx, chat.Changed, conv)
}

// read is one read of a conversation and its readSum.
type read struct {
	messages []agent.Message
	sum      uint64
}

// load reads the open conversation and builds the transcript from it. A read
// that returns what the transcript was built from, untouched since, as when
// a kept conversation comes back unchanged, leaves it as it is.
func (p *Plugin) load() {
	if p.running() {
		p.reloadAfterTurn = true
		return
	}
	p.reloadAfterTurn = false
	id := p.conv.Session
	version, backend := p.version, p.agent
	kernel.Go(p.scope, &p.loading, func(ctx context.Context) (read, error) {
		messages, err := backend.Messages(ctx, id)
		return read{messages, readSum(messages)}, err
	}, func(r read, err error) {
		if err != nil {
			p.t.add(&row{kind: rowNote, text: "The conversation did not load: " + err.Error(), loadFailed: true})
			return
		}
		if p.version != version {
			// Events have overtaken this snapshot. Keep the live transcript
			// and read history again once the turn is quiet.
			p.load()
			return
		}
		p.attempt = p.agent.Status().Attempt
		if r.sum != 0 && r.sum == p.t.read {
			return
		}
		// App notes survive the reload, but not one saying an earlier read
		// failed: this one worked.
		var notes []*row
		for _, r := range p.t.rows {
			if r.kind == rowNote && !r.loadFailed {
				notes = append(notes, r)
			}
		}
		p.t.load(r.messages, r.sum)
		p.t.rows = append(p.t.rows, notes...)
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
	if errors.Is(err, chat.ErrTaken) {
		p.draft, p.replacing, p.replacingFiles = "", false, false
		return
	}
	if err != nil {
		p.host.Toast(ui.Toast{Title: "The message was not sent", Description: err.Error()})
		return
	}
	if strings.TrimSpace(d.Text) == "" {
		return
	}
	replace := p.replacing && d.Session != ""
	p.draft, p.sending, p.replacing, p.replacingFiles = "", true, false, false
	p.sendSeq++
	if replace {
		p.t.dropLastTurn()
	}
	p.t.addUser(d.Text, d.Files)
	p.list.ScrollToEnd()
	switch {
	case replace:
		p.rewindAndDeliver(d)
		return
	case d.Session != "":
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
		// The folder stays the one chosen, so a next chat and its tab group
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

// ---- Retry and edit

// canReplace reports whether the last message can be taken back now: the
// backend can rewind, and no turn is running or loading.
func (p *Plugin) canReplace() bool {
	_, ok := kernel.Use(p.ctx, agent.RewindKey)
	return ok && p.conv.Session != "" && !p.running() && !p.loading.Running()
}

// retry sends the last message again in place of its turn. The words go as
// they were sent, without the Outgoing pipeline, which would take files and
// mentions from the composer.
func (p *Plugin) retry(text string) {
	if !p.canReplace() {
		return
	}
	d := chat.Draft{Session: p.conv.Session, Text: text}
	p.sending = true
	p.sendSeq++
	p.t.dropLastTurn()
	p.t.addUser(d.Text, nil)
	p.list.ScrollToEnd()
	p.rewindAndDeliver(d)
}

// edit puts the last message in the composer; sending it replaces the turn.
func (p *Plugin) edit(r *row) {
	if !p.canReplace() {
		return
	}
	p.draft, p.replacing, p.replacingFiles, p.focus = r.text, true, len(r.files) > 0, true
}

func (p *Plugin) cancelEdit() {
	p.draft, p.replacing, p.replacingFiles, p.focus = "", false, false, true
}

// rewindAndDeliver takes back the conversation's last user message and
// everything after it, then sends d. The last message is read from the
// backend, since one sent in this session has no ID on show. A backend that
// can only fork goes on in a new conversation, which becomes the open one.
// A failed rewind is reported and the conversation read again, never retried.
func (p *Plugin) rewindAndDeliver(d chat.Draft) {
	rewinder, ok := kernel.Use(p.ctx, agent.RewindKey)
	if !ok {
		return
	}
	id, seq, backend := d.Session, p.sendSeq, p.agent
	var task kernel.Task
	kernel.Go(p.scope, &task, func(ctx context.Context) (agent.SessionID, error) {
		messages, err := backend.Messages(ctx, id)
		if err != nil {
			return "", fmt.Errorf("the conversation could not be read: %w", err)
		}
		last := lastUserMessage(messages)
		if last == "" {
			return "", errors.New("the conversation has no message to replace")
		}
		return rewinder.Rewind(ctx, id, last)
	}, func(next agent.SessionID, err error) {
		if err != nil {
			if p.sendSeq == seq {
				p.sending = false
			}
			p.t.note("The last message was not replaced: " + err.Error() + ". Check the conversation before trying again.")
			p.load()
			return
		}
		if next != id {
			p.conv.Session = next
			kernel.Emit(p.ctx, chat.Changed, p.conv)
			kernel.Emit(p.ctx, chat.Started, p.conv)
			p.host.Toast(ui.Toast{Title: "Continued in a new conversation",
				Description: "This agent can't take a turn back, so the original conversation is kept as it was."})
		}
		p.deliver(next, d)
	})
}

func lastUserMessage(messages []agent.Message) agent.MessageID {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agent.RoleUser {
			return messages[i].ID
		}
	}
	return ""
}

// sendAway sends the first message of a conversation the user left while it
// was being created. Nothing of it is on show, so a failure is a toast.
func (p *Plugin) sendAway(id agent.SessionID, d chat.Draft) {
	backend := p.agent
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (agent.SendDisposition, error) {
		return backend.Send(ctx, agent.Send{Session: id, Mode: d.Mode, Text: d.Text, Files: d.Files})
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
		return backend.Send(ctx, agent.Send{Session: id, Mode: d.Mode, Text: d.Text, Files: d.Files})
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
			if k := p.kept.find(ev.Session); k != nil && ev.Session != "" {
				k.t.apply(ev) // so it comes back as it is now
			}
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
		// A kept conversation whose turn the drop cut off is read afresh
		// when it comes back.
		p.kept = slices.DeleteFunc(p.kept, func(k *kept) bool { return k.t.busy })
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
