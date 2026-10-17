package main

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// composer is the field the user works with Codex in, over the
// transcript's foot.
func (a *app) composer(c *ui.Context, p palette) {
	ui.Column(c).FillWidth().Padding(8, 24, 16, 24).Children(func() {
		card := ui.Column(c).MaxWidth(780).Margin(0, ui.Auto).FillWidth().
			Radius(24).Padding(10, 14, 10, 14).Gap(4).
			Background(p.composerBg).Border(1, p.composerBorder).
			Shadow(0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.05))
		card.Children(func() {
			// The draft itself: Enter sends, Shift+Enter breaks the line.
			ui.TextAreaBase(c, &a.draft).Label("Compose").
				Placeholder("Work with Codex").FontSize(14).LineHeight(1.5).
				Padding(4, 8).Lines(1, 7).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == 0 {
						a.send() // reads the draft as it is now
						k.SetText("")
						return true
					}
					return false
				})

			ui.Row(c).AlignItems(ui.Center).Gap(2).Padding(2, 0, 0, 0).Children(func() {
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

// folderModal is the dialog for starting a chat in a chosen directory.
func (a *app) folderModal(c *ui.Context, p palette) {
	dialog(c, p, &a.dirOpen, 360, func() {
		ui.Text(c, "Start a chat in a folder").FontSize(14).FontWeight(600)
		ui.Text(c, "The agent reads and changes files in this folder.").FontSize(12.5).TextColor(p.muted)
		textField(c, p, &a.dirDraft, "")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
			cancel.OnClick(func() { a.dirOpen = false })
			cancel.Children(func() {
				ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
			})
			start := ui.PrimaryButton(c, "Start chat here")
			start.OnClick(func() { a.startChatIn(a.dirDraft) })
		})
		if a.dirNote != "" {
			ui.Text(c, a.dirNote).FontSize(12).TextColor(p.orange).SingleLine()
		}
	})
}

// dialog is the frame every dialog shares: the palette's card centered
// over a dimmed window. It builds on DialogBase rather than Modal: the
// theme's modal panel would sit behind the card as a second layer.
func dialog(c *ui.Context, p palette, open *bool, width float32, fn func()) {
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Width(width).Radius(14).Padding(16, 18).
			Background(p.bg).Border(1, p.hairline).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.2))
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
	dialog(c, p, &a.attachOpen, 400, func() {
		ui.Text(c, "Quote a file").FontSize(14).FontWeight(600)
		ui.Text(c, "Its path in this chat's folder, or an absolute one.").FontSize(12.5).TextColor(p.muted)
		textField(c, p, &a.attachDraft, "src/main.go")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
			cancel.OnClick(func() { a.attachOpen = false })
			cancel.Children(func() {
				ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
			})
			quote := ui.PrimaryButton(c, "Quote")
			quote.OnClick(a.attachFile)
		})
		if a.attachNote != "" {
			ui.Text(c, a.attachNote).FontSize(12).TextColor(p.danger).SingleLine()
		}
	})
}

