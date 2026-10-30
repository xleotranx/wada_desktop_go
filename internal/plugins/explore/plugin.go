// Package explore is the side chat: a read-only conversation in a drawer
// beside the open one, started from text selected in its transcript, whose
// answers the user can add to the main conversation's draft.
package explore

import (
	"context"
	"errors"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// preamble goes before the first message of a side chat, which starts
// without the main conversation's history. The drawer never shows it.
const preamble = "This is a side chat beside another conversation. You can read and search files but not change them. " +
	"The user may quote part of that conversation.\n\n"

// link remembers a side chat of a conversation, under the backend origin
// both belong to.
type link struct {
	Origin string
	Parent agent.SessionID
	Child  agent.SessionID
	Open   bool // the drawer was showing it beside its conversation
}

type entryKind uint8

const (
	entryUser   entryKind = iota // the user's words
	entryAnswer                  // the agent's words, finished or arriving
	entryNote                    // a faint line: a tool call or a failure
)

type entry struct {
	id        uint64
	kind      entryKind
	text      string
	streaming bool
}

type textKey struct {
	message agent.MessageID
	ordinal int
}

// side is the side chat in the drawer.
type side struct {
	origin  string
	parent  chat.Conversation
	child   agent.SessionID // empty until the first message creates it
	primed  bool            // the preamble went with a message, or it has history
	scope   *kernel.Scope   // its reads and sends; disposed with it
	loading kernel.Task
	entries []*entry
	live    map[textKey]*entry
	nextID  uint64
	busy    bool // a turn is running
	sending bool // a message is on its way and its turn hasn't started
	draft   string
	focus   bool
	list    ui.ListState
}

func (s *side) add(kind entryKind, text string) *entry {
	s.nextID++
	e := &entry{id: s.nextID, kind: kind, text: text}
	s.entries = append(s.entries, e)
	return e
}

func (s *side) running() bool { return s.busy || s.sending }

// Plugin owns the drawer and the persisted links from conversations to their
// side chats.
type Plugin struct {
	ctx      *kernel.Context
	agent    agent.Service
	children agent.Children
	chat     chat.Controller
	ws       workspace.Service
	host     shell.Host
	links    []link
	side     *side                     // the side chat in the drawer; nil before the first
	sides    map[agent.SessionID]*side // created side chats, followed while out of the drawer
	open     bool                      // the drawer shows beside side's conversation
	creating kernel.Task
	md       markdown.Options
	mdTheme  markdown.Theme
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "explore", Title: "Side chats",
		Description: "Ask about selected text in a read-only side chat, beside the conversation.",
		Requires:    []kernel.Named{agent.ServiceKey, agent.ChildrenKey, chat.ControllerKey, workspace.ServiceKey, shell.HostKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.children = kernel.Require(c, agent.ChildrenKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.sides = map[agent.SessionID]*side{}
	c.Settings().Get("side-chats-v1", &p.links)
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, agent.StatusChanged, p.statusChanged)
	kernel.On(c, chat.Changed, func(chat.Conversation) { p.restore() })
	kernel.On(c, workspace.Changed, func(struct{}) { p.prune() })
	kernel.Contribute(c, chat.SelectionActions, "explore.side-chat", 0, chat.SelectionAction{
		Title: "Start a side chat", Icon: lucide.MessageSquarePlus, Run: p.start})
	kernel.Contribute(c, chat.Drawers, "explore.drawer", 0, shell.View{Render: p.drawer})
	return kernel.Contribute(c, kernel.Commands, "explore.show", 0, kernel.Command{
		Title: "Show Side Chat", Menu: "View", Enabled: p.canShow, Run: func() { p.reveal(true) }})
}

// here reports that the side chat in the drawer belongs beside the open
// conversation.
func (p *Plugin) here() bool {
	return p.side != nil && p.side.parent.Session == p.chat.Current().Session && p.side.origin == p.agent.Status().Origin
}

// shown reports that the drawer is on show: open, beside its conversation.
func (p *Plugin) shown() bool { return p.open && p.here() }

// replace puts s in the drawer. The side chat there before is dropped only
// if nothing was created for it yet and nothing is on its way; otherwise it
// stays in sides, so its sends finish and its turns are followed.
func (p *Plugin) replace(s *side) {
	if old := p.side; old != nil && old != s && old.child == "" && !old.running() {
		old.scope.Dispose()
	}
	p.side, p.open = s, s != nil
}

// forget stops following the side chats keep says no to, closing the drawer
// on one of them.
func (p *Plugin) forget(keep func(*side) bool) {
	if p.side != nil && !keep(p.side) {
		p.replace(nil)
	}
	for id, s := range p.sides {
		if !keep(s) {
			s.scope.Dispose()
			delete(p.sides, id)
		}
	}
}

func (p *Plugin) newSide(parent chat.Conversation) *side {
	return &side{origin: p.agent.Status().Origin, parent: parent, scope: p.ctx.NewScope(),
		live: map[textKey]*entry{}, list: ui.ListState{FollowEnd: true}}
}

// start opens a new side chat beside the conversation the text was selected
// in, with the text quoted in its composer. Nothing is created until the user
// sends.
func (p *Plugin) start(sel chat.Selection) {
	if sel.Conversation.Session == "" {
		p.host.Toast(ui.Toast{Title: "Send the first message first",
			Description: "A side chat starts beside a conversation the agent has."})
		return
	}
	s := p.newSide(sel.Conversation)
	s.draft, s.focus = quote(sel.Text)+"\n\n", true
	p.replace(s)
}

// latest is the newest side chat of the open conversation.
func (p *Plugin) latest() (link, bool) {
	conv, origin := p.chat.Current(), p.agent.Status().Origin
	for i := len(p.links) - 1; i >= 0; i-- {
		if l := p.links[i]; l.Parent == conv.Session && l.Origin == origin && conv.Session != "" {
			return l, true
		}
	}
	return link{}, false
}

func (p *Plugin) canShow() bool {
	_, ok := p.latest()
	return ok || p.here()
}

// reveal opens the drawer again: the side chat it had beside this
// conversation, or else the newest one, as followed since it was created or
// read from the backend.
func (p *Plugin) reveal(focus bool) {
	if !p.canShow() {
		return
	}
	if !p.here() {
		l, _ := p.latest()
		s := p.sides[l.Child]
		if s == nil {
			s = p.newSide(p.chat.Current())
			s.child, s.primed = l.Child, true
			p.sides[s.child] = s
			p.load(s)
		}
		p.replace(s)
	}
	p.open, p.side.focus = true, focus
	p.remember(p.side.child, true)
}

// restore shows the drawer beside a conversation whose side chat was open
// when the user left it, also after a restart.
func (p *Plugin) restore() {
	if p.here() {
		return
	}
	if l, ok := p.latest(); ok && l.Open {
		p.reveal(false)
	}
}

func (p *Plugin) close() {
	p.open = false
	if p.side != nil {
		p.remember(p.side.child, false)
	}
}

// remember records whether the drawer shows child.
func (p *Plugin) remember(child agent.SessionID, open bool) {
	for i, l := range p.links {
		if l.Child == child && child != "" && l.Open != open {
			p.links[i].Open = open
			p.saveLinks()
		}
	}
}

func (p *Plugin) load(s *side) {
	svc, id := p.agent, s.child
	kernel.Go(s.scope, &s.loading, func(ctx context.Context) ([]agent.Message, error) {
		return svc.Messages(ctx, id)
	}, func(messages []agent.Message, err error) {
		if err != nil {
			s.add(entryNote, "The side chat did not load: "+err.Error())
			return
		}
		live := s.entries
		s.entries = nil
		for _, m := range messages {
			if text := messageText(m); text != "" {
				kind := entryAnswer
				if m.Role == agent.RoleUser {
					kind, text = entryUser, strings.TrimPrefix(text, strings.TrimSpace(preamble))
				}
				s.add(kind, strings.TrimSpace(text))
			}
		}
		s.entries = append(s.entries, live...)
	})
}

func messageText(m agent.Message) string {
	switch m.Role {
	case agent.RoleUser:
		return strings.TrimSpace(m.Text)
	case agent.RoleAssistant:
		var parts []string
		for _, part := range m.Parts {
			if part.Kind == agent.PartText && strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	case agent.RoleOther:
	}
	return ""
}

// send sends the composer's words to the side chat, creating it with the
// first message. Creation and sending are never retried: a failure has an
// unknown outcome, so it is reported.
func (p *Plugin) send() {
	s := p.side
	text := strings.TrimSpace(s.draft)
	if text == "" || s.running() || s.loading.Running() || p.creating.Running() || p.agent.Status().State != agent.Ready {
		return
	}
	s.draft, s.sending = "", true
	s.add(entryUser, text)
	s.list.ScrollToEnd()
	if s.child != "" {
		p.deliver(s, text)
		return
	}
	req := agent.Spawn{Parent: s.parent.Session, Name: name(text), Directory: s.parent.Directory, Model: s.parent.Model,
		Policy: agent.ToolPolicy{Allow: []agent.ToolKind{agent.ToolRead, agent.ToolSearch},
			Deny: []agent.ToolKind{agent.ToolEdit, agent.ToolShell, agent.ToolFetch, agent.ToolOther}}}
	children, origin := p.children, s.origin
	// Creation belongs to the plugin, so closing the drawer or starting
	// another side chat cannot lose the identity of one already created, nor
	// its first message.
	kernel.Go(p.ctx, &p.creating, func(ctx context.Context) (agent.Session, error) {
		return children.Spawn(ctx, req)
	}, func(session agent.Session, err error) {
		if session.ID != "" {
			p.links = append(p.links, link{Origin: origin, Parent: req.Parent, Child: session.ID, Open: p.side == s && p.open})
			p.saveLinks()
		}
		// s may have left the drawer meanwhile; it still gets its message.
		s.child = session.ID
		if session.ID != "" {
			p.sides[session.ID] = s
		}
		switch {
		case err != nil:
			s.sending = false
			s.add(entryNote, "The side chat did not start: "+err.Error()+". It was not tried again.")
		case session.ID == "":
			s.sending = false
			s.add(entryNote, "The side chat did not start: the backend returned no conversation.")
		default:
			p.deliver(s, text)
		}
	})
}

// name is what the backend calls a side chat: the user's own words, after
// the quote.
func name(text string) string {
	var words []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, ">") {
			words = append(words, line)
		}
	}
	title := "Side chat"
	if len(words) > 0 {
		title += ": " + strings.Join(words, " ")
	}
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:80]) + "…"
	}
	return title
}

