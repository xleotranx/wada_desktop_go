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
	case "write", "edit", "apply_patch":
		return protocol.ToolEdit
	case "fetch", "web", "web_search":
		return protocol.ToolFetch
	default:
		return protocol.ToolOther
	}
}