// fullAccessButton shows how shell actions run in the chat's folder and
// moves the policy between ask-first and open through the workspace
// config the server watches.
func (a *app) fullAccessButton(c *ui.Context, p palette) {
	dir := a.chatDir()
	if dir == "" {
		dir = a.home
	}
	a.watchWorkspacePolicy(dir)
	label := "Full access"
	if a.policyLevel != "full" {
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
	ref := a.sessionModelOf(a.selectedSession)
	name, variants := a.modelLabel(ref)
	b := ui.ButtonBase(c).Label("Model " + name).Height(28).Radius(999).PaddingX(8).
		Cursor(ui.CursorPointer).Tooltip("Choose a model")
	b.OnClick(func() {
		a.modelFilter = ""
		a.pickerOpen = true
	})
	b.Children(func() {
		ui.Icon(c, icLoad).FontSize(13).TextColor(p.muted)
		ui.Text(c, name).MarginStart(5).FontSize(13).TextColor(p.text).SingleLine()
		if ref.variant != "" {
			ui.Text(c, ref.variant).MarginStart(4).FontSize(13).TextColor(p.muted).SingleLine()
		}
		ui.Icon(c, icChevUD).FontSize(12).MarginStart(5).TextColor(p.muted)
	})
	a.modelPicker(c, p, b, ref, variants)
}

// modelPicker is the panel under the model button: a filter, one row per
// model of the catalog, and the reasoning efforts the chat's model has.
// The panel is the flex column itself: a wrapper in between would hold
// the content height of the whole catalog and spill past the panel
// instead of handing the spare room to the list that scrolls.
func (a *app) modelPicker(c *ui.Context, p palette, anchor ui.Element, ref agentModelRef, variants []string) {
	ui.Popover(c, anchor, &a.pickerOpen, func() {
		ui.Box(c).Width(320).MaxHeight(420).Radius(14).Padding(10).Gap(8).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.2)).
			Children(func() {
				textField(c, p, &a.modelFilter, "Search models")
				models := a.filteredModels()
				ui.Scroll(c).Grow(1).FillWidth().Gap(1).Children(func() {
					for _, m := range models {
						m := m // fresh per row, the closures keep their own model
						c.Key("model:" + m.providerID + "/" + m.id)
						pick := ui.ButtonBase(c).Label("Pick "+m.name).Height(32).Radius(8).
							Padding(0, 8).Justify(ui.Start).FillWidth()
						chosen := m.providerID == ref.providerID && m.id == ref.id
						if pick.Hovered() {
							pick.Background(p.hover)
						} else if chosen {
							pick.Background(p.selected)
						}
						pick.OnClick(func() { a.chooseModel(m, "") })
						pick.Children(func() {
							ui.Text(c, m.name).FontSize(13.5).SingleLine()
							ui.Text(c, m.providerID).MarginStart(6).FontSize(11).
								TextColor(p.faint).Grow(1).SingleLine()
							if chosen {
								ui.Icon(c, icCheck).FontSize(14).TextColor(p.text)
							} else if m.free {
								ui.Text(c, "Free").FontSize(11).FontWeight(500).
									TextColor(p.greenBadge).Shrink(0)
							}
						})
					}
					if len(models) == 0 {
						ui.Text(c, "No models in the catalog match.").
							FontSize(12).TextColor(p.muted)
					}
				})
				if len(variants) > 0 {
					ui.Row(c).Shrink(0).Gap(4).Children(func() {
						for _, v := range variants {
							v := v // fresh per chip, so the closures keep their own effort
							chip := ui.ButtonBase(c).Label("Reasoning " + v).Height(22).Radius(999).
								PaddingX(9).Tooltip("Reasoning effort: " + v)
							if v == ref.variant {
								chip.Background(p.selected)
							} else if chip.Hovered() {
								chip.Background(p.hover)
							}
							chip.OnClick(func() { a.pickVariant(v) })
							chip.Children(func() {
								t := ui.Text(c, v).FontSize(11.5).SingleLine()
								if v == ref.variant {
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
func (a *app) filteredModels() []agentModel {
	out := []agentModel{}
	for _, m := range a.models {
		if strings.Contains(strings.ToLower(m.name), strings.ToLower(a.modelFilter)) ||
			strings.Contains(strings.ToLower(m.providerID), strings.ToLower(a.modelFilter)) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].free != out[j].free {
			return out[i].free
		}
		return out[i].name < out[j].name
	})
	return out
}

// voiceButton stands at the composer's right: the dark voice-session
// circle while the draft is empty, the blue send button once the user has
// typed something, and a stop square while the agent's turn runs.
func (a *app) voiceButton(c *ui.Context, p palette) {
	bg, icon, label := p.voiceBg, icWaves, "Voice session"
	switch {
	case a.busySession != "":
		bg, icon, label = ui.Hex("#3572e3"), icStop, "Stop the agent"
	case strings.TrimSpace(a.draft) != "":
		bg, icon, label = ui.Hex("#3572e3"), icSend, "Send message"
	}
	b := ui.ButtonBase(c).Label(label).Size(30, 30).Radius(999).Center().
		Cursor(ui.CursorPointer).Tooltip(label).Background(bg)
	b.OnClick(func() {
		switch {
		case a.busySession != "":
			a.stopTurn()
		case strings.TrimSpace(a.draft) != "":
			a.send()
		}
	})
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(p.bg)
	})
}
