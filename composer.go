package main

import (
	"sort"
	"strings"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// composer is the field the user works with Codex in, over the
// transcript's foot. Onevra's chat composer owns the card, the editor and
// the send/stop affordance; the app supplies the chips that decide how the
// next turn runs.
func (a *app) composer(c *ui.Context, p palette) {
	ui.Column(c).FillWidth().Padding(8, 24, 16, 24).Children(func() {
		if a.conversation.commandError != "" {
			ui.Text(c, a.conversation.commandError).FontSize(12).TextColor(p.danger)
		}
		if a.conversation.target.isDraft() {
			ui.Row(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().Padding(0, 0, 4, 0).Children(func() { a.projectPicker(c, p) })
		}
		ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().Children(func() {
			busy := a.conversation.transcript.Busy() != ""
			result := onechat.Composer(c, &a.conversation.draft, onechat.ComposerOptions{
				Label:       "Compose",
				Placeholder: "Work with Codex",
				SendLabel:   "Send message",
				StopLabel:   "Stop the agent",
				Running:     busy,
				Accessory: func(c *ui.Context) {
					attach := onechat.IconAction(c, "Attach", iconRef(icPlus))
					attach.OnClick(a.openAttachDialog)
					a.fullAccessButton(c, p)
					a.agentPill(c, p)
				},
				Trailing: func(c *ui.Context) { a.modelButton(c, p) },
			})
			if result.Send {
				a.send()
			}
			if result.Stop {
				a.stopTurn()
			}
		})
	})
}

// dialog is the frame every dialog shares: Onevra's modal panel over a dimmed
// window, sized to the content it holds.
func dialog(c *ui.Context, p palette, open *bool, width float32, fn func()) {
	components.Dialog(c, open, components.DialogOptions{Label: "Dialog"}, func(panel ui.Element) {
		panel.Width(width).MaxWidth(width)
		ui.Column(c).Gap(12).Children(fn)
	})
}

// textField is the field the dialogs and the model picker share: Onevra's
// bordered single-line field, with the theme's focus ring.
func textField(c *ui.Context, p palette, value *string, placeholder string) {
	components.TextBox(c, value, components.TextBoxOptions{}).
		FillWidth().Placeholder(placeholder).FontSize(13)
}

// attachModal quotes a workspace file into the draft, so the agent reads
// the text the user asked about along with the message.
func (a *app) attachModal(c *ui.Context, p palette) {
	st := a.layout.attach
	if st == nil || !st.open {
		return
	}
	dialog(c, p, &st.open, 400, func() {
		ui.Text(c, "Quote a file").FontSize(15).FontWeight(600)
		ui.Text(c, "Its path in this chat's folder, or an absolute one.").FontSize(12.5).TextColor(p.muted)
		textField(c, p, &st.draft, "src/main.go")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if components.Button(c, "Cancel", components.ButtonGhost).Clicked() {
				a.closeAttach()
			}
			if components.Button(c, "Quote", components.ButtonPrimary).Clicked() {
				a.attachFile()
			}
		})
		if st.note != "" {
			ui.Text(c, st.note).FontSize(12).TextColor(p.danger).SingleLine()
		}
	})
}

// fullAccessButton shows how shell actions run in the chat's folder and
// moves the policy between ask-first and open through the workspace
// config the server watches.
func (a *app) fullAccessButton(c *ui.Context, p palette) {
	dir := a.chatDir()
	label := "Full access"
	if a.workspace.policyLevel != "full" {
		// A policy not yet read, or unreadable, leans to the safe name.
		label = "Asks first"
	}
	chip := onechat.Chip(c, label, onechat.ChipOptions{Icon: iconRef(icShield), Caret: true})
	chip.Tooltip("How shell actions run in " + dirBase(dir))
	chip.Menu(func(m *ui.Menu) {
		if m.Item("Asks first").Chosen() {
			a.setAsk(true)
		}
		m.Separator()
		if m.Item("Full access").Chosen() {
			a.setAsk(false)
		}
	})
}

// modelButton names what the chat runs on: the model's fancy name and
// its reasoning effort, opening the picker over the catalog. The visible
// label is the bare name; the accessible one says what the chip chooses.
func (a *app) modelButton(c *ui.Context, p palette) {
	ref := a.sessionModelOf(string(a.conversation.target.session))
	name, variants := a.modelLabel(ref)
	b := ui.ButtonBase(c).Label("Model "+name).Height(26).Radius(6).Padding(0, 7).
		AlignItems(ui.Center).Gap(6).Shrink(1).MinWidth(0).
		Cursor(ui.CursorPointer).Tooltip("Choose a model")
	if b.Hovered() || b.Focused() {
		b.Background(p.hover)
	}
	b.OnClick(func() {
		a.layout.modelFilter = ""
		a.layout.pickerOpen = true
	})
	b.Children(func() {
		ui.Icon(c, icLoad).FontSize(12).TextColor(p.muted).Shrink(0)
		ui.Text(c, name).FontSize(12.5).TextColor(p.text).SingleLine().
			Shrink(1).MinWidth(0).Ellipsis("…")
		if ref.Variant != "" {
			ui.Text(c, ref.Variant).FontSize(12).TextColor(p.muted).Shrink(0)
		}
		ui.Icon(c, icChevDown).FontSize(10.5).TextColor(p.faint.Alpha(.9)).Shrink(0)
	})
	a.modelPicker(c, p, b, ref, variants)
}

