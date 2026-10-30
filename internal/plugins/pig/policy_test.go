package pig

import (
	"testing"
	"wada-desktop-go/internal/contract/agent"
)

func TestAccessPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		policy     *agent.ToolPolicy
		kind       agent.ToolKind
		want       permission
	}{
		{"read", modeAsk, nil, agent.ToolRead, permit},
		{"write", modeAsk, nil, agent.ToolEdit, ask},
		{"shell", modeAsk, nil, agent.ToolShell, ask},
		{"web", modeAsk, nil, agent.ToolFetch, ask},
		{"unknown", modeAsk, nil, agent.ToolOther, ask},
		{"full", modeFull, nil, agent.ToolEdit, permit},
		{"child denies full", modeFull, &agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}, agent.ToolEdit, deny},
		{"child asks full", modeFull, &agent.ToolPolicy{}, agent.ToolShell, ask},
		{"deny wins", modeFull, &agent.ToolPolicy{Allow: []agent.ToolKind{agent.ToolRead}, Deny: []agent.ToolKind{agent.ToolRead}}, agent.ToolRead, deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionFor(tc.mode, tc.policy, tc.kind); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
