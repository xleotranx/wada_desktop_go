package main

import (
	"strings"
	"wada-desktop-go/internal/opencode"

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
	command.CommandDialog(c, &a.layout.commandOpen, &a.layout.commands, items, command.Options{
		SearchLabel:  "Command palette",
		Placeholder:  "Search commands, chats and projects",
		ResultsLabel: "Commands",
		EmptyText:    "No command matches.",
		// Executing a command closes the palette; Escape and backdrop
		// dismissal are the dialog's own.
		OnActivate: func(id string) {
			a.layout.commandOpen = false
			a.runCommand(id)
		},
	}, components.DialogOptions{Label: "Command palette"})
}

// commandItems is the palette's list as the window stands: the fixed
// actions, then every other server, the project folders and the chats the
// active server listed. IDs never change with a title.
func (a *app) commandItems() []command.Item {
	items := []command.Item{
		{ID: "action:new-chat", Title: "New chat", Group: "Actions",
			Description: "Start a draft in a new conversation", Shortcut: "⌘N",
			Icon: icPen, Keywords: []string{"draft", "start", "compose"}},
		{ID: "action:search", Title: "Search chats", Group: "Actions",
			Description: "Filter the conversation list", Shortcut: "⌘⇧F", Icon: icSearch,
			Keywords: []string{"find", "history"}},
		{ID: "action:find", Title: "Find in chat", Group: "Actions",
			Description: "Find text in the open transcript", Shortcut: "⌘F", Icon: icSearch,
			Keywords: []string{"find", "search", "transcript", "match"}},
		{ID: "action:files", Title: "Open Files", Group: "Tools",
			Description: "Browse the workspace beside the chat", Shortcut: "⌘P",
			Icon: icFolder, Keywords: []string{"tree", "explorer", "read"}},
		{ID: "action:changes", Title: "Open Changes", Group: "Tools",
			Description: "Review the working tree or a branch", Shortcut: "⌃⇧G",
			Icon: icGitBranch, Keywords: []string{"diff", "git", "patch"}},
		{ID: "action:settings", Title: "Open Settings", Group: "Tools",
			Description: "Servers, models and this app's version", Shortcut: "⌘,",
			Icon: icGear, Keywords: []string{"preferences", "config"}},
		{ID: "action:shortcuts", Title: "Keyboard shortcuts", Group: "Tools",
			Description: "Every command key the window answers", Shortcut: "⌘/",
			Icon: icKey, Keywords: []string{"keys", "help", "reference"}},
		{ID: "action:terminal", Title: "Toggle terminal", Group: "Tools",
			Description: "Open the shell beneath the chat", Shortcut: "⌘J", Icon: icTerminal,
			Keywords: []string{"shell", "console", "bottom", "panel"}},
	}
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
	items = append(items, command.Item{ID: "action:reset-layout", Title: "Reset pane widths",
		Group: "View", Description: "Return both side panes to their default width", Icon: icPanelRight,
		Keywords: []string{"layout", "resize", "default"}})
	for _, s := range a.connections.servers {
		if s.id == a.connections.activeServer {
			continue
		}
		items = append(items, command.Item{ID: "server:" + s.id, Title: "Switch to " + s.name,
			Group: "Servers", Description: s.url, Icon: icServer,
			Keywords: []string{"server", "connect"}})
	}
	for _, dir := range a.projectDirectories() {
		items = append(items, command.Item{ID: "project:" + dir, Title: "New chat in " + dirBase(dir),
			Group: "Projects", Description: dir, Icon: icFolder,
			Keywords: []string{"project", "folder", "workspace"}})
	}
	// The palette filters on every keystroke, so it carries the newest
	// slice of history rather than a list that paging can grow without
	// bound; the sidebar's search still reads the whole store.
	for _, s := range a.scope().sessions[:min(len(a.scope().sessions), commandChatLimit)] {
		title := s.Title
		if title == "" {
			title = "New chat"
		}
		items = append(items, command.Item{ID: "chat:" + s.ID, Title: title,
			Group: "Chats", Description: s.Directory, Icon: icChat,
			Keywords: []string{"conversation", "session", "open"}})
	}
	return items
}

// runCommand executes one palette choice by its stable ID.
func (a *app) runCommand(id string) {
	switch id {
	case "action:new-chat":
		a.startNewChat()
		return
	case "action:search":
		a.openChatSearch()
		return
	case "action:find":
		a.openChatFind()
		return
	case "action:files":
		a.openTool("Files")
		return
	case "action:changes":
		a.openTool("Changes")
		return
	case "action:settings":
		a.openSettings("")
		return
	case "action:shortcuts":
		a.openSettings(tabShortcuts)
		return
	case "action:terminal":
		a.toggleTerminal()
		return
	case "action:show-tools":
		a.layout.browserCollapsed = false
		return
	case "action:hide-tools":
		a.layout.browserCollapsed = true
		return
	case "action:show-sidebar":
		a.layout.sidebarCollapsed = false
		return
	case "action:hide-sidebar":
		a.layout.sidebarCollapsed = true
		return
	case "action:reset-layout":
		a.layout.sidebarW = sidebarDefaultWidth
		a.layout.browserW = browserDefaultWidth
		a.terminal.height = terminalDefaultHeight
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
	a.openConversation(opencode.Session{ID: id})
}
