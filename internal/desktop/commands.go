package desktop

import (
	"strings"
	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/command"
)

// commandChatLimit bounds the chats the palette lists; filtering runs on
// every keystroke, and the sidebar's search stays the way to the store.
const commandChatLimit = 200

// openCommandPalette shows the window's command dialog and starts it empty,
// so the first keystrokes search rather than continue the last search.
func (a *app) openCommandPalette() {
	a.layout.commandOpen = true
	a.layout.commandScope = ""
	a.layout.commands.Query = ""
	a.layout.commands.ActiveID = ""
}

// commandDialog is the Cmd+K palette: one search over the app's actions,
// servers, projects and chats.
func (a *app) commandDialog(c *ui.Context) {
	// Closed, the dialog draws nothing, and the list is not worth building
	// over every conversation the server listed.
	var items []command.Item
	if a.layout.commandOpen {
		items = a.commandItems()
	}
	placeholder := "Search commands and chats"
	switch a.layout.commandScope {
	case commandScopeCommands:
		placeholder = "Search commands"
	case commandScopeChats:
		placeholder = "Search chats and projects"
	}
	command.CommandDialog(c, &a.layout.commandOpen, &a.layout.commands, items, command.Options{
		SearchLabel:  "Command palette",
		Placeholder:  placeholder + " ( > commands · @ chats )",
		ResultsLabel: a.commandScopeLabel(),
		EmptyText:    "No command matches.",
		Filter:       a.filterCommandItems,
		// Executing a command closes the palette; choosing a scope keeps
		// it open with the list narrowed. Escape and backdrop dismissal are
		// the dialog's own.
		OnActivate: func(id string) {
			if scope, ok := commandScopeIDs[id]; ok {
				a.layout.commandScope = scope
				a.layout.commands.Query = ""
				a.layout.commands.ActiveID = ""
				a.layout.commands.Focus()
				return
			}
			a.layout.commandOpen = false
			a.runCommand(id)
		},
	}, components.DialogOptions{Label: "Command palette"})
}

