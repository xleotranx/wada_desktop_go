package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestPendingApprovalPill points the composer at the open chat's waiting
// asks, pluralizes them and leaves when they are answered.
func TestPendingApprovalPill(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Approval work"}}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Approval work"
	ask := func(id string) {
		a.applyUpdate(opencode.Update{Kind: opencode.PermissionAsked, SessionID: "ses_a",
			Permission: &opencode.PermissionData{
				ID: id, SessionID: "ses_a", Action: "shell",
				Resources: []string{"go test ./..."},
			}})
	}
	ask("perm-1")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("1 approval waiting") {
		t.Fatalf("the waiting approval is not named: %v", tt.Texts())
	}
	if err := tt.Click("Review the approval"); err != nil {
		t.Fatal(err)
	}
	ask("perm-2")
	tt.Frame()
	if !tt.HasText("2 approvals waiting") {
		t.Fatalf("the second approval is not counted: %v", tt.Texts())
	}
	if err := tt.Click("Review the approvals"); err != nil {
		t.Fatal(err)
	}
	a.dropPerm("perm-1")
	a.dropPerm("perm-2")
	tt.Frame()
	if tt.HasText("approval waiting") {
		t.Fatalf("an answered approval still waits: %v", tt.Texts())
	}
}
