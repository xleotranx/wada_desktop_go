// Package palette is the command palette (⌘K): one search box over every
// registered command and over the conversations started in this app.
// Commands come straight from kernel.Commands, so a command another plugin
// contributes is in the palette without that plugin knowing about it.
package palette

import (
	"cmp"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/command"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

const (
	openID      = "palette.open"
	groupChats  = "Conversations"
	groupCmds   = "Commands"
	sessionID   = "session:" // prefixes the item ID of a conversation
	recentShown = 5          // conversations listed before anything is typed
)

// Plugin is the command palette. The zero value is ready to install.
//
// Commands need nothing else. Conversations come from the workspace record
// when it is active, so the palette keeps working without it.
type Plugin struct {
	ctx *kernel.Context

	open  bool
	state command.State
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "palette", Title: "Command palette",
		Description: "Search conversations and run any command with ⌘K.",
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.open = false
	for _, err := range []error{
		kernel.Contribute(c, shell.Overlays, "palette.dialog", 10, shell.View{Render: p.view}),
		kernel.Contribute(c, kernel.Commands, openID, 0, kernel.Command{
			Title: "Command Palette…", Menu: "View", Keys: []string{"cmd+k"}, Run: p.toggle}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// toggle opens the palette on an empty search, or closes it. The dialog also
// closes itself on Escape or a press outside, so the query is dropped when
// the palette opens, not when it closes.
func (p *Plugin) toggle() {
	if p.open {
		p.open = false
		return
	}
	p.open = true
	p.state.Query, p.state.ActiveID = "", ""
}

// ---- Activation

// activate closes the palette and acts on the item once the handler that
// activated it has returned, so a command that opens a dialog or asks for the
// focus doesn't do it from inside the palette's own event handling.
func (p *Plugin) activate(id string) {
	p.open = false
	p.ctx.Post(func() { p.run(id) })
}

func (p *Plugin) run(id string) {
	if sid, ok := strings.CutPrefix(id, sessionID); ok {
		ctl, ok := kernel.Use(p.ctx, chat.ControllerKey)
		ws, known := kernel.Use(p.ctx, workspace.ServiceKey)
		if !ok || !known {
			return
		}
		if conv, ok := ws.Lookup(agent.SessionID(sid)); ok {
			ctl.Open(conv.Session())
		}
		return
	}
	for _, e := range kernel.Items(p.ctx, kernel.Commands) {
		if e.ID != id {
			continue
		}
		kernel.Invoke(p.ctx, e, func(cmd kernel.Command) {
			if cmd.Run != nil && (cmd.Enabled == nil || cmd.Enabled()) {
				cmd.Run()
			}
		})
		return
	}
}

// ---- View

// view draws the dialog; it is called every frame, as the dialog requires.
func (p *Plugin) view(c *ui.Context) {
	var items []command.Item
	if p.open {
		items = p.items()
	}
	command.CommandDialog(c, &p.open, &p.state, items, command.Options{
		Filter:      filter,
		SearchLabel: "Search commands and conversations", Placeholder: "Search conversations or run a command",
		ResultsLabel: "Results", EmptyText: "No matching commands or conversations",
		OnActivate: p.activate,
	}, components.DialogOptions{Label: "Command palette"})
}

// items lists the conversations, then the commands.
func (p *Plugin) items() []command.Item {
	var items []command.Item
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
		for _, conv := range ws.Conversations() {
			items = append(items, command.Item{ID: sessionID + string(conv.ID), Title: cmp.Or(conv.Title, "Untitled chat"),
				Description: native.Tilde(conv.Folder), Group: groupChats, Icon: lucide.MessageSquare()})
		}
	}
	for _, e := range kernel.Items(p.ctx, kernel.Commands) {
		if e.ID == openID || e.Value.Run == nil {
			continue
		}
		disabled := false
		if e.Value.Enabled != nil {
			kernel.Invoke(p.ctx, e, func(cmd kernel.Command) { disabled = !cmd.Enabled() })
		}
		item := command.Item{ID: e.ID, Title: e.Value.Title, Group: groupCmds, Disabled: disabled}
		if e.Value.Menu != "" {
			item.Keywords = []string{e.Value.Menu}
		}
		if len(e.Value.Keys) > 0 {
			item.Shortcut = shortcutLabel(e.Value.Keys[0])
		}
		items = append(items, item)
	}
	return items
}

// filter matches typed words against titles and menus. With nothing typed it
// lists the latest few conversations before the commands.
func filter(items []command.Item, query string) []command.Item {
	if strings.TrimSpace(query) != "" {
		return command.Filter(items, query)
	}
	var chats, commands []command.Item
	for _, it := range items {
		switch {
		case it.Group != groupChats:
			commands = append(commands, it)
		case len(chats) < recentShown:
			chats = append(chats, it)
		}
	}
	return append(chats, commands...)
}

// shortcutLabel spells a command's accelerator the way macOS menus do:
// "cmd+shift+a" is "⇧⌘A".
func shortcutLabel(keys string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(keys)), "+")
	var ctrl, alt, shift, cmd bool
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "ctrl":
			ctrl = true
		case "alt", "option":
			alt = true
		case "shift":
			shift = true
		case "cmd", "cmdorctrl":
			cmd = true
		}
	}
	var b strings.Builder
	for _, m := range []struct {
		on  bool
		sym string
	}{{ctrl, "⌃"}, {alt, "⌥"}, {shift, "⇧"}, {cmd, "⌘"}} {
		if m.on {
			b.WriteString(m.sym)
		}
	}
	key := parts[len(parts)-1]
	if sym, ok := keySymbols[key]; ok {
		key = sym
	} else {
		key = strings.ToUpper(key)
	}
	return b.String() + key
}

var keySymbols = map[string]string{
	"enter": "↩", "escape": "⎋", "esc": "⎋", "tab": "⇥", "space": "Space", "backspace": "⌫", "delete": "⌦",
	"left": "←", "right": "→", "up": "↑", "down": "↓", "pageup": "⇞", "pagedown": "⇟", "home": "↖", "end": "↘",
}
