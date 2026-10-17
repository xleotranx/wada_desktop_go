package main

import (
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
				a.composeIconButton(c, p, "Attach", icPlus)
				a.fullAccessButton(c, p)
				a.folderChip(c, p)
				ui.Spacer(c)
				a.modelButton(c, p)
				a.composeIconButton(c, p, "Dictate", icMic)
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
func (a *app) composeIconButton(c *ui.Context, p palette, label string, icon *ui.SVG) {
	b := ui.ButtonBase(c).Label(label).Size(30, 28).Radius(9).Center().Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(p.text)
	})
}

// fullAccessButton shows what the session's permission level is.
func (a *app) fullAccessButton(c *ui.Context, p palette) {
	b := ui.ButtonBase(c).Label("Full access").Height(28).Radius(999).PaddingX(10).Tooltip("Session runs with full access")
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icShield).FontSize(15).TextColor(p.orange)
		ui.Text(c, "Full access").MarginStart(6).FontSize(13).FontWeight(500).TextColor(p.orange).SingleLine()
	})
}

// modelButton chooses which model the chat runs on.
func (a *app) modelButton(c *ui.Context, p palette) {
	b := ui.ButtonBase(c).Label("Model").Height(28).Radius(999).PaddingX(8).Tooltip("Choose a model").
		Menu(func(m *ui.Menu) {
			m.Item("GPT-6.1 Sol (Medium)").Checked(true)
			m.Item("GPT-6.1 Sol (High)")
			m.Item("GPT-6.1 Mini")
			m.Separator()
			m.Item("Manage models…")
		})
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icLoad).FontSize(13).TextColor(p.muted)
		ui.Text(c, "GPT-6.1 Sol").MarginStart(5).FontSize(13).TextColor(p.text).SingleLine()
		ui.Text(c, "Medium").MarginStart(4).FontSize(13).TextColor(p.muted).SingleLine()
		ui.Icon(c, icChevUD).FontSize(12).MarginStart(5).TextColor(p.muted)
	})
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
