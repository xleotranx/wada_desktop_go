// Package activity fills the work panel with what the open conversation did:
// the files it changed, with their diffs, when the backend can tell
// (agent.ChangesKey), and the last shell command it ran, with its output.
// Both are read when a conversation opens; the command then follows the
// agent's events, and the changes are read again after every turn.
package activity

import (
	"context"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/kernel"
)

// Plugin is the work panel's activity. The zero value is ready to install.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller

	session agent.SessionID // the conversation followed
	last    *agent.Tool     // its latest finished shell command
	history kernel.Task
	// shell holds the calls in flight known to be shell commands: an event
	// after ToolStarted may not say what kind its tool is.
	shell map[string]bool

	changes     []agent.FileChange
	changesTask kernel.Task
	open        map[string]bool // files whose diff is shown, by path
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "activity", Title: "Activity",
		Description: "The files a conversation changed and its last command, in the work panel.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.session, p.last, p.changes, p.open, p.shell = "", nil, nil, map[string]bool{}, map[string]bool{}
	kernel.On(c, chat.Changed, func(conv chat.Conversation) { p.follow(conv.Session) })
	kernel.On(c, agent.Events, p.events)
	p.follow(p.chat.Current().Session)
	for _, err := range []error{
		kernel.Contribute(c, chat.Work, "activity.changes", 0, chat.WorkSection{Title: "Changes",
			Shown: func() bool { return len(p.changes) > 0 }, Render: p.changesView}),
		kernel.Contribute(c, chat.Work, "activity.command", 10, chat.WorkSection{Title: "Last command",
			Shown: func() bool { return p.last != nil }, Render: p.commandView}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// follow shows the conversation on show from now on.
func (p *Plugin) follow(id agent.SessionID) {
	if id == p.session {
		return
	}
	p.history.Cancel()
	p.changesTask.Cancel()
	p.session, p.last, p.changes = id, nil, nil
	clear(p.open)
	clear(p.shell)
	if id == "" {
		return
	}
	kernel.Go(p.ctx, &p.history, func(ctx context.Context) (*agent.Tool, error) {
		messages, err := p.agent.Messages(ctx, id)
		return lastCommand(messages), err
	}, func(tool *agent.Tool, err error) {
		// An event may have brought a later command already.
		if err == nil && id == p.session && p.last == nil {
			p.last = tool
		}
	})
	p.readChanges()
}

func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		if ev.Session != p.session || p.session == "" {
			continue
		}
		switch ev.Kind {
		case agent.ToolStarted, agent.ToolCalled:
			if ev.Tool != nil && (ev.Tool.Kind == agent.ToolShell || ev.Tool.Command != "") {
				p.shell[ev.Tool.ID] = true
			}
		case agent.ToolFinished:
			if ev.Tool != nil && (ev.Tool.Kind == agent.ToolShell || p.shell[ev.Tool.ID]) {
				tool := *ev.Tool
				tool.Kind = agent.ToolShell
				p.last = &tool
				delete(p.shell, ev.Tool.ID)
			}
		case agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			clear(p.shell)
			p.readChanges()
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ApprovalRequested, agent.InteractionRequested, agent.TurnStarted, agent.ModelChanged,
			agent.SessionRenamed:
		}
	}
}

// readChanges asks the backend which files the conversation changed, when it
// can tell. A failed read keeps what was shown.
func (p *Plugin) readChanges() {
	source, ok := kernel.Use(p.ctx, agent.ChangesKey)
	if !ok {
		p.changes = nil
		return
	}
	id := p.session
	kernel.Go(p.ctx, &p.changesTask, func(ctx context.Context) ([]agent.FileChange, error) {
		return source.Changes(ctx, id)
	}, func(changes []agent.FileChange, err error) {
		if err == nil && id == p.session {
			p.changes = changes
		}
	})
}

// lastCommand is the latest shell command in stored messages.
func lastCommand(messages []agent.Message) *agent.Tool {
	for i := len(messages) - 1; i >= 0; i-- {
		parts := messages[i].Parts
		for j := len(parts) - 1; j >= 0; j-- {
			if t := parts[j].Tool; parts[j].Kind == agent.PartTool && t != nil && t.Kind == agent.ToolShell {
				tool := *t
				return &tool
			}
		}
	}
	return nil
}

// output is a command's output as lines, without a trailing blank one.
func output(t *agent.Tool) []string {
	text := strings.TrimRight(strings.Join(t.Output, ""), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
