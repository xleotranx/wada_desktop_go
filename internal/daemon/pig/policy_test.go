package pig

import (
	"testing"
	"wada-desktop-go/internal/protocol"
)

func TestAccessPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		policy     *protocol.ToolPolicy
		kind       protocol.ToolKind
		want       permission
	}{
		{"read", modeAsk, nil, protocol.ToolRead, permit},
		{"write", modeAsk, nil, protocol.ToolEdit, ask},
		{"shell", modeAsk, nil, protocol.ToolShell, ask},
		{"web", modeAsk, nil, protocol.ToolFetch, ask},
		{"unknown", modeAsk, nil, protocol.ToolOther, ask},
		{"full", modeFull, nil, protocol.ToolEdit, permit},
		{"child denies full", modeFull, &protocol.ToolPolicy{Deny: []protocol.ToolKind{protocol.ToolEdit}}, protocol.ToolEdit, deny},
		{"child asks full", modeFull, &protocol.ToolPolicy{}, protocol.ToolShell, ask},
		{"deny wins", modeFull, &protocol.ToolPolicy{Allow: []protocol.ToolKind{protocol.ToolRead}, Deny: []protocol.ToolKind{protocol.ToolRead}}, protocol.ToolRead, deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionFor(tc.mode, tc.policy, tc.kind); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChiefToolKinds(t *testing.T) {
	for name, want := range map[string]protocol.ToolKind{
		"wait_agent":         protocol.ToolRead,
		"inspect_agent_diff": protocol.ToolRead,
		"stop_agent":         protocol.ToolEdit,
		"create_worktree":    protocol.ToolEdit,
	} {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %v, want %v", name, got, want)
		}
	}
	if got := permissionFor(modeAsk, nil, toolKind("stop_agent")); got != ask {
		t.Fatalf("stop_agent permission in ask mode = %v, want ask", got)
	}
}
