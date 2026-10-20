package main

import (
	"strings"

	"github.com/oneo-me/onevra/source/components/command"
)

// Command palette scopes: a leading prefix narrows the list before the
// usual text matching, and the Filter group switches scopes without
// leaving the dialog.
const (
	commandScopeCommands = "commands"
	commandScopeChats    = "chats"
)

var commandScopeIDs = map[string]string{
	"scope:all":      "",
	"scope:commands": commandScopeCommands,
	"scope:chats":    commandScopeChats,
}

// splitCommandScope reads a palette query's scope prefix.
func splitCommandScope(query string) (scope, rest string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", ""
	}
	switch q[0] {
	case '>':
		return commandScopeCommands, strings.TrimSpace(q[1:])
	case '@':
		return commandScopeChats, strings.TrimSpace(q[1:])
	}
	return "", query
}

func commandGroupInScope(group, scope string) bool {
	switch scope {
	case commandScopeCommands:
		return group == "Actions" || group == "Tools" || group == "View" || group == "Recent"
	case commandScopeChats:
		return group == "Chats" || group == "Projects"
	}
	return true
}

// filterCommandItems applies the chosen scope, or a query prefix that
// overrides it, then the usual match. The Filter group stays visible so
// another scope is one choice away.
func (a *app) filterCommandItems(items []command.Item, query string) []command.Item {
	scope, rest := splitCommandScope(query)
	if scope != "" {
		query = rest
	} else {
		scope = a.layout.commandScope
	}
	if scope == "" {
		return command.Filter(items, query)
	}
	narrowed := make([]command.Item, 0, len(items))
	for _, item := range items {
		if item.Group == "Filter" || commandGroupInScope(item.Group, scope) {
			narrowed = append(narrowed, item)
		}
	}
	return command.Filter(narrowed, query)
}

// commandScopeLabel names the palette's current scope for its heading.
func (a *app) commandScopeLabel() string {
	switch a.layout.commandScope {
	case commandScopeCommands:
		return "Commands"
	case commandScopeChats:
		return "Chats"
	}
	return "Results"
}

// commandScopeItems are the palette's first rows: the scopes and the way
// back to everything.
func commandScopeItems() []command.Item {
	return []command.Item{
		{ID: "scope:commands", Title: "Show commands", Group: "Filter",
			Description: "Actions, tools and view commands", Icon: icCommand,
			Keywords: []string{"scope", "filter", "actions"}},
		{ID: "scope:chats", Title: "Show chats", Group: "Filter",
			Description: "Conversations and project folders", Icon: icChat,
			Keywords: []string{"scope", "filter", "chats"}},
		{ID: "scope:all", Title: "Show everything", Group: "Filter",
			Description: "Commands, chats, servers and projects", Icon: icFilter,
			Keywords: []string{"scope", "filter", "all", "clear"}},
	}
}
