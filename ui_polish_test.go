package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// TestDraftSuggestionFillsComposer checks the draft screen's starts: a
// chosen card writes its prompt into the composer and asks for the keyboard.
func TestDraftSuggestionFillsComposer(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Explain this codebase. A guided tour of the app's pieces"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.conversation.draft != "Explain this codebase" {
		t.Fatalf("draft = %q", a.conversation.draft)
	}
	if !tt.Focused("Compose") {
		t.Fatal("the composer did not take the keyboard")
	}
}

// TestToastHostShowsAndDrains keeps Onevra's toast viewport mounted: a
// queued toast shows on the next frame and leaves the queue.
func TestToastHostShowsAndDrains(t *testing.T) {
	a := newTestApp(t)
	a.notify(ui.Toast{Title: "Transcript exported", Type: components.ToastSuccess})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Transcript exported") {
		t.Fatalf("the toast did not show: %v", tt.Texts())
	}
	if len(a.layout.toasts) != 0 {
		t.Fatalf("%d toasts left queued", len(a.layout.toasts))
	}
}
