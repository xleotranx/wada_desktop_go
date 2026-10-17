package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProjectsStartEmpty(t *testing.T) {
	a := newApp()
	if len(a.projects) != 0 {
		t.Fatalf("unexpected discovered projects: %v", a.projects)
	}
}

func TestProjectHoverStartsDraftInFolder(t *testing.T) {
	a := newApp()
	a.addFolder(t.TempDir())
	dir := a.projects[0]
	a.selectedSession = "old-chat"
	a.sessions = []agentSession{{id: "old-chat", dir: a.home}}
	tt := ui.NewTester(a.view, 1400, 920)
	label := "New chat in " + dirBase(dir)
	if tt.HasText(label) {
		t.Fatal("quick action visible before hover")
	}
	box, ok := tt.Find("Open project " + dirBase(dir))
	if !ok {
		t.Fatal("project row missing")
	}
	tt.Move(box.X+20, box.Y+box.H/2)
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if a.selectedSession != "" || a.chatDir() != dir {
		t.Fatalf("quick chat opened in %q with session %q", a.chatDir(), a.selectedSession)
	}
	a.draft = "work here"
	a.send()
	job := <-a.jobs
	if job.dir != dir || job.sid != "" || job.text != "work here" {
		t.Fatalf("wrong prompt target: %+v", job)
	}
}

func TestNewChatProjectPicker(t *testing.T) {
	a := newApp()
	a.addFolder(t.TempDir())
	dir := a.projects[0]
	a.startNewChat()
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Choose project"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Use project " + dirBase(dir)); err != nil {
		t.Fatal(err)
	}
	if a.chatDir() != dir || a.selectedSession != "" {
		t.Fatal("project selection did not configure the draft")
	}
	if !tt.HasText("What should we build in " + dirBase(dir) + "?") {
		t.Fatal("new chat does not show selected project")
	}
	if err := tt.Click("Choose project"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Don't work in a project"); err != nil {
		t.Fatal(err)
	}
	if a.chatDir() != "" {
		t.Fatal("project could not be cleared")
	}
	first := a.draftVersion
	a.startNewChat()
	if a.draftVersion == first {
		t.Fatal("new draft would reuse the previous server chat")
	}
}

func TestProjectPickerFiltersAndAddsFolder(t *testing.T) {
	a := newApp()
	a.addProject(filepath.Join(t.TempDir(), "alpha"))
	a.addProject(filepath.Join(t.TempDir(), "beta"))
	a.projectPickerOpen = true
	a.projectFilter = "beta"
	a.draft = "keep my draft"
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Use project alpha") || !tt.HasText("Use project beta") {
		t.Fatal("project filter did not narrow the list")
	}
	if err := tt.Click("New project…"); err != nil {
		t.Fatal(err)
	}
	a.dirDraft = t.TempDir()
	if err := tt.Click("Add project"); err != nil {
		t.Fatal(err)
	}
	if a.draftProject == "" || a.draft != "keep my draft" || a.dirOpen {
		t.Fatal("adding a project did not preserve and configure the draft")
	}
	if a.chatDir() != a.projects[len(a.projects)-1] {
		t.Fatal("new project was not selected")
	}
}

func TestLegacyImportedProjectsAreRemovedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), sidebarMetadata{projects: []string{"/imported"}, pins: []string{"keep-chat"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), "PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a := newApp()
	db, err = a.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 0 || !a.pinned["keep-chat"] {
		t.Fatal("migration must clear project imports while keeping chat pins")
	}
	a.addFolder(t.TempDir())
	if err := db.save(context.Background(), a.sidebarSnapshot()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b := newApp()
	db, err = b.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.projects) != 1 {
		t.Fatal("explicit project removed on next restart")
	}
}