// modelPicker is the panel under the model button: a filter, one row per
// model of the catalog, and the reasoning efforts the chat's model has.
// The panel is the flex column itself: a wrapper in between would hold
// the content height of the whole catalog and spill past the panel
// instead of handing the spare room to the list that scrolls.
func (a *app) modelPicker(c *ui.Context, p palette, anchor ui.Element, ref opencode.ModelRef, variants []string) {
	ui.Popover(c, anchor, &a.layout.pickerOpen, func() {
		ui.Box(c).Width(320).MaxHeight(420).Radius(c.Theme().Radius*1.75).Padding(10).Gap(8).
			Background(c.Theme().Surface).Border(1, p.hairline).Shadow(0, 10, 30, 0, p.shadow).
			Children(func() {
				textField(c, p, &a.layout.modelFilter, "Search models")
				models := a.filteredModels()
				ui.Scroll(c).Grow(1).FillWidth().Gap(1).Children(func() {
					for _, m := range models {
						m := m // fresh per row, the closures keep their own model
						c.Key("model:" + m.ProviderID + "/" + m.ID)
						chosen := m.ProviderID == ref.ProviderID && m.ID == ref.ID
						row := ui.ButtonBase(c).Label("Pick "+m.Name).Height(32).Radius(8).
							Padding(0, 8).Justify(ui.Start).FillWidth()
						if row.Hovered() {
							row.Background(p.hover)
						} else if chosen {
							row.Background(p.selected)
						}
						row.OnClick(func() { a.chooseModel(m, "") })
						row.Children(func() {
							ui.Text(c, m.Name).FontSize(13.5).SingleLine()
							ui.Text(c, m.ProviderID).MarginStart(6).FontSize(11).
								TextColor(p.faint).Grow(1).SingleLine()
							if chosen {
								ui.Icon(c, icCheck).FontSize(14).TextColor(c.Theme().Accent)
							} else if m.Free {
								components.Badge(c, "Free", components.BadgeOptions{Tone: components.BadgeSuccess})
							}
						})
					}
					if len(models) == 0 {
						ui.Text(c, a.noModelsNote()).FontSize(12).TextColor(p.muted)
					}
				})
				if len(a.scope().models) > 0 {
					// The picker is where a missing model becomes a
					// question; its answer lives in the settings.
					ui.Divider(c)
					if components.Button(c, "Manage models…", components.ButtonGhost).Clicked() {
						a.layout.pickerOpen = false
						a.openSettings(tabModels)
					}
				}
				if len(variants) > 0 {
					ui.Row(c).Shrink(0).Gap(4).Children(func() {
						for _, v := range variants {
							v := v // fresh per chip, so the closures keep their own effort
							on := v == ref.Variant
							chip := ui.ButtonBase(c).Label("Reasoning " + v).Height(22).Radius(999).
								PaddingX(9).Tooltip("Reasoning effort: " + v)
							if on {
								chip.Background(p.selected)
							} else if chip.Hovered() {
								chip.Background(p.hover)
							}
							chip.OnClick(func() { a.pickVariant(v) })
							chip.Children(func() {
								t := ui.Text(c, v).FontSize(11.5).SingleLine()
								if on {
									t.FontWeight(500)
								}
							})
						}
					})
				}
			})
	})
}

// filteredModels is the catalog narrowed by the pick filter, with the
// free models first and, within each group, names alphabetically.
func (a *app) filteredModels() []opencode.Model { return a.offerModels(a.layout.modelFilter) }

// enabledModels is everything the picker offers with no filter at all:
// the model a new conversation falls back to comes from here.
func (a *app) enabledModels() []opencode.Model { return a.offerModels("") }

// offerModels is the catalog in picker order -- free models first, names
// alphabetically inside each group -- minus what the settings turned off
// and whatever the query does not match.
func (a *app) offerModels(query string) []opencode.Model {
	query = strings.ToLower(strings.TrimSpace(query))
	out := []opencode.Model{}
	for _, m := range a.scope().models {
		if a.modelDisabled(m) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.Name), query) &&
			!strings.Contains(strings.ToLower(m.ProviderID), query) &&
			!strings.Contains(strings.ToLower(m.ID), query) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return modelLess(out[i], out[j]) })
	return out
}

// noModelsNote words why the picker lists nothing.
func (a *app) noModelsNote() string {
	switch {
	case len(a.scope().models) == 0 && !a.scope().modelsLoaded:
		return "The catalog is still loading."
	case len(a.scope().models) == 0:
		return "The server reported no models."
	case len(a.enabledModels()) == 0:
		return "Every model is off. Turn some on under Manage models…"
	default:
		return "No models in the catalog match."
	}
}
