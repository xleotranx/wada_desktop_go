package main

import (
	"sort"
	"strings"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// composer is the field the user works with Codex in, over the
// transcript's foot.
func (a *app) composer(c *ui.Context, p palette) {
	ui.Column(c).FillWidth().Padding(8, 24, 16, 24).Children(func() {
		if a.conversation.commandError != "" {
			ui.Text(c, a.conversation.commandError).FontSize(12).TextColor(p.danger)
		}
		if a.conversation.target.isDraft() {
			ui.Row(c).MaxWidth(780).Margin(0, ui.Auto).FillWidth().Padding(0, 0, 4, 0).Children(func() { a.projectPicker(c, p) })
		}
		card := ui.Column(c).MaxWidth(780).Margin(0, ui.Auto).FillWidth().
			Radius(24).Padding(10, 14, 10, 14).Gap(4).
			Background(p.composerBg).Border(1, p.composerBorder).
			Shadow(0, 2, 10, 0, p.cardShadow)
		card.Children(func() {
			// The draft itself: Enter sends, Shift+Enter breaks the line.
			ui.TextAreaBase(c, &a.conversation.draft).Label("Compose").
				Placeholder("Work with Codex").FontSize(14).LineHeight(1.5).
				Padding(4, 8).Lines(1, 7).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == 0 {
						if a.send() {
							k.SetText("")
						}
						return true
					}
					return false
				})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(2).Padding(2, 4, 0, 0).Children(func() {
				attach := a.composeIconButton(c, p, "Attach", icPlus)
				attach.OnClick(a.openAttachDialog)
				a.fullAccessButton(c, p)
				ui.Spacer(c)
				a.modelButton(c, p)
				a.voiceButton(c, p)
			})
		})
	})
}

// dialog is the frame every dialog shares: the palette's card centered
// over a dimmed window. It builds on DialogBase rather than Modal: the
// theme's modal panel would sit behind the card as a second layer.
func dialog(c *ui.Context, p palette, open *bool, width float32, fn func()) {
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.scrim)
		panel.Width(width).Radius(14).Padding(16, 18).
			Background(p.bg).Border(1, p.hairline).
			Shadow(0, 10, 30, 0, p.shadow)
		ui.Column(c).Gap(10).Children(fn)
	})
}

// textField is the field the dialogs and the model picker share: the box
// carries the one border, and the input has no look of its own, so the
// pair reads as a single field. The border deepens while the field takes
// the keyboard, as the old theme look did.
func textField(c *ui.Context, p palette, value *string, placeholder string) {
	box := ui.Box(c).Shrink(0).FillWidth().Radius(8).Padding(8, 10).
		Background(p.composerBg).Border(1, p.composerBorder)
	if box.FocusWithin() {
		box.Border(1, p.composerBorderFocus)
	}
	box.Children(func() {
		ui.TextInputBase(c, value).FontSize(13).Placeholder(placeholder)
	})
}

// composeIconButton is a small icon button of the composer's bottom row.
func (a *app) composeIconButton(c *ui.Context, p palette, label string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c).Label(label).Size(30, 28).Radius(9).Center().Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(p.text)
	})
	return b
}

// attachModal quotes a workspace file into the draft, so the agent reads
// the text the user asked about along with the message.
func (a *app) attachModal(c *ui.Context, p palette) {
	st := a.layout.attach
	if st == nil || !st.open {
		return
	}
	dialog(c, p, &st.open, 400, func() {
		ui.Text(c, "Quote a file").FontSize(14).FontWeight(600)
		ui.Text(c, "Its path in this chat's folder, or an absolute one.").FontSize(12.5).TextColor(p.muted)
		textField(c, p, &st.draft, "src/main.go")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
			cancel.OnClick(func() { a.closeAttach() })
			cancel.Children(func() {
				ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
			})
			quote := ui.PrimaryButton(c, "Quote")
			quote.OnClick(a.attachFile)
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
	b := ui.ButtonBase(c).Label(label).Height(28).Radius(999).PaddingX(10).
		Tooltip("How shell actions run in " + dirBase(dir)).
		Menu(func(m *ui.Menu) {
			if m.Item("Asks first").Chosen() {
				a.setAsk(true)
			}
			m.Separator()
			if m.Item("Full access").Chosen() {
				a.setAsk(false)
			}
		})
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icShield).FontSize(15).TextColor(p.orange)
		ui.Text(c, label).MarginStart(6).FontSize(13).FontWeight(500).SingleLine()
	})
}

