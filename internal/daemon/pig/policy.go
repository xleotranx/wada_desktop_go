package pig

import (
	"slices"
	"wada-desktop-go/internal/protocol"
)

const (
	modeAsk  = "ask"
	modeFull = "full"
)

type permission uint8

const (
	ask permission = iota
	permit
	deny
)

func permissionFor(mode string, policy *protocol.ToolPolicy, kind protocol.ToolKind) permission {
	if policy != nil {
		if slices.Contains(policy.Deny, kind) {
			return deny
		}
		if slices.Contains(policy.Allow, kind) {
			return permit
		}
		return ask
	}
	if mode == modeFull || kind == protocol.ToolRead || kind == protocol.ToolSearch {
		return permit
	}
	return ask
}

func toolKind(name string) protocol.ToolKind {
	switch name {
	case "bash", "shell":
		return protocol.ToolShell
	case "read":
		return protocol.ToolRead
	case "grep", "find", "ls":
		return protocol.ToolSearch
	// archive_workspace can force-remove a worktree and its uncommitted work.
	case "write", "edit", "apply_patch", "create_workspace", "archive_workspace", "stop_agent":
		return protocol.ToolEdit
	case "fetch", "web", "web_search":
		return protocol.ToolFetch
	case "list_providers", "list_profiles", "list_workspaces", "wait_agent", "inspect_agent_diff":
		return protocol.ToolRead
	case "spawn_agent", "send_agent":
		return protocol.ToolOther
	default:
		return protocol.ToolOther
	}
}
