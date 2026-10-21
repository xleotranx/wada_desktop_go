package desktop

import (
	"testing"

	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestActivityViewListsAndExpands opens the Activity view with its key and
// drives one step: the list names what ran, says the turn is live, and a
// row opens the detail the transcript folds away.
func TestActivityViewListsAndExpands(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_activity")
	a.conversation.selected = "Activity"
	a.conversation.transcript.Start("ses_activity")
	a.conversation.transcript.Append(chat.Block{
		Kind: chat.KindActivity, Text: "Ran tests", Key: "run", Icon: "shell",
		Detail: []string{"$ go test ./...", "ok"},
	})
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindNote, Text: "A note"})
	tt := ui.NewTester(a.view, 1400, 920)

	tt.Key(ui.Cmd|ui.Alt, ui.KeyU)
	tt.Frame()
	if a.layout.browserCollapsed || a.layout.browserTool != "Activity" {
		t.Fatalf("Cmd+Option+U opened tool %q (collapsed=%v)",
			a.layout.browserTool, a.layout.browserCollapsed)
	}
	for _, want := range []string{"Ran tests", "Live", "2 steps", "A note"} {
		if !tt.HasText(want) {
			t.Fatalf("the Activity view is missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Activity step: Ran tests"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("$ go test ./...") {
		t.Fatalf("the step detail did not open: %v", tt.Texts())
	}

	tt.Key(ui.Cmd|ui.Alt, ui.KeyU)
	tt.Frame()
	if !a.layout.browserCollapsed {
		t.Fatal("the second toggle left the Activity view open")
	}
}

// TestActivityViewShowsApprovals keeps a waiting approval visible beside
// the chat, with the resources it names.
func TestActivityViewShowsApprovals(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_activity")
	a.conversation.selected = "Activity"
	seedTranscript(a, []chat.Block{{
		Kind: chat.KindPermission,
		Request: &chat.Permission{
			ID: "perm-1", Session: "ses_activity", Action: "edit",
			Resources: []string{"src/app.go"},
		},
	}})
	a.openTool("Activity")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Approval needed") || !tt.HasText("src/app.go") {
		t.Fatalf("the approval is missing: %v", tt.Texts())
	}
}

// TestActivityViewEmpty tells the truth before anything has run.
func TestActivityViewEmpty(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_empty")
	a.conversation.selected = "Empty"
	a.openTool("Activity")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Nothing has run in this chat yet.") {
		t.Fatalf("the empty Activity view: %v", tt.Texts())
	}
}