// commandItems is the palette's list as the window stands: the fixed
// actions, then every other server, the project folders and the chats the
// active server listed. IDs never change with a title.
func (a *app) commandItems() []command.Item {
	unreadTitle := "Mark chat as unread"
	if id := string(a.conversation.target.session); id != "" && a.scope().unread[id] {
		unreadTitle = "Mark chat as read"
	}
	items := commandScopeItems()
	items = append(items, []command.Item{
		{ID: "action:new-chat", Title: "New chat", Group: "Actions",
			Description: "Start a draft in a new conversation", Shortcut: "⌘N",
			Icon: icPen, Keywords: []string{"draft", "start", "compose"}},
		{ID: "action:history", Title: "Chat history", Group: "Chats",
			Description: "Every chat this app has listed, newest first", Icon: icHistory,
			Keywords: []string{"history", "recents", "older", "chats"}},
		{ID: "action:search", Title: "Search chats", Group: "Actions",
			Description: "Filter the conversation list", Shortcut: "⌘⌥F", Icon: icSearch,
			Keywords: []string{"find", "history"}},
		{ID: "action:find", Title: "Find in chat", Group: "Actions",
			Description: "Find text in the open transcript", Shortcut: "⌘F", Icon: icSearch,
			Keywords: []string{"find", "search", "transcript", "match"}},
		{ID: "action:model", Title: "Choose model", Group: "Chats",
			Description: "Pick what this chat runs on", Shortcut: "⌃⇧M", Icon: icCpu,
			Keywords: []string{"model", "reasoning", "provider"}},
		{ID: "action:filter-unread", Title: "Show unread chats", Group: "Chats",
			Description: "Narrow the sidebar to chats with news", Icon: icFilter,
			Keywords: []string{"filter", "unread", "sidebar"}},
		{ID: "action:filter-attention", Title: "Show chats needing attention", Group: "Chats",
			Description: "Narrow the sidebar to waiting approvals", Icon: icFilter,
			Keywords: []string{"filter", "attention", "approval", "sidebar"}},
		{ID: "action:filter-drafts", Title: "Show chats with drafts", Group: "Chats",
			Description: "Narrow the sidebar to unsent words", Icon: icPen,
			Keywords: []string{"filter", "drafts", "sidebar"}},
		{ID: "action:filter-archived", Title: "Show archived chats", Group: "Chats",
			Description: "Narrow the sidebar to chats put away", Icon: icArchive,
			Keywords: []string{"filter", "archived", "hidden", "sidebar"}},
		{ID: "action:filter-clear", Title: "Show all chats", Group: "Chats",
			Description: "Clear the sidebar filter", Icon: icFilter,
			Keywords: []string{"filter", "all", "clear", "sidebar"}},
		{ID: "action:unread", Title: unreadTitle, Group: "Chats",
			Description: "Keep a dot on this chat until it is opened again",
			Shortcut:    "⌘⇧U", Icon: icBell,
			Keywords: []string{"unread", "read", "notification", "attention"}},
		{ID: "action:next-marked", Title: "Next chat needing attention", Group: "Chats",
			Description: "Open the next unread chat or waiting approval",
			Shortcut:    "⌥⌘A", Icon: icBell,
			Keywords: []string{"next", "unread", "attention", "approval"}},
		{ID: "action:activity", Title: "Activity view", Group: "Tools",
			Description: "Watch what the agent has run in this chat", Shortcut: "⌥⌘U",
			Icon: icActivity, Keywords: []string{"tools", "steps", "running", "shell"}},
		{ID: "action:settings", Title: "Open Settings", Group: "Tools",
			Description: "Servers, models and this app's version", Shortcut: "⌘,",
			Icon: icGear, Keywords: []string{"preferences", "config"}},
		{ID: "action:shortcuts", Title: "Keyboard shortcuts", Group: "Tools",
			Description: "Every command key the window answers", Shortcut: "⌘/",
			Icon: icKey, Keywords: []string{"keys", "help", "reference"}},
		{ID: "action:edit-shortcuts", Title: "Customize shortcuts", Group: "Tools",
			Description: "Change the window's command keys", Shortcut: "",
			Icon: icKey, Keywords: []string{"keys", "remap", "bindings", "customize"}},
	}...)
	if a.layout.browserCollapsed {
		items = append(items, command.Item{ID: "action:show-tools", Title: "Show tools",
			Group: "View", Description: "Open the pane beside the chat", Shortcut: "⌘⇧J", Icon: icPanelRight})
	} else {
		items = append(items, command.Item{ID: "action:hide-tools", Title: "Hide tools",
			Group: "View", Description: "Fold the pane beside the chat away", Shortcut: "⌘⇧J", Icon: icPanelRight})
	}
	if a.layout.sidebarCollapsed {
		items = append(items, command.Item{ID: "action:show-sidebar", Title: "Show sidebar",
			Group: "View", Description: "Bring the conversations back", Shortcut: "⌘B", Icon: icPanel})
	} else {
		items = append(items, command.Item{ID: "action:hide-sidebar", Title: "Hide sidebar",
			Group: "View", Description: "Give the chat the whole window", Shortcut: "⌘B", Icon: icPanel})
	}
	if a.layout.fullView {
		items = append(items, command.Item{ID: "action:full-view", Title: "Exit full view",
			Group: "View", Description: "Bring the side panes back", Shortcut: "⌘⇧F", Icon: icCollapse})
	} else {
		items = append(items, command.Item{ID: "action:full-view", Title: "Enter full view",
			Group: "View", Description: "Give the conversation the whole window", Shortcut: "⌘⇧F", Icon: icExpand})
	}
	if sid := string(a.conversation.target.session); sid != "" {
		items = append(items, command.Item{ID: "action:copy-transcript", Title: "Copy transcript",
			Group: "Actions", Description: "Put this conversation on the clipboard as markdown",
			Icon: icCopy, Keywords: []string{"copy", "markdown", "export", "clipboard", "transcript"}})
	}
	items = append(items, command.Item{ID: "action:reset-layout", Title: "Reset pane widths",
		Group: "View", Description: "Return both side panes to their default width", Icon: icPanelRight,
		Keywords: []string{"layout", "resize", "default"}})
	for _, action := range a.featureActions() {
		items = append(items, command.Item{ID: "action:" + action.ID, Title: action.Title, Description: action.Description, Icon: action.Icon, Group: "Tools", Keywords: []string{action.ID, action.Description}, Shortcut: a.shortcutKeys(action.Shortcut)})
	}
	for _, s := range a.connections.servers {
		if s.id == a.connections.activeServer {
			continue
		}
		items = append(items, command.Item{ID: "server:" + s.id, Title: "Switch to " + s.name,
			Group: "Servers", Description: s.url, Icon: icServer,
			Keywords: []string{"server", "connect"}})
	}
	for _, dir := range a.visibleProjectDirectories() {
		items = append(items, command.Item{ID: "project:" + dir, Title: "New chat in " + dirBase(dir),
			Group: "Projects", Description: dir, Icon: icFolder,
			Keywords: []string{"project", "folder", "workspace"}})
	}
	// The palette filters on every keystroke, so it carries the newest
	// slice of history rather than a list that paging can grow without
	// bound; the sidebar's search still reads the whole store.
	listed := a.listedSessions()
	for _, s := range listed[:min(len(listed), commandChatLimit)] {
		title := s.Title
		if title == "" {
			title = "New chat"
		}
		items = append(items, command.Item{ID: "chat:" + s.ID, Title: title,
			Group: "Chats", Description: s.Directory, Icon: icChat,
			Keywords: []string{"conversation", "session", "open"}})
	}
	for i := range items {
		if id, ok := shortcutActionIDs[items[i].ID]; ok {
			items[i].Shortcut = a.shortcutKeys(id)
		}
	}
	return a.withRecentCommands(items)
}