// deliver sends text to the side chat; its first message carries the
// preamble.
func (p *Plugin) deliver(s *side, text string) {
	if !s.primed {
		text, s.primed = preamble+text, true
	}
	svc, id := p.agent, s.child
	var task kernel.Task
	kernel.Go(s.scope, &task, func(ctx context.Context) (agent.SendDisposition, error) {
		return svc.Send(ctx, agent.Send{Session: id, Text: text})
	}, func(d agent.SendDisposition, err error) {
		switch {
		case err == nil:
			if d == agent.SendHandled {
				s.sending = false
			}
		case errors.Is(err, agent.ErrNotSent):
			s.sending = false
			s.add(entryNote, "The message was not sent"+strings.TrimPrefix(err.Error(), agent.ErrNotSent.Error())+".")
		default:
			s.sending = false
			s.add(entryNote, "The message may not have reached the agent: "+err.Error()+". Check before sending it again.")
		}
	})
}

func (p *Plugin) stop() {
	s := p.side
	if s == nil || s.child == "" || !s.running() {
		return
	}
	svc, id := p.agent, s.child
	var task kernel.Task
	kernel.Go(s.scope, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, svc.Abort(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			s.add(entryNote, "The agent did not stop: "+err.Error())
		}
	})
}

// events follow the side chat's turns into the drawer, also while it is
// closed or another conversation is on show.
func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		s := p.sides[ev.Session]
		if s == nil {
			continue
		}
		if s == p.side && p.shown() {
			p.ctx.Invalidate() // the drawer shows its side chat as it goes
		}
		switch ev.Kind {
		case agent.TurnStarted:
			s.busy, s.sending = true, false
		case agent.TextStarted, agent.TextDelta:
			e := p.liveText(s, ev.Text)
			e.text += ev.Text.Delta
		case agent.TextEnded:
			e := p.liveText(s, ev.Text)
			e.text, e.streaming = ev.Text.Text, false
			delete(s.live, textKey{ev.Text.Message, ev.Text.Ordinal})
		case agent.ToolCalled:
			s.add(entryNote, toolLabel(ev.Tool.Kind))
		case agent.TurnSucceeded, agent.TurnInterrupted:
			settle(s)
		case agent.TurnFailed:
			settle(s)
			s.add(entryNote, strings.TrimSuffix("The agent stopped with an error: "+ev.Error, ": "))
		case agent.ToolStarted, agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested,
			agent.ModelChanged, agent.SessionRenamed:
		}
	}
}