// modelButton names what the chat runs on: the model's fancy name and
// its reasoning effort, opening the picker over the catalog.
func (a *app) modelButton(c *ui.Context, p palette) {
	ref := a.sessionModelOf(string(a.conversation.target.session))
	name, variants := a.modelLabel(ref)
	b := ui.ButtonBase(c).Label("Model " + name).Height(28).Radius(999).PaddingX(8).
		MinWidth(80).Shrink(1).
		Cursor(ui.CursorPointer).Tooltip("Choose a model")
	b.OnClick(func() {
		a.layout.modelFilter = ""
		a.layout.pickerOpen = true
	})
	b.Children(func() {
		ui.Icon(c, icLoad).FontSize(13).TextColor(p.muted).Shrink(0)
		ui.Text(c, name).MarginStart(5).FontSize(13).TextColor(p.text).
			SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if ref.Variant != "" {
			ui.Text(c, ref.Variant).MarginStart(4).FontSize(13).TextColor(p.muted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
		ui.Icon(c, icChevUD).FontSize(12).MarginStart(5).TextColor(p.muted).Shrink(0)
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
		ui.Box(c).Width(320).MaxHeight(420).Radius(14).Padding(10).Gap(8).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 10, 30, 0, p.shadow).
			Children(func() {
				textField(c, p, &a.layout.modelFilter, "Search models")
				models := a.filteredModels()
				ui.Scroll(c).Grow(1).FillWidth().Gap(1).Children(func() {
					for _, m := range models {
						m := m // fresh per row, the closures keep their own model
						c.Key("model:" + m.ProviderID + "/" + m.ID)
						pick := ui.ButtonBase(c).Label("Pick "+m.Name).Height(32).Radius(8).
							Padding(0, 8).Justify(ui.Start).FillWidth()
						chosen := m.ProviderID == ref.ProviderID && m.ID == ref.ID
						if pick.Hovered() {
							pick.Background(p.hover)
						} else if chosen {
							pick.Background(p.selected)
						}
						pick.OnClick(func() { a.chooseModel(m, "") })
						pick.Children(func() {
							ui.Text(c, m.Name).FontSize(13.5).SingleLine()
							ui.Text(c, m.ProviderID).MarginStart(6).FontSize(11).
								TextColor(p.faint).Grow(1).SingleLine()
							if chosen {
								ui.Icon(c, icCheck).FontSize(14).TextColor(p.text)
							} else if m.Free {
								ui.Text(c, "Free").FontSize(11).FontWeight(500).
									TextColor(p.greenBadge).Shrink(0)
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
					manage := ui.ButtonBase(c).Label("Manage models…").Height(26).Radius(8).
						Padding(0, 8).FillWidth().Justify(ui.Start).Cursor(ui.CursorPointer).Shrink(0)
					if manage.Hovered() {
						manage.Background(p.hover)
					}
					manage.OnClick(func() {
						a.layout.pickerOpen = false
						a.openSettings(tabModels)
					})
					manage.Children(func() {
						ui.Text(c, "Manage models…").FontSize(12.5).TextColor(p.muted).SingleLine()
					})
				}
				if len(variants) > 0 {
					ui.Row(c).Shrink(0).Gap(4).Children(func() {
						for _, v := range variants {
							v := v // fresh per chip, so the closures keep their own effort
							chip := ui.ButtonBase(c).Label("Reasoning " + v).Height(22).Radius(999).
								PaddingX(9).Tooltip("Reasoning effort: " + v)
							if v == ref.Variant {
								chip.Background(p.selected)
							} else if chip.Hovered() {
								chip.Background(p.hover)
							}
							chip.OnClick(func() { a.pickVariant(v) })
							chip.Children(func() {
								t := ui.Text(c, v).FontSize(11.5).SingleLine()
								if v == ref.Variant {
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

// voiceButton stands at the composer's right: the dark voice-session
// circle while the draft is empty, the blue send button once the user has
// typed something, and a stop square while the agent's turn runs.
func (a *app) voiceButton(c *ui.Context, p palette) {
	bg, icon, label := p.voiceBg, icWaves, "Voice session"
	switch {
	case a.conversation.transcript.Busy() != "":
		bg, icon, label = p.accent, icStop, "Stop the agent"
	case strings.TrimSpace(a.conversation.draft) != "":
		bg, icon, label = p.accent, icSend, "Send message"
	}
	b := ui.ButtonBase(c).Label(label).Size(30, 30).Radius(999).Center().Shrink(0).
		Cursor(ui.CursorPointer).Tooltip(label).Background(bg)
	b.OnClick(func() {
		switch {
		case a.conversation.transcript.Busy() != "":
			a.stopTurn()
		case strings.TrimSpace(a.conversation.draft) != "":
			a.send()
		}
	})
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(p.bg)
	})
}
