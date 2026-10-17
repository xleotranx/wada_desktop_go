package main

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// composer is the field the user works with ChatGPT in, over the
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
				Placeholder("Work with ChatGPT").FontSize(14).LineHeight(1.5).
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
				a.folderChip(c, p)
				ui.Spacer(c)
				a.modelButton(c, p)
				a.voiceButton(c, p)
			})
		})
	})
}

// folderChip shows where the chat works; clicking it starts a new chat
// in another folder.
func (a *app) folderChip(c *ui.Context, p palette) {
	dir := a.chatDir()
	if dir == "" {
		dir = workdir()
	}
	b := ui.ButtonBase(c).Label("Workspace folder, " + dirBase(dir)).Height(28).Radius(999).
		PaddingX(10).Cursor(ui.CursorPointer).Tooltip("This chat works in " + dir + ". Start a new chat somewhere else?")
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(a.openFolderDialog)
	b.Children(func() {
		ui.Icon(c, icFolder).FontSize(13).TextColor(p.muted)
		ui.Text(c, dirBase(dir)).MarginStart(5).FontSize(13).TextColor(p.text).SingleLine()
	})
}

// folderModal is the dialog for starting a chat in a chosen directory.
func (a *app) folderModal(c *ui.Context, p palette) {
	ui.Modal(c, &a.dirOpen, func() {
		ui.Box(c).Width(360).Radius(14).Padding(16, 18).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.2)).
			Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, "Start a chat in a folder").FontSize(14).FontWeight(600)
					ui.Text(c, "The agent reads and changes files in this folder.").FontSize(12.5).TextColor(p.muted)
					ui.Box(c).FillWidth().Radius(8).Padding(4, 8).
						Background(p.composerBg).Border(1, p.composerBorder).Children(func() {
						ui.TextInput(c, &a.dirDraft).FontSize(13)
					})
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
			})
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
	ui.Modal(c, &a.attachOpen, func() {
		ui.Box(c).Width(400).Radius(14).Padding(16, 18).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.2)).
			Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, "Quote a file").FontSize(14).FontWeight(600)
					ui.Text(c, "Its path in this chat's folder, or an absolute one.").FontSize(12.5).TextColor(p.muted)
					ui.Box(c).FillWidth().Radius(8).Padding(4, 8).
						Background(p.composerBg).Border(1, p.composerBorder).Children(func() {
						ui.TextInput(c, &a.attachDraft).FontSize(13).Placeholder("src/main.go")
					})
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
			})
	})
}

// fullAccessButton shows how shell actions run in the chat's folder and
// moves the policy between ask-first and open through the workspace
// config the server watches.
func (a *app) fullAccessButton(c *ui.Context, p palette) {
	dir := a.chatDir()
	if dir == "" {
		dir = workdir()
	}
	label := "Full access"
	if level, err := permissionLevel(dir); err != nil || level == "ask" {
		// A config the app cannot read leans to the safe name.
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
func (a *app) modelPicker(c *ui.Context, p palette, anchor ui.Element, ref agentModelRef, variants []string) {
	ui.Popover(c, anchor, &a.pickerOpen, func() {
		ui.Box(c).Width(320).Height(420).Radius(14).Padding(10).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.2)).
			Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					ui.Box(c).FillWidth().Radius(8).Padding(3, 8).
						Background(p.composerBg).Border(1, p.composerBorder).Children(func() {
						ui.TextInput(c, &a.modelFilter).FontSize(13).Placeholder("Search models")
					})
					ui.Scroll(c).Grow(1).Gap(1).Children(func() {
						for _, m := range a.filteredModels() {
							m := m // fresh per row, the closures keep their own model
							c.Key("model:" + m.providerID + "/" + m.id)
							pick := ui.ButtonBase(c).Label("Pick "+m.name).Height(30).Radius(8).
								Padding(0, 8).Justify(ui.Start).FillWidth()
							chosen := m.providerID == ref.providerID && m.id == ref.id
							if pick.Hovered() {
								pick.Background(p.hover)
							} else if chosen {
								pick.Background(p.selected)
							}
							pick.OnClick(func() { a.chooseModel(m, "") })
							pick.Children(func() {
								ui.Text(c, m.name).FontSize(13).SingleLine()
								ui.Text(c, m.providerID).MarginStart(6).FontSize(11).
									TextColor(p.faint).Grow(1).SingleLine()
								if chosen {
									ui.Icon(c, icCheck).FontSize(13).TextColor(p.text)
								} else if m.free {
									ui.Text(c, "Free").FontSize(11).FontWeight(500).
										TextColor(p.greenText)
								}
							})
						}
						if len(a.filteredModels()) == 0 {
							ui.Text(c, "No models in the catalog match.").
								FontSize(12).TextColor(p.muted)
						}
					})
					if len(variants) > 0 {
						ui.Row(c).Gap(4).Children(func() {
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
