package chat

import (
	"context"
	"slices"
	"strconv"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
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
	if state.request.Session != "" {
		answer.Session = state.request.Session
	}
	state.submitting = true
	parent := p.conv.Session
	var task kernel.Task
	kernel.Go(p.ctx, &task, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, backend.Answer(ctx, answer)
	}, func(_ struct{}, err error) {
		state.submitting = false
		if err != nil {
			p.t.note("The answer may not have reached the agent: " + err.Error())
			return
		}
		kernel.Emit(p.ctx, chat.RequestAnswered, chat.Answered{Session: answer.Session, Interaction: answer.ID})
		if p.conv.Session == parent {
			p.t.rows = slices.DeleteFunc(p.t.rows, func(r *row) bool { return r.interaction == state })
		} else if kept := p.kept.find(parent); kept != nil {
			kept.t.rows = slices.DeleteFunc(kept.t.rows, func(r *row) bool { return r.interaction == state })
		}
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
		if len(q.Questions) > 0 {
			p.questionsView(c, pal, state)
			return
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

func (p *Plugin) questionsView(c *ui.Context, pal native.Palette, state *interactionState) {
	for _, question := range state.request.Questions {
		c.Key(question.Key)
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, question.Title).FontSize(13).TextColor(pal.Text)
			if question.Description != "" {
				ui.Text(c, question.Description).FontSize(12).TextColor(pal.Text)
			}
			for n, option := range question.Options {
				selected := slices.Contains(state.answers[question.Key], option.Value)
				label := option.Label
				if selected {
					label = "✓ " + label
				}
				c.Key(strconv.Itoa(n))
				components.Button(c, label, components.ButtonSecondary).Disabled(state.submitting).OnClick(func() {
					if state.answers == nil {
						state.answers = map[string][]string{}
					}
					if question.Multiple {
						values := state.answers[question.Key]
						if selected {
							values = slices.DeleteFunc(values, func(v string) bool { return v == option.Value })
						} else {
							values = append(values, option.Value)
						}
						state.answers[question.Key] = values
					} else {
						state.answers[question.Key] = []string{option.Value}
						*state.custom[question.Key] = ""
					}
				})
				if option.Description != "" {
					ui.Text(c, option.Description).FontSize(12).TextColor(pal.Text)
				}
			}
			if question.Custom {
				// Bind to persistent per-field storage rather than a frame-local value.
				field := state.custom[question.Key]
				c.Key("custom")
				components.TextBox(c, field, components.TextBoxOptions{}).FillWidth().Label("Answer to " + question.Title).Disabled(state.submitting)
			}
		})
	}
	c.Key("submit")
	components.Button(c, "Submit answer", components.ButtonSecondary).Disabled(state.submitting).OnClick(func() {
		answers := map[string][]string{}
		for _, q := range state.request.Questions {
			values := slices.Clone(state.answers[q.Key])
			if value := state.custom[q.Key]; value != nil && *value != "" {
				if q.Multiple {
					values = append(values, *value)
				} else {
					values = []string{*value}
				}
			}
			if values == nil {
				values = []string{}
			}
			answers[q.Key] = values
		}
		p.answer(state, agent.InteractionAnswer{Answers: answers})
	})
	c.Key("cancel")
	components.Button(c, "Cancel question", components.ButtonSecondary).Disabled(state.submitting).OnClick(func() { p.answer(state, agent.InteractionAnswer{Cancelled: true}) })
}
