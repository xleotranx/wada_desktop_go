// Package models is the composer's model and effort pickers. A new
// conversation starts with the model chosen here, remembered across launches.
// Choosing a model in an open conversation switches it there. Its settings
// section turns models off so the model picker leaves them out.
package models

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// Plugin is the model picker.
type Plugin struct {
	ctx    *kernel.Context
	agent  agent.Service
	models agent.Models
	chat   chat.Controller
	host   shell.Host

	catalog  []agent.Model
	fallback agent.ModelRef // the backend's default model
	loaded   bool
	attempt  uint64
	load     kernel.Task
	switcher kernel.Task
	pending  agent.ModelRef // asked of the open conversation, not yet confirmed
	session  agent.SessionID
	scope    *kernel.Scope // model switches belong to the current conversation

	newModel *kernel.Setting[agent.ModelRef]
	// hidden holds the modelKey of each model turned off in settings; a model
	// the backend adds later is shown until it is turned off.
	hidden    *kernel.Setting[[]string]
	query     string          // the settings section's model search
	collapsed map[string]bool // provider groups closed in settings

	modelPicker, effortPicker onechat.PickerState
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "models", Title: "Model picker",
		Description: "Choose the model and effort in the composer.",
		Requires:    []kernel.Named{agent.ServiceKey, agent.ModelsKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.collapsed = make(map[string]bool)
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.models = kernel.Require(c, agent.ModelsKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.newModel = kernel.NewSetting(c, "new-chat-model", agent.ModelRef{})
	p.hidden = kernel.NewSetting(c, "hidden-models", []string(nil))
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
	kernel.Contribute(c, settings.Sections, "models.section", 12, settings.Section{
		Title: "Models", Description: "Choose which models the composer's model picker lists.",
		Render: p.section})
	return kernel.Contribute(c, chat.ComposerPrimary, "models.model", 0, shell.View{Render: p.view})
}

func (p *Plugin) conversationChanged(conv chat.Conversation) {
	if p.scope == nil || conv.Session != p.session {
		if p.scope != nil {
			p.scope.Dispose()
		}
		p.scope = p.ctx.NewScope()
		p.session = conv.Session
		p.pending = agent.ModelRef{}
	}
	if conv.Session == "" || conv.Model == p.pending {
		p.pending = agent.ModelRef{}
	}
}

type catalog struct {
	models   []agent.Model
	fallback agent.ModelRef
}

// reload reads the catalog from the current connection. The default model is
// best effort; the model list is required.
func (p *Plugin) reload() {
	p.attempt = p.agent.Status().Attempt
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) (catalog, error) {
		var out catalog
		var err error
		if out.models, err = p.models.Models(ctx); err != nil {
			return out, err
		}
		out.fallback, _ = p.models.DefaultModel(ctx)
		return out, nil
	}, func(cat catalog, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The model list did not load", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.catalog, p.fallback, p.loaded = cat.models, cat.fallback, true
	})
}

// outgoing gives a new conversation the chosen model, unless another
// transform chose already or the backend no longer offers it.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	if m := p.newModel.Get(); d.Session == "" && d.Model.IsZero() && !m.IsZero() && (!p.loaded || p.find(m) != nil) {
		d.Model = m
	}
	return d, nil, nil
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

