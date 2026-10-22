// Package usage shows what the open conversation has cost and how full its
// context window is: as Context and Cost in the work panel, or, while that is
// hidden, at the end of the composer with a popover that breaks the latest
// answer's tokens down and totals the conversation. The backends report usage
// with the stored messages, not in their events, so the plugin reads the
// conversation's messages when it opens and again after every turn.
package usage

import (
	"context"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
)

// Plugin is the usage meter. The zero value is ready to install.
//
// The context window comes from the model catalog when the backend provides
// one; without it the meter shows the tokens alone.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller

	session agent.SessionID // the conversation measured
	sum     summary         // of session, once known
	known   bool
	load    kernel.Task

	catalog     []agent.Model
	catalogRead bool
	catalogTask kernel.Task

	open bool // the panel
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "usage", Title: "Usage",
		Description: "The open conversation's cost and context window, in the work panel or the composer.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.session, p.known, p.catalog, p.catalogRead = "", false, nil, false
	kernel.On(c, chat.Changed, func(conv chat.Conversation) { p.follow(conv.Session) })
	kernel.On(c, agent.Events, p.events)
	// A read before the backend connected failed; read again once it has.
	attempt := p.agent.Status().Attempt
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != attempt && p.session != "" {
			p.reload()
		}
		if st.State == agent.Ready {
			attempt = st.Attempt
		}
	})
	p.follow(p.chat.Current().Session)
	for _, err := range []error{
		kernel.Contribute(c, chat.ComposerTrailing, "usage.meter", 0, shell.View{Render: p.view}),
		kernel.Contribute(c, chat.Work, "usage.context", 20, chat.WorkSection{Title: "Context",
			Shown: func() bool { return p.measured() && p.sum.last.Context() > 0 }, Render: p.contextSection}),
		kernel.Contribute(c, chat.Work, "usage.cost", 30, chat.WorkSection{Title: "Cost",
			Shown: p.measured, Render: p.costSection}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// follow measures the conversation on show from now on.
func (p *Plugin) follow(id agent.SessionID) {
	if id == p.session {
		return
	}
	p.load.Cancel()
	p.session, p.sum, p.known, p.open = id, summary{}, false, false
	if id != "" {
		p.reload()
	}
}

// events reads the usage again once a turn of the measured conversation ends.
func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		if ev.Session == p.session && p.session != "" &&
			(ev.Kind == agent.TurnSucceeded || ev.Kind == agent.TurnFailed || ev.Kind == agent.TurnInterrupted) {
			p.reload()
			return
		}
	}
}

func (p *Plugin) reload() {
	id := p.session
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) (summary, error) {
		messages, err := p.agent.Messages(ctx, id)
		return summarize(messages), err
	}, func(sum summary, err error) {
		// A conversation that can't be read shows no usage; the chat page
		// reports the failure.
		if err != nil || id != p.session {
			return
		}
		p.sum, p.known = sum, true
		p.readCatalog()
	})
}

// readCatalog reads the models once, for their context windows.
func (p *Plugin) readCatalog() {
	models, ok := kernel.Use(p.ctx, agent.ModelsKey)
	if !ok || p.catalogRead || p.catalogTask.Running() {
		return
	}
	kernel.Go(p.ctx, &p.catalogTask, models.Models, func(catalog []agent.Model, err error) {
		if err == nil {
			p.catalog, p.catalogRead = catalog, true
		}
	})
}

// summary is a conversation's usage.
type summary struct {
	cost    float64      // the whole conversation's
	total   agent.Tokens // the whole conversation's
	replies int          // assistant messages

	// The latest answer that reported tokens: what fills the context now.
	last     agent.Tokens
	lastCost float64
	model    agent.ModelRef
}

func summarize(messages []agent.Message) summary {
	var s summary
	for _, m := range messages {
		if m.Role != agent.RoleAssistant {
			continue
		}
		s.replies++
		s.cost += m.Cost
		t := m.Tokens
		s.total.Input += t.Input
		s.total.Output += t.Output
		s.total.Reasoning += t.Reasoning
		s.total.CacheRead += t.CacheRead
		s.total.CacheWrite += t.CacheWrite
		if t.Context() > 0 {
			s.last, s.lastCost, s.model = t, m.Cost, m.Model
		}
	}
	return s
}

// model is the catalog's entry for the latest answer's model; zero when the
// catalog doesn't have it.
func (p *Plugin) model() agent.Model {
	for _, m := range p.catalog {
		if m.Ref.Provider == p.sum.model.Provider && m.Ref.ID == p.sum.model.ID {
			return m
		}
	}
	return agent.Model{}
}
