// Package models is the composer's model, effort and mode pickers. A new
// conversation starts with the model and mode chosen here (remembered across
// launches); choosing a model in an open conversation switches it there. The
// mode picker also lists the chat.Targets other plugins contribute, such as a
// workflow: choosing one sends the next message there instead of to the agent.
package models

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Plugin is the model picker. The zero value is ready to install.
type Plugin struct {
	ctx    *kernel.Context
	agent  agent.Service
	models agent.Models
	chat   chat.Controller
	host   shell.Host

	catalog  []agent.Model
	fallback agent.ModelRef // the backend's default model
	modes    []agent.Mode
	loaded   bool
	attempt  uint64
	load     kernel.Task
	switcher kernel.Task
	pending  agent.ModelRef // asked of the open conversation, not yet confirmed
	session  agent.SessionID
	scope    *kernel.Scope // model switches belong to the current conversation

	newModel *kernel.Setting[agent.ModelRef]
	newMode  *kernel.Setting[string]
	// target is the chat.Targets entry the next message goes to, instead of the
	// agent. It is not remembered: the picker returns to the chosen mode once
	// the message is sent, when another conversation opens and when the new
	// chat's folder changes.
	target    string
	directory string // the new chat's folder the target was chosen in

	modelPicker, effortPicker, modePicker onechat.PickerState
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "models", Title: "Model picker",
		Description: "Choose the model, effort and mode in the composer.",
		Requires:    []kernel.Named{agent.ServiceKey, agent.ModelsKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.models = kernel.Require(c, agent.ModelsKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.newModel = kernel.NewSetting(c, "new-chat-model", agent.ModelRef{})
	p.newMode = kernel.NewSetting(c, "new-chat-mode", "")
	p.scope = nil
	p.conversationChanged(p.chat.Current())
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != p.attempt {
			p.reload()
		}
	})
	kernel.On(c, agent.ModelsChanged, func(struct{}) {
		if p.agent.Status().State == agent.Ready {
			p.reload()
		}
	})
	kernel.On(c, chat.Changed, p.conversationChanged)
	if p.agent.Status().State == agent.Ready {
		p.reload()
	}
	kernel.Transform(c, chat.Outgoing, 0, p.outgoing)
	return kernel.Contribute(c, chat.ComposerAccessory, "models.pickers", 0, shell.View{Render: p.view})
}

func (p *Plugin) conversationChanged(conv chat.Conversation) {
	if p.scope == nil || conv.Session != p.session {
		if p.scope != nil {
			p.scope.Dispose()
		}
		p.scope = p.ctx.NewScope()
		p.session = conv.Session
		p.pending = agent.ModelRef{}
		p.target = ""
	}
	// Two new chats share the empty session; their folders tell them apart.
	if conv.Directory != p.directory {
		p.directory = conv.Directory
		p.target = ""
	}
	if conv.Session == "" || conv.Model == p.pending {
		p.pending = agent.ModelRef{}
	}
}

type catalog struct {
	models   []agent.Model
	fallback agent.ModelRef
	modes    []agent.Mode
}

// reload reads the catalog from the current connection. The default model and
// modes are best effort; the model list is required.
func (p *Plugin) reload() {
	p.attempt = p.agent.Status().Attempt
	modes, _ := kernel.Use(p.ctx, agent.ModesKey)
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) (catalog, error) {
		var out catalog
		var err error
		if out.models, err = p.models.Models(ctx); err != nil {
			return out, err
		}
		out.fallback, _ = p.models.DefaultModel(ctx)
		if modes != nil {
			out.modes, _ = modes.Modes(ctx)
		}
		return out, nil
	}, func(cat catalog, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The model list did not load", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.catalog, p.fallback, p.modes, p.loaded = cat.models, cat.fallback, cat.modes, true
	})
}

// outgoing gives a new conversation the chosen model and mode, unless another
// transform chose already, or the backend no longer offers them. A message for
// a target is handed to it and goes no further; one that is gone, or fails
// to take it, is an error, so the message stays in the composer and never
// reaches the agent in its place.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, error) {
	if d.Session != "" {
		return d, nil
	}
	if id := p.target; id != "" {
		p.target = ""
		taken := false
		for _, e := range kernel.Items(p.ctx, chat.Targets) {
			if e.ID == id {
				kernel.Invoke(p.ctx, e, func(t chat.Target) { t.Take(d); taken = true })
			}
		}
		if !taken {
			return d, errTargetGone
		}
		return d, chat.ErrTaken
	}
	if m := p.newModel.Get(); d.Model.IsZero() && !m.IsZero() && (!p.loaded || p.find(m) != nil) {
		d.Model = m
	}
	if mode := p.newMode.Get(); d.Mode == "" && mode != "" &&
		(!p.loaded || slices.ContainsFunc(p.modes, func(x agent.Mode) bool { return x.ID == mode })) {
		d.Mode = mode
	}
	return d, nil
}

// current is the model the composer stands on: the open conversation's, or
// the choice for a new one, or the backend's default.
func (p *Plugin) current() agent.ModelRef {
	conv := p.chat.Current()
	switch {
	case conv.Session == "":
		if m := p.newModel.Get(); !m.IsZero() {
			return m
		}
	case !p.pending.IsZero():
		return p.pending
	case !conv.Model.IsZero():
		return conv.Model
	}
	return p.fallback
}

func (p *Plugin) find(ref agent.ModelRef) *agent.Model {
	i := slices.IndexFunc(p.catalog, func(m agent.Model) bool {
		return m.Ref.Provider == ref.Provider && m.Ref.ID == ref.ID
	})
	if i < 0 {
		return nil
	}
	return &p.catalog[i]
}

