package pig

import (
	"slices"
	"wada-desktop-go/internal/contract/agent"
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

func permissionFor(mode string, policy *agent.ToolPolicy, kind agent.ToolKind) permission {
	if policy != nil {
		if slices.Contains(policy.Deny, kind) {
			return deny
		}
		if slices.Contains(policy.Allow, kind) {
			return permit
		}
		return ask
	}
	if mode == modeFull || kind == agent.ToolRead || kind == agent.ToolSearch {
		return permit
	}
	return ask
}

func toolKind(name string) agent.ToolKind {
	switch name {
	case "bash", "shell":
		return agent.ToolShell
	case "read":
		return agent.ToolRead
	case "grep", "find", "ls":
		return agent.ToolSearch
	case "write", "edit", "apply_patch":
		return agent.ToolEdit
	case "fetch", "web", "web_search":
		return agent.ToolFetch
	default:
		return agent.ToolOther
	}
}
