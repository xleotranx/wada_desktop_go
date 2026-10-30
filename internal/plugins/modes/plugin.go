// Package modes is the composer's mode picker. The chosen mode applies to
// every outgoing message and is remembered across launches; a backend without
// modes keeps its own behavior. Workflows apply to the next submitted task
// while preserving the chosen access mode.
package modes

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin is the mode picker.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller

	modes   []agent.Mode
	sourced bool   // a provider of agent.ModesKey was there at the last load
	attempt uint64 // the connection attempt the modes were loaded for
	load    kernel.Task
	mode    *kernel.Setting[string]
	// selectedWorkflow is the one-task behavior for the next submitted message.
	// It is not remembered: the picker returns to the chosen mode once
	// the message is sent, when another conversation opens and when the new
	// chat's folder changes.
	selectedWorkflow string
	session          agent.SessionID
	directory        string // the new chat's folder the workflow was chosen in
	picker           onechat.PickerState
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "modes", Title: "Mode picker",
		Description: "Choose the mode in the composer, or apply a workflow to the next task.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.mode = kernel.NewSetting(c, "mode", "")
	p.follow(p.chat.Current())
	kernel.On(c, chat.Changed, p.follow)
	kernel.On(c, chat.WorkflowSelected, func(id string) { p.selectedWorkflow = id })
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != p.attempt {
			p.reload()
		}
	})
	// The modes come from an optional provider; load again when one comes or goes.
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if _, ok := kernel.Use(c, agent.ModesKey); ok != p.sourced && p.agent.Status().State == agent.Ready {
			p.reload()
		}
	})
	if p.agent.Status().State == agent.Ready {
		p.reload()
	}
	// Stamp selection early; attachments, mentions and skills still run afterward.
	kernel.Transform(c, chat.Outgoing, -10, p.outgoing)
	return kernel.Contribute(c, chat.ComposerAccessory, "modes.mode", 0, shell.View{Render: p.view})
}

func (p *Plugin) follow(conv chat.Conversation) {
	// Two new chats share the empty session; their folders tell them apart.
	if conv.Session != p.session || conv.Directory != p.directory {
		p.session, p.directory, p.selectedWorkflow = conv.Session, conv.Directory, ""
	}
}

// reload reads the backend's modes from the current connection; they are best
// effort, and a backend without them has none.
func (p *Plugin) reload() {
	p.attempt = p.agent.Status().Attempt
	modes, ok := kernel.Use(p.ctx, agent.ModesKey)
	p.sourced = ok
	if !ok {
		p.load.Cancel()
		p.modes = nil
		return
	}
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) ([]agent.Mode, error) {
		list, _ := modes.Modes(ctx)
		return list, nil
	}, func(list []agent.Mode, _ error) { p.modes = list })
}

// outgoing preserves access mode and stamps the selected workflow. The commit
// clears it only after every outgoing transform and delivery validation accepts.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	if id := p.selectedWorkflow; id != "" {
		found := false
		for _, e := range kernel.Items(p.ctx, chat.Workflows) {
			if e.ID == id {
				found = true
			}
		}
		if !found {
			return d, nil, fmt.Errorf("the selected workflow is unavailable; choose again")
		}
		d.Workflow = id
		if d.Mode == "" {
			d.Mode = p.selectedMode()
		}
		return d, func() {
			if p.selectedWorkflow == id {
				p.selectedWorkflow = ""
			}
		}, nil
	}
	if d.Mode == "" {
		d.Mode = p.selectedMode()
	}
	return d, nil, nil
}

// selectedMode keeps the label and outgoing mode in step when the backend's
// choices change. A backend without modes keeps its own behavior.
func (p *Plugin) selectedMode() string {
	if len(p.modes) == 0 {
		return ""
	}
	if selected := p.mode.Get(); slices.ContainsFunc(p.modes, func(m agent.Mode) bool { return m.ID == selected }) {
		return selected
	}
	return p.modes[0].ID
}

const (
	workflowPrefix = "workflow:" // distinguishes a workflow picker ID from a mode's
	defaultMode    = "(default)"
)

// view picks the mode for outgoing messages, or a target for the next one. A
// backend without modes still has its own way of chatting, to choose against
// the targets; with nothing to choose between it shows nothing.
func (p *Plugin) view(c *ui.Context) {
	targets := kernel.Items(p.ctx, chat.Workflows)
	if max(len(p.modes), 1)+len(targets) <= 1 {
		return
	}
	c.Key("mode")
	var options []onechat.Option
	for _, m := range p.modes {
		options = append(options, onechat.Option{ID: m.ID, Label: cmp.Or(m.Name, m.ID), Description: m.Description})
	}
	if len(p.modes) == 0 {
		options = append(options, onechat.Option{ID: defaultMode, Label: "Chat"})
	}
	if len(targets) > 0 {
		for i := range options {
			options[i].Group = "Modes"
		}
		for _, e := range targets {
			options = append(options, onechat.Option{ID: workflowPrefix + e.ID, Label: e.Value.Title,
				Description: e.Value.Summary, Group: "Workflows"})
		}
	}
	selected := cmp.Or(p.selectedMode(), defaultMode)
	if p.selectedWorkflow != "" {
		selected = workflowPrefix + p.selectedWorkflow
	}
	label := selected
	for _, o := range options {
		if o.ID == selected {
			label = o.Label
		}
	}
	picked := onechat.Picker(c, &p.picker, onechat.PickerOptions{
		Label: label, Options: options, Selected: selected, Width: 280,
	})
	if chosen := picked.Chosen; chosen != "" {
		// Choosing is not rendering's job: act once the frame is done.
		p.ctx.Post(func() { p.pick(chosen) })
	}
}

func (p *Plugin) pick(chosen string) {
	if id, isTarget := strings.CutPrefix(chosen, workflowPrefix); isTarget {
		p.selectedWorkflow = id
		return
	}
	p.selectedWorkflow = ""
	if chosen != defaultMode {
		p.mode.Set(chosen)
	}
}