func (p *Plugin) liveText(s *side, t *agent.TextEvent) *entry {
	key := textKey{t.Message, t.Ordinal}
	if e := s.live[key]; e != nil {
		return e
	}
	e := s.add(entryAnswer, "")
	e.streaming = true
	s.live[key] = e
	return e
}

// settle ends a turn: words still arriving stand as they are.
func settle(s *side) {
	for _, e := range s.live {
		e.streaming = false
	}
	s.entries = slices.DeleteFunc(s.entries, func(e *entry) bool { return e.kind == entryAnswer && strings.TrimSpace(e.text) == "" })
	clear(s.live)
	s.busy, s.sending = false, false
}

func toolLabel(kind agent.ToolKind) string {
	switch kind {
	case agent.ToolRead:
		return "Read files"
	case agent.ToolSearch:
		return "Searched"
	case agent.ToolShell, agent.ToolEdit, agent.ToolFetch, agent.ToolOther:
	}
	return "Used a tool"
}

// statusChanged closes the drawer when the backend's origin changes, settles a
// turn the connection dropped under, and restores the drawer once the backend
// is ready, since a side chat belongs to its origin.
func (p *Plugin) statusChanged(st agent.Status) {
	p.forget(func(s *side) bool { return s.origin == st.Origin })
	if st.State == agent.Down {
		for _, s := range p.sides {
			if s.running() {
				settle(s)
				s.add(entryNote, "The connection dropped while the agent was working.")
			}
		}
	}
	if st.State == agent.Ready {
		p.restore()
	}
}