// choose applies a model: remembered for new conversations, switched in the
// open one.
func (p *Plugin) choose(ref agent.ModelRef) {
	conv := p.chat.Current()
	if conv.Session == "" {
		p.newModel.Set(ref)
		return
	}
	p.pending = ref
	models := p.models
	kernel.Go(p.scope, &p.switcher, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, models.SetModel(ctx, conv.Session, ref)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.pending = agent.ModelRef{}
			p.host.Toast(ui.Toast{Title: "The model did not change", Description: err.Error(), Type: components.ToastError})
		}
	})
}

func modelKey(ref agent.ModelRef) string { return ref.Provider + "/" + ref.ID }

func (p *Plugin) label(ref agent.ModelRef) string {
	if m := p.find(ref); m != nil && m.Name != "" {
		return m.Name
	}
	if ref.ID != "" {
		return ref.ID
	}
	return "Default model"
}

func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	ref := p.current()
	options := make([]onechat.Option, 0, len(p.catalog))
	for _, m := range p.catalog {
		o := onechat.Option{ID: modelKey(m.Ref), Label: m.Name, Group: m.Ref.Provider}
		if o.Label == "" {
			o.Label = m.Ref.ID
		}
		if m.Free {
			o.Detail = "Free"
		}
		options = append(options, o)
	}
	empty := "No models match"
	if !p.loaded {
		empty = "Loading models…"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).MinWidth(0).Children(func() {
		picked := onechat.Picker(c, &p.modelPicker, onechat.PickerOptions{
			Label: p.label(ref), SearchLabel: "Search models", EmptyText: empty,
			Icon: lucide.Cpu, Options: options, Selected: modelKey(ref), Width: 300,
		})
		picked.Trigger.MaxWidth(220).Background(pal.Selected).Tooltip("Model: " + p.label(ref))
		if picked.Chosen != "" {
			i := slices.IndexFunc(p.catalog, func(m agent.Model) bool { return modelKey(m.Ref) == picked.Chosen })
			if i >= 0 {
				p.choose(p.catalog[i].Ref)
			}
		}
		if m := p.find(ref); m != nil && len(m.Variants) > 0 {
			efforts := []onechat.Option{{ID: defaultEffort, Label: "Default"}}
			for _, v := range m.Variants {
				efforts = append(efforts, onechat.Option{ID: v, Label: v})
			}
			effortLabel := ref.Variant
			if effortLabel == "" {
				effortLabel = "Default effort"
			}
			selected := ref.Variant
			if selected == "" {
				selected = defaultEffort
			}
			chosen := onechat.Picker(c, &p.effortPicker, onechat.PickerOptions{
				Label: effortLabel, Options: efforts, Selected: selected, Width: 200,
			})
			chosen.Trigger.MaxWidth(120).Tooltip("Reasoning effort")
			if chosen.Chosen != "" {
				next := ref
				next.Variant = chosen.Chosen
				if next.Variant == defaultEffort {
					next.Variant = ""
				}
				p.choose(next)
			}
		}
		targets := kernel.Items(p.ctx, chat.Targets)
		// A backend without modes still has its own way of chatting, to choose
		// against the targets.
		if p.chat.Current().Session == "" && max(len(p.modes), 1)+len(targets) > 1 {
			p.modeView(c, targets)
		}
	})
}

// defaultEffort is the picker ID of the model's own default effort; picker
// IDs can't be empty.
const defaultEffort = "(default)"

// errTargetGone is reported when the chosen target did not take the message.
var errTargetGone = errors.New("what the mode picker chose for it is no longer available; choose again")

const (
	targetPrefix = "target:" // tells a target's picker ID from a mode's
	defaultMode  = "(default)"
)

// modeView picks the mode of a new chat, or one of the targets for its first
// message.
func (p *Plugin) modeView(c *ui.Context, targets []kernel.Entry[chat.Target]) {
	var options []onechat.Option
	for _, m := range p.modes {
		options = append(options, onechat.Option{ID: m.ID, Label: cmp.Or(m.Name, m.ID), Description: m.Description})
	}
	if len(p.modes) == 0 { // a backend without modes still has its own way of chatting
		options = append(options, onechat.Option{ID: defaultMode, Label: "Chat"})
	}
	if len(targets) > 0 {
		for i := range options {
			options[i].Group = "Modes"
		}
		for _, e := range targets {
			options = append(options, onechat.Option{ID: targetPrefix + e.ID, Label: e.Value.Title,
				Description: e.Value.Summary, Group: cmp.Or(e.Value.Group, "More")})
		}
	}
	selected := p.newMode.Get()
	switch {
	case p.target != "":
		selected = targetPrefix + p.target
	case selected == "" && len(p.modes) > 0:
		selected = p.modes[0].ID
	case selected == "":
		selected = defaultMode
	}
	label := selected
	for _, o := range options {
		if o.ID == selected {
			label = o.Label
		}
	}
	picked := onechat.Picker(c, &p.modePicker, onechat.PickerOptions{
		Label: label, Options: options, Selected: selected, Width: 280,
	})
	switch id, isTarget := strings.CutPrefix(picked.Chosen, targetPrefix); {
	case picked.Chosen == "":
	case isTarget:
		p.target = id
	default:
		p.target = ""
		if picked.Chosen != defaultMode {
			p.newMode.Set(picked.Chosen)
		}
	}
}