// commandRecentsLimit bounds the palette's Recent group.
const commandRecentsLimit = 5

// rememberCommand puts one action at the front of the palette's history,
// newest first, once each.
func (a *app) rememberCommand(id string) {
	recents := []string{id}
	for _, recent := range a.preferences.commandRecents {
		if recent != id && len(recents) < commandRecentsLimit {
			recents = append(recents, recent)
		}
	}
	a.preferences.commandRecents = recents
	a.saveState()
}

// withRecentCommands lifts the remembered actions into a Recent group at
// the top of the palette and takes their old rows out, so each action
// stands once.
func (a *app) withRecentCommands(items []command.Item) []command.Item {
	if len(a.preferences.commandRecents) == 0 {
		return items
	}
	byID := make(map[string]command.Item, len(items))
	for _, item := range items {
		if _, exists := byID[item.ID]; !exists {
			byID[item.ID] = item
		}
	}
	recent := make([]command.Item, 0, len(a.preferences.commandRecents))
	for _, id := range a.preferences.commandRecents {
		item, ok := byID[id]
		if !ok {
			continue
		}
		item.Group = "Recent"
		recent = append(recent, item)
	}
	if len(recent) == 0 {
		return items
	}
	skip := make(map[string]bool, len(recent))
	for _, item := range recent {
		skip[item.ID] = true
	}
	// The scope switcher keeps the top of the list; the history sits just
	// under it, ahead of the ordinary groups.
	lead := 0
	for lead < len(items) && items[lead].Group == "Filter" {
		lead++
	}
	out := make([]command.Item, 0, len(items))
	out = append(out, items[:lead]...)
	out = append(out, recent...)
	for _, item := range items[lead:] {
		if !skip[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

// runCommand executes one palette choice by its stable ID, remembering
// the actions for the palette's Recent group.
func (a *app) runCommand(id string) {
	if strings.HasPrefix(id, "action:") && a.runFeatureAction(strings.TrimPrefix(id, "action:")) {
		a.rememberCommand(id)
		return
	}
	if strings.HasPrefix(id, "action:") {
		a.rememberCommand(id)
	}
	switch id {
	case "action:new-chat":
		a.startNewChat()
		return
	case "action:search":
		a.openChatSearch()
		return
	case "action:history":
		a.openHistory()
		return
	case "action:find":
		a.openChatFind()
		return
	case "action:model":
		a.openModelPicker()
		return
	case "action:filter-unread":
		a.setSidebarFilter(sidebarFilterUnread)
		return
	case "action:filter-attention":
		a.setSidebarFilter(sidebarFilterAttention)
		return
	case "action:filter-drafts":
		a.setSidebarFilter(sidebarFilterDrafts)
		return
	case "action:filter-archived":
		a.setSidebarFilter(sidebarFilterArchived)
		return
	case "action:filter-clear":
		a.setSidebarFilter("")
		return
	case "action:unread":
		a.toggleUnread()
		return
	case "action:next-marked":
		a.openNextMarked()
		return
	case "action:activity":
		a.toggleActivity()
		return
	case "action:settings":
		a.openSettings("")
		return
	case "action:shortcuts":
		a.openSettings(tabShortcuts)
		return
	case "action:edit-shortcuts":
		a.openShortcuts()
		return
	case "action:full-view":
		a.toggleFullView()
		return
	case "action:copy-transcript":
		a.copyTranscript()
		return
	case "action:show-tools":
		a.showTools()
		return
	case "action:hide-tools":
		a.hideTools()
		return
	case "action:show-sidebar":
		a.showSidebar()
		return
	case "action:hide-sidebar":
		a.hideSidebar()
		return
	case "action:reset-layout":
		a.leaveFullView(false)
		a.layout.sidebarW = sidebarDefaultWidth
		a.layout.browserW = browserDefaultWidth
		if bottom := a.bottom(); bottom != nil {
			bottom.ResetSize()
		}
		a.saveState()
		return
	}
	switch {
	case strings.HasPrefix(id, "server:"):
		a.useServer(strings.TrimPrefix(id, "server:"))
	case strings.HasPrefix(id, "project:"):
		a.startChatIn(strings.TrimPrefix(id, "project:"))
	case strings.HasPrefix(id, "chat:"):
		a.openCommandChat(strings.TrimPrefix(id, "chat:"))
	}
}

// openCommandChat opens one listed conversation, or a session by ID the
// list no longer holds after a refresh.
func (a *app) openCommandChat(id string) {
	for _, s := range a.scope().sessions {
		if s.ID == id {
			a.openConversation(s)
			return
		}
	}
	a.openConversation(agent.Session{ID: id})
}
