// Package palette is the command palette (⌘K): one search box over every
// registered command and over the conversations the backend holds. Commands
// come straight from kernel.Commands, so a command another plugin contributes
// is in the palette without that plugin knowing about it.
package palette

import (
	"cmp"
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"

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
// Commands need nothing from the backend. Conversations come from agent.Service
// when one is active, so the palette keeps working while the backend is off.
type Plugin struct {
	ctx *kernel.Context

	open  bool
	state command.State

	recent   []agent.Session // the newest conversations, read when the palette opens
	found    []agent.Session // what the latest search added to them
	searched string          // the query found belongs to
	load     kernel.Task
	search   kernel.Task
	// searchDelay waits before a search goes out, so typing sends one.
	searchDelay time.Duration
	home        string // shown as ~ in a conversation's folder
}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "palette", Title: "Command palette",
		Description: "Search conversations and run any command with ⌘K.",
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.open, p.recent, p.found, p.searched = false, nil, nil, ""
	if p.searchDelay == 0 {
		p.searchDelay = 200 * time.Millisecond
	}
	p.home, _ = os.UserHomeDir()
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
// closes itself on Escape or a press outside, so everything a search left
// behind is dropped when the palette opens, not when it closes.
func (p *Plugin) toggle() {
	if p.open {
		p.open = false
		return
	}
	p.open = true
	p.state.Query, p.state.ActiveID = "", ""
	p.search.Cancel()
	p.found, p.searched = nil, ""
	p.refresh()
}

// refresh reads the newest conversations.
func (p *Plugin) refresh() {
	svc, ok := kernel.Use(p.ctx, agent.ServiceKey)
	if !ok {
		p.recent = nil
		return
	}
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) (agent.SessionPage, error) {
		return svc.Sessions(ctx, agent.SessionQuery{})
	}, func(page agent.SessionPage, err error) {
		if err == nil { // the commands still work; the sidebar reports a failing backend
			p.recent = page.Sessions
		}
	})
}

// searchFor looks for conversations whose title matches q beyond the newest
// ones, replacing a search in flight. What comes back joins the known
// conversations, which are then filtered like the commands are.
func (p *Plugin) searchFor(q string) {
	q = strings.TrimSpace(q)
	if q == p.searched {
		return
	}
	p.searched = q
	svc, ok := kernel.Use(p.ctx, agent.ServiceKey)
	if q == "" || !ok {
		p.search.Cancel()
		p.found = nil
		return
	}
	delay := p.searchDelay
	kernel.Go(p.ctx, &p.search, func(ctx context.Context) (agent.SessionPage, error) {
		select {
		case <-ctx.Done():
			return agent.SessionPage{}, ctx.Err()
		case <-time.After(delay):
		}
		if err := ctx.Err(); err != nil { // replaced while waiting
			return agent.SessionPage{}, err
		}
		return svc.Sessions(ctx, agent.SessionQuery{Search: q})
	}, func(page agent.SessionPage, err error) {
		if err == nil {
			p.found = page.Sessions
		}
	})
}

// known is every conversation the palette has heard of, newest first.
func (p *Plugin) known() []agent.Session {
	all := slices.Clone(p.recent)
	for _, s := range p.found {
		if !slices.ContainsFunc(all, func(x agent.Session) bool { return x.ID == s.ID }) {
			all = append(all, s)
		}
	}
	slices.SortStableFunc(all, func(a, b agent.Session) int { return cmp.Compare(b.Updated, a.Updated) })
	return all
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
		if !ok {
			return
		}
		for _, s := range p.known() {
			if s.ID == agent.SessionID(sid) {
				ctl.Open(s)
				return
			}
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
		// The query typed so far decides whether a search is due. Starting it
		// is not rendering's job, so it waits for the frame to end.
		if q := strings.TrimSpace(p.state.Query); q != p.searched {
			p.ctx.Post(func() { p.searchFor(q) })
		}
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
	for _, s := range p.known() {
		items = append(items, command.Item{ID: sessionID + string(s.ID), Title: cmp.Or(s.Title, "Untitled chat"),
			Description: p.tilde(s.Directory), Group: groupChats, Icon: lucide.MessageSquare()})
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

// tilde shortens a folder under the home folder.
func (p *Plugin) tilde(dir string) string {
	if rest, ok := strings.CutPrefix(dir, p.home+"/"); ok && p.home != "" {
		return "~/" + rest
	}
	return dir
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
