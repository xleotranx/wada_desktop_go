package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestDraftScreenProjectChips starts a new chat in a project from the
// draft screen itself, keeping the words already in the composer.
func TestDraftScreenProjectChips(t *testing.T) {
	a := newTestApp(t)
	first := filepath.Join(t.TempDir(), "wada")
	second := filepath.Join(t.TempDir(), "onevra")
	a.addProject(first)
	a.addProject(second)
	a.conversation.draft = "keep these words"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Start in a project") || !tt.HasText("Work in wada") || !tt.HasText("Work in onevra") {
		t.Fatalf("the draft screen lacks its projects: %v", tt.Texts())
	}
	if err := tt.Click("Work in onevra"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.conversation.draftProject != canonDir(second) {
		t.Fatalf("the draft landed in %q", a.conversation.draftProject)
	}
	if a.conversation.draft != "keep these words" {
		t.Fatalf("choosing a project dropped the draft: %q", a.conversation.draft)
	}
	if !tt.HasText("What should we build in onevra?") {
		t.Fatalf("the title did not follow the project: %v", tt.Texts())
	}
}

// TestDraftScreenWithoutProjects keeps the section off an empty list.
func TestDraftScreenWithoutProjects(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if tt.HasText("Start in a project") {
		t.Fatalf("an empty list offered projects: %v", tt.Texts())
	}
}
