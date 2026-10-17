package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click and type as a user
// would.
func TestView(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	// The interface starts on the demo chat.
	if a.selected != "Implement PD-09 outcomes and retries" {
		t.Fatalf("selected %q at start", a.selected)
	}
	if !tt.HasText("The full gate is now running the browser journeys.") {
		t.Fatalf("transcript %q not shown", tt.Texts())
	}
	if !tt.HasText("Full access") || !tt.HasText("longcat-2.5-preview-free") {
		t.Fatalf("composer missing: %v", tt.Texts())
	}
}

func TestChooseChat(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Start PD-08"); err != nil {
		t.Fatal(err)
	}
	if a.selected != "Start PD-08" {
		t.Fatalf("chosen chat %q after a click", a.selected)
	}

	// A folder row opens its chats instead of choosing one.
	if err := tt.Click("wada_automation"); err != nil {
		t.Fatal(err)
	}
	if !a.expanded["folder:wada_automation"] {
		t.Fatal("clicking a closed folder should open it")
	}
	if !tt.HasText("Nightly digest job") {
		t.Fatalf("chats under the folder not shown: %v", tt.Texts())
	}
}

func TestActivityExpands(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("go tool mygo test ./...") {
		t.Fatalf("expanded commands missing: %v", tt.Texts())
	}
	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("go tool mygo test ./...") {
		t.Fatal("expanded commands did not close")
	}
}

func TestComposerSends(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	// Pick a chat, then work in the composer.
	if err := tt.Click("Start PD-08"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Ship the boundary check")
	if a.draft != "Ship the boundary check" {
		t.Fatalf("draft %q after typing", a.draft)
	}
	tt.Key(0, ui.KeyEnter)
	if a.draft != "" {
		t.Fatalf("draft %q after Enter", a.draft)
	}
	if len(a.blocks) == 0 || a.blocks[len(a.blocks)-1].text != "Ship the boundary check" {
		t.Fatalf("last block %v", a.blocks[len(a.blocks)-1])
	}
	if a.blocks[len(a.blocks)-1].kind != "user" {
		t.Fatal("the sent block is not the user's")
	}

	// The round button turns into the send button once anything is typed.
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Type("One more")
	if !tt.HasText("Send message") {
		t.Fatalf("send button not shown; texts %q", tt.Texts())
	}
	if a.draft != "One more" {
		t.Fatalf("draft %q", a.draft)
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatal(err)
	}
	if a.draft != "" {
		t.Fatalf("draft %q after the send button", a.draft)
	}
	if n := len(a.blocks); n == 0 || a.blocks[n-1].text != "One more" {
		t.Fatalf("last block %v", a.blocks[n-1])
	}
	if tt.HasText("Send message") {
		t.Fatal("send button stayed after sending")
	}
}