// view is the model picker, with the effort picker beside it for a model that
// has efforts.
func (p *Plugin) view(c *ui.Context) {
	pal := native.PaletteFor(c)
	ref := p.current()
	options := make([]onechat.Option, 0, len(p.catalog))
	hidden := p.hiddenSet()
	for _, m := range p.catalog {
		if hidden[modelKey(m.Ref)] {
			continue
		}
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
	switch {
	case !p.loaded:
		empty = "Loading models…"
	case len(options) == 0 && len(p.catalog) > 0:
		empty = "Every model is turned off in Settings → Models"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).MinWidth(0).Children(func() {
		{
			c.Key("model")
			picked := onechat.Picker(c, &p.modelPicker, onechat.PickerOptions{
				Label: p.label(ref), SearchLabel: "Search models", EmptyText: empty,
				Icon: lucide.Cpu, Options: options, Selected: modelKey(ref), Width: 300,
			})
			picked.Trigger.MinWidth(70).MaxWidth(200).Background(pal.Selected).Tooltip("Model: " + p.label(ref))
			if picked.Chosen != "" {
				i := slices.IndexFunc(p.catalog, func(m agent.Model) bool { return modelKey(m.Ref) == picked.Chosen })
				if i >= 0 {
					p.choose(p.catalog[i].Ref)
				}
			}
			if m := p.find(ref); m != nil && len(m.Variants) > 0 {
				c.Key("effort")
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
				chosen.Trigger.Shrink(0).MaxWidth(120).Tooltip("Reasoning effort")
				if chosen.Chosen != "" {
					next := ref
					next.Variant = chosen.Chosen
					if next.Variant == defaultEffort {
						next.Variant = ""
					}
					p.choose(next)
				}
			}
		}
	})
}

// defaultEffort is the picker ID of the model's own default effort; picker
// IDs can't be empty.
const defaultEffort = "(default)"

func (p *Plugin) hiddenSet() map[string]bool {
	set := map[string]bool{}
	for _, k := range p.hidden.Get() {
		set[k] = true
	}
	return set
}

// show turns models on or off for the model picker.
func (p *Plugin) show(on bool, refs ...agent.ModelRef) {
	set := p.hiddenSet()
	for _, ref := range refs {
		if on {
			delete(set, modelKey(ref))
		} else {
			set[modelKey(ref)] = true
		}
	}
	keys := slices.Sorted(maps.Keys(set))
	p.hidden.Set(keys)
}

// section lists the catalog as a compact checklist, grouped by provider.
func (p *Plugin) section(c *ui.Context) {
	pal := native.PaletteFor(c)
	if !p.loaded {
		ui.Text(c, "Loading models…").TextColor(pal.Muted)
		return
	}
	if len(p.catalog) == 0 {
		ui.Text(c, "No models yet. Connect a provider to list its models.").TextColor(pal.Muted)
		return
	}
	ui.Column(c).FillWidth().MaxWidth(600).MinWidth(0).Gap(12).Children(func() {
		p.modelChecklist(c, pal)
	})
}

func (p *Plugin) modelChecklist(c *ui.Context, pal native.Palette) {
	c.Key("models-search")
	components.TextBox(c, &p.query, components.TextBoxOptions{}).Placeholder("Search models").FillWidth()
	query := strings.ToLower(strings.TrimSpace(p.query))
	hidden := p.hiddenSet()
	var providers []string
	byProvider := map[string][]agent.Model{}
	for _, m := range p.catalog {
		name := cmp.Or(m.Name, m.Ref.ID)
		if query != "" && !strings.Contains(strings.ToLower(name+" "+m.Ref.ID+" "+m.Ref.Provider), query) {
			continue
		}
		if _, ok := byProvider[m.Ref.Provider]; !ok {
			providers = append(providers, m.Ref.Provider)
		}
		byProvider[m.Ref.Provider] = append(byProvider[m.Ref.Provider], m)
	}
	if len(providers) == 0 {
		ui.Text(c, "No models match").TextColor(pal.Muted)
		return
	}
	for _, provider := range providers {
		models := byProvider[provider]
		all := make([]agent.ModelRef, len(models))
		shown := 0
		for i, m := range models {
			all[i] = m.Ref
			if !hidden[modelKey(m.Ref)] {
				shown++
			}
		}
		c.Key("provider:" + provider)
		ui.Column(c).FillWidth().Gap(0).Children(func() {
			expanded := query != "" || !p.collapsed[provider]
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Padding(4, 0, 8, 0).Children(func() {
				chevron := lucide.ChevronRight
				if expanded {
					chevron = lucide.ChevronDown
				}
				button := ui.ButtonBase(c.Key("disclosure")).Label(provider).Role(ui.RoleDisclosure).Expanded(expanded).
					Grow(1).MinWidth(0).Justify(ui.Start).Height(28).Padding(0, 4).Gap(6).Radius(4).Cursor(ui.CursorPointer).
					OnClick(func() { p.collapsed[provider] = expanded })
				if button.Hovered() {
					button.Background(pal.Hover)
				}
				button.Children(func() {
					ui.Icon(c, chevron()).FontSize(12).TextColor(pal.Muted).Shrink(0)
					ui.Text(c, provider).FontSize(13).FontWeight(600).TextColor(pal.Text).MinWidth(0).SingleLine().Ellipsis("…")
					ui.Text(c, strconv.Itoa(shown)+" of "+strconv.Itoa(len(models))+" enabled").FontSize(12).TextColor(pal.Muted).Shrink(0)
				})
				components.Button(c.Key("all"), "Enable all", components.ButtonGhost).Padding(4, 8).
					OnClick(func() { p.show(true, all...) })
				components.Button(c.Key("none"), "Disable all", components.ButtonGhost).Padding(4, 8).
					OnClick(func() { p.show(false, all...) })
			})
			if !expanded {
				return
			}
			for _, m := range models {
				name := cmp.Or(m.Name, m.Ref.ID)
				ui.Column(c.Key(modelKey(m.Ref))).FillWidth().Children(func() {
					on := !hidden[modelKey(m.Ref)]
					checkbox := components.Checkbox(c, &on, name).FillWidth().MinWidth(0).Height(32).Padding(0, 8).
						Tooltip(m.Ref.ID).OnClick(func() { p.show(on, m.Ref) })
					if checkbox.Hovered() {
						checkbox.Background(pal.Hover)
					}
					ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
				})
			}
		})
	}
}