// addToChat appends an answer to the main conversation's draft, which takes
// the focus, for the user to edit and send. It never sends it.
func (p *Plugin) addToChat(text string) {
	if !p.shown() {
		return
	}
	draft := strings.TrimRight(p.chat.DraftText(), "\n")
	if strings.TrimSpace(draft) != "" {
		draft += "\n\n"
	}
	p.chat.SetDraft(draft + "From a side chat:\n\n" + strings.TrimSpace(text))
}

func (p *Plugin) saveLinks() {
	if err := p.ctx.Settings().Set("side-chats-v1", p.links); err != nil {
		p.host.Toast(ui.Toast{Title: "Could not remember the side chat", Description: err.Error(), Type: components.ToastError})
	}
}

// prune drops the side chats of conversations this origin's record no longer
// has, such as deleted ones.
func (p *Plugin) prune() {
	origin := p.agent.Status().Origin
	if origin == "" || !p.ws.Loaded() {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(p.links), func(l link) bool {
		if l.Origin != origin {
			return false
		}
		_, ok := p.ws.Lookup(l.Parent)
		return !ok
	})
	if len(kept) != len(p.links) {
		p.links = kept
		p.saveLinks()
		p.forget(func(s *side) bool { _, ok := p.ws.Lookup(s.parent.Session); return ok || s.origin != origin })
	}
}

// quote makes text a Markdown block quote, line by line.
func quote(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}
