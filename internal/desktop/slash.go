package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// slashCommand is one local composer command: it runs an app action
// instead of sending a prompt.
type slashCommand struct {
	name, title, description string
	run                      func(*app)
}

var slashCommands = []slashCommand{
	{"/activity", "Activity view", "Show or fold the Activity view", func(a *app) { a.toggleActivity() }},
	{"/clear", "Clear", "Empty the composer", func(a *app) {}},
	{"/full", "Full view", "Show only the conversation, or leave it", func(a *app) { a.toggleFullView() }},
	{"/help", "Slash commands", "Show this list", func(a *app) { a.layout.slashHelp = true }},
	{"/model", "Model", "Choose what this chat runs on", func(a *app) { a.openModelPicker() }},
	{"/new", "New chat", "Start a new chat in this folder", func(a *app) { a.startNewChat() }},
	{"/search", "Search chats", "Find a conversation in the sidebar", func(a *app) { a.openChatSearch() }},
	{"/settings", "Settings", "Open servers, models and appearance", func(a *app) { a.openSettings("") }},
	{"/shortcuts", "Keyboard", "Open the keyboard reference", func(a *app) { a.openSettings(tabShortcuts) }},
	{"/sidebar", "Sidebar", "Show or hide the conversation sidebar", func(a *app) { a.toggleSidebar() }},
	{"/stop", "Stop", "Stop the running turn, if one is going", func(a *app) {
		if a.conversation.transcript.Busy() == "" {
			a.notify(ui.Toast{Title: "No turn is running"})
			return
		}
		a.stopTurn()
	}},
}

// slashMatch is the command a composer text names exactly, if any. Words
// after the command go to the agent as an ordinary prompt.
func (a *app) slashMatch(text string) (slashCommand, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") {
		return slashCommand{}, false
	}
	for _, command := range a.slashCommands() {
		if strings.EqualFold(text, command.name) {
			return command, true
		}
	}
	return slashCommand{}, false
}

// runSlash runs an exact slash command, clearing the draft first. It
// reports whether the text was a command and must not be sent.
func (a *app) runSlash(text string) bool {
	command, ok := a.slashMatch(text)
	if !ok {
		return false
	}
	a.executeSlash(command)
	return true
}

// slashPrefix lists commands matching a bare slash prefix. A word after
// the command means the text is a prompt, not a command.
func (a *app) slashPrefix(text string) []slashCommand {
	draft := strings.TrimSpace(text)
	if !strings.HasPrefix(draft, "/") || strings.ContainsAny(draft, " \t\n") {
		return nil
	}
	var matches []slashCommand
	for _, command := range a.slashCommands() {
		if strings.HasPrefix(command.name, strings.ToLower(draft)) {
			matches = append(matches, command)
		}
	}
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}

// slashMatches suggests commands for the composer's current words.
func (a *app) slashMatches() []slashCommand {
	return a.slashPrefix(a.conversation.draft)
}

// slashChoice is the command Enter runs for a prefix that is not exact: the
// selected match, or the first one.
func (a *app) slashChoice(text string) (slashCommand, bool) {
	matches := a.slashPrefix(text)
	if len(matches) == 0 {
		return slashCommand{}, false
	}
	index := min(max(a.layout.slashSelected, 0), len(matches)-1)
	return matches[index], true
}

// executeSlash empties the composer, saves that, then runs the action.
func (a *app) executeSlash(command slashCommand) {
	key := a.draftKey()
	a.conversation.draft = ""
	a.conversation.commandError = ""
	a.layout.slashSelected, a.layout.slashSeen = 0, ""
	a.forgetDraft(key)
	a.closeAttach()
	a.saveState()
	command.run(a)
}

// slashMenu is the short suggestion list under the composer while a slash
// command is being typed.
func (a *app) slashMenu(c *ui.Context, p palette) {
	// The slot stays in the tree even when empty: adding a sibling before
	// the composer would move its focus identity and drop the keyboard.
	ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().
		Children(func() {
			if a.layout.slashHelp || a.modulesModal() {
				return
			}
			matches := a.slashMatches()
			if len(matches) == 0 {
				return
			}
			if a.layout.slashSeen != a.conversation.draft {
				a.layout.slashSeen = a.conversation.draft
				a.layout.slashSelected = 0
			}
			a.layout.slashSelected = min(max(a.layout.slashSelected, 0), len(matches)-1)
			ui.Column(c).Padding(0, 0, 5, 0).Children(func() {
				ui.Column(c).FillWidth().Radius(10).Padding(4).Gap(1).
					Background(p.Bg).Border(1, p.Hairline).Children(func() {
					for i, command := range matches {
						a.slashRow(c, p, command, i == a.layout.slashSelected)
					}
				})
			})
		})
}

func (a *app) slashRow(c *ui.Context, p palette, command slashCommand, selected bool) {
	c.Key("slash:" + command.name)
	b := ui.ButtonBase(c).Label("Run "+command.name).Height(29).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8)
	if selected {
		b.Background(p.Selected)
	}
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.executeSlash(command) })
	b.Children(func() {
		ui.Text(c, command.name).Font("monospace").FontSize(12).TextColor(p.Orange).
			Shrink(0)
		ui.Text(c, command.title).FontSize(12.5).TextColor(p.Text).SingleLine()
		ui.Text(c, command.description).FontSize(11.5).TextColor(p.Faint).
			Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
	})
}

// slashHelpDialog lists every local command for /help.
func (a *app) slashHelpDialog(c *ui.Context, p palette) {
	if !a.layout.slashHelp {
		return
	}
	dialog(c, p, &a.layout.slashHelp, 540, "Slash commands", func() {
		ui.Text(c, "Slash commands").FontSize(15).FontWeight(600).TextColor(p.Text)
		ui.Text(c, "Type one in the composer and send it. A command with words after it goes to the agent as a normal prompt.").
			FontSize(12).LineHeight(1.45).TextColor(p.Muted)
		ui.Scroll(c).FillWidth().MaxHeight(360).Gap(1).Children(func() {
			for _, command := range a.slashCommands() {
				ui.Row(c).FillWidth().Padding(4, 8).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, command.name).Font("monospace").FontSize(12).
						TextColor(p.Orange).Width(94).Shrink(0)
					ui.Text(c, command.title).FontSize(12.5).TextColor(p.Text).
						Width(118).Shrink(0).SingleLine()
					ui.Text(c, command.description).FontSize(11.5).TextColor(p.Faint).
						Grow(1).MinWidth(0).SingleLine()
				})
			}
		})
		ui.Text(c, "Esc closes").FontSize(11).TextColor(p.Faint)
	})
}

func (a *app) slashCommands() []slashCommand { return a.registeredSlashes }
