package chat

import (
	"context"
	"slices"
	"strconv"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

func (p *Plugin) answer(state *interactionState, answer agent.InteractionAnswer) {
	if state.submitting {
		return
	}
	backend, ok := kernel.Use(p.ctx, agent.InteractionsKey)
	if !ok {
		p.t.note("The agent cannot receive this answer.")
		return
	}
	answer.Session, answer.ID = p.conv.Session, state.request.ID
	state.submitting = true
	var task kernel.Task
	kernel.Go(p.scope, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, backend.Answer(ctx, answer)
	}, func(_ struct{}, err error) {
		state.submitting = false
		if err != nil {
			p.t.note("The answer may not have reached the agent: " + err.Error())
			return
		}
		p.t.rows = slices.DeleteFunc(p.t.rows, func(r *row) bool { return r.interaction == state })
	})
}

func (p *Plugin) interactionView(c *ui.Context, pal native.Palette, state *interactionState) {
	q := state.request
	ui.Column(c).FillWidth().Gap(10).Radius(12).Padding(12, 14).
		Background(pal.DetailBg).Border(1, pal.Hairline).Children(func() {
		ui.Text(c, q.Title).FontSize(13).FontWeight(600).TextColor(pal.Text)
		if q.Message != "" {
			ui.Text(c, q.Message).FontSize(13).TextColor(pal.Text)
		}
		if q.Kind == agent.Input || q.Kind == agent.Editor {
			c.Key("answer")
			ui.Box(c).FillWidth().Children(func() {
				components.TextBox(c, &state.value, components.TextBoxOptions{Multiline: q.Kind == agent.Editor}).
					FillWidth().Label("Answer to " + q.Title).Placeholder(q.Placeholder).Disabled(state.submitting)
			})
		}
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			button := func(key, label string, answer func() agent.InteractionAnswer) {
				c.Key(key)
				components.Button(c, label, components.ButtonSecondary).Disabled(state.submitting).
					OnClick(func() { p.answer(state, answer()) })
			}
			switch q.Kind {
			case agent.Confirm:
				button("yes", "Yes", func() agent.InteractionAnswer { return agent.InteractionAnswer{Confirmed: true} })
				button("no", "No", func() agent.InteractionAnswer { return agent.InteractionAnswer{} })
			case agent.Select:
				for n, option := range q.Options {
					button(strconv.Itoa(n), option, func() agent.InteractionAnswer { return agent.InteractionAnswer{Value: option} })
				}
			case agent.Input, agent.Editor:
				button("submit", "Submit answer", func() agent.InteractionAnswer { return agent.InteractionAnswer{Value: state.value} })
			}
			button("cancel", "Cancel question", func() agent.InteractionAnswer { return agent.InteractionAnswer{Cancelled: true} })
		})
	})
}
