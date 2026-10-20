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
	unreadTitle := "Mark chat as unread"
	if id := string(a.conversation.target.session); id != "" && a.scope().unread[id] {
		unreadTitle = "Mark chat as read"
	}
	items := []command.Item{
		{ID: "action:new-chat", Title: "New chat", Group: "Actions",
			Description: "Start a draft in a new conversation", Shortcut: "⌘N",
			Icon: icPen, Keywords: []string{"draft", "start", "compose"}},
		{ID: "action:folder", Title: "Open folder", Group: "Actions",
			Description: "Add a project folder", Shortcut: "⌘O",
			Icon: icFolder, Keywords: []string{"project", "workspace", "directory"}},
		{ID: "action:search", Title: "Search chats", Group: "Actions",
			Description: "Filter the conversation list", Shortcut: "⌘⌥F", Icon: icSearch,
			Keywords: []string{"find", "history"}},
		{ID: "action:find", Title: "Find in chat", Group: "Actions",
			Description: "Find text in the open transcript", Shortcut: "⌘F", Icon: icSearch,
			Keywords: []string{"find", "search", "transcript", "match"}},
		{ID: "action:model", Title: "Choose model", Group: "Chats",
			Description: "Pick what this chat runs on", Shortcut: "⌃⇧M", Icon: icCpu,
			Keywords: []string{"model", "reasoning", "provider"}},
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
		{ID: "action:files", Title: "Quick open file", Group: "Tools",
			Description: "Find and open a workspace file", Shortcut: "⌘P",
			Icon: icFolder, Keywords: []string{"files", "search", "open", "tree"}},
		{ID: "action:changes", Title: "Open Changes", Group: "Tools",
			Description: "Review the working tree or a branch", Shortcut: "⌃⇧G",
			Icon: icGitBranch, Keywords: []string{"diff", "git", "patch"}},
		{ID: "action:settings", Title: "Open Settings", Group: "Tools",
			Description: "Servers, models and this app's version", Shortcut: "⌘,",
			Icon: icGear, Keywords: []string{"preferences", "config"}},
		{ID: "action:shortcuts", Title: "Keyboard shortcuts", Group: "Tools",
			Description: "Every command key the window answers", Shortcut: "⌘/",
			Icon: icKey, Keywords: []string{"keys", "help", "reference"}},
		{ID: "action:edit-shortcuts", Title: "Customize shortcuts", Group: "Tools",
			Description: "Change the window's command keys", Shortcut: "",
			Icon: icKey, Keywords: []string{"keys", "remap", "bindings", "customize"}},
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
	if a.layout.fullView {
		items = append(items, command.Item{ID: "action:full-view", Title: "Exit full view",
			Group: "View", Description: "Bring the side panes back", Shortcut: "⌘⇧F", Icon: icCollapse})
	} else {
		items = append(items, command.Item{ID: "action:full-view", Title: "Enter full view",
			Group: "View", Description: "Give the conversation the whole window", Shortcut: "⌘⇧F", Icon: icExpand})
	}
	if len(a.scope().closedTabs) > 0 {
		items = append(items, command.Item{ID: "action:reopen-tab", Title: "Reopen closed chat tab",
			Group: "View", Description: "Bring back the last tab a cross closed",
			Shortcut: "⌘⇧T", Icon: icHistory})
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
	for i := range items {
		if id, ok := shortcutActionIDs[items[i].ID]; ok {
			items[i].Shortcut = a.shortcutKeys(id)
		}
	}
	return items
}

// runCommand executes one palette choice by its stable ID.
func (a *app) runCommand(id string) {
	switch id {
	case "action:new-chat":
		a.startNewChat()
		return
	case "action:folder":
		a.openProjectDialog()
		return
	case "action:search":
		a.openChatSearch()
		return
	case "action:find":
		a.openChatFind()
		return
	case "action:model":
		a.openModelPicker()
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
	case "action:files":
		a.openQuickOpen()
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
	case "action:edit-shortcuts":
		a.openShortcuts()
		return
	case "action:terminal":
		a.toggleTerminal()
		return
	case "action:full-view":
		a.toggleFullView()
		return
	case "action:reopen-tab":
		a.reopenClosedTab()
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
