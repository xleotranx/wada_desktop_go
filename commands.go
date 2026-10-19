package main

import (
	"strings"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/command"
)

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
	command.CommandDialog(c, &a.layout.commandOpen, &a.layout.commands, a.commandItems(), command.Options{
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
			Description: "Filter the conversation list", Shortcut: "⌘F", Icon: icSearch,
			Keywords: []string{"find", "history"}},
		{ID: "action:files", Title: "Open Files", Group: "Tools",
			Description: "Browse the workspace beside the chat", Shortcut: "⌘P",
			Icon: icFolder, Keywords: []string{"tree", "explorer", "read"}},
		{ID: "action:changes", Title: "Open Changes", Group: "Tools",
			Description: "Review the working tree or a branch", Shortcut: "⌃⇧G",
			Icon: icGitBranch, Keywords: []string{"diff", "git", "patch"}},
		{ID: "action:settings", Title: "Open Settings", Group: "Tools",
			Description: "Servers, models and this app's version", Shortcut: "⌘,",
			Icon: icGear, Keywords: []string{"preferences", "config"}},
	}
	if a.layout.browserCollapsed {
		items = append(items, command.Item{ID: "action:show-tools", Title: "Show tools",
			Group: "View", Description: "Open the pane beside the chat", Icon: icPanelRight})
	} else {
		items = append(items, command.Item{ID: "action:hide-tools", Title: "Hide tools",
			Group: "View", Description: "Fold the pane beside the chat away", Icon: icPanelRight})
	}
	if a.layout.sidebarCollapsed {
		items = append(items, command.Item{ID: "action:show-sidebar", Title: "Show sidebar",
			Group: "View", Description: "Bring the conversations back", Icon: icPanel})
	} else {
		items = append(items, command.Item{ID: "action:hide-sidebar", Title: "Hide sidebar",
			Group: "View", Description: "Give the chat the whole window", Icon: icPanel})
	}
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
	for _, s := range a.scope().sessions {
		title := s.Title
		if title == "" {
			title = "New chat"
		}
		items = append(items, command.Item{ID: "chat:" + s.ID, Title: title,
			Group: "Chats", Description: s.Directory, Icon: icHistory,
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
	case "action:files":
		a.openTool("Files")
		return
	case "action:changes":
		a.openTool("Changes")
		return
	case "action:settings":
		a.openSettings("")
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
