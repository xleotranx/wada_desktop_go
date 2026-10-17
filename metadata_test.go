package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMetadataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sidebarMetadata{projects: []string{"/work/b", "/work/a"}, pins: []string{"old", "new"}, projectPins: map[string]bool{"/work/a": true}}
	if err := db.save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.load(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restored %+v, %v; want %+v", got, err, want)
	}
	// Removing everything is durable, rather than a signal to rediscover.
	empty := sidebarMetadata{projectPins: map[string]bool{}}
	if err := db.save(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	got, err = db.load(context.Background())
	if err != nil || len(got.projects) != 0 || len(got.pins) != 0 {
		t.Fatalf("empty restore: %+v, %v", got, err)
	}
}

func TestSidebarProjectActions(t *testing.T) {
	a := newApp()
	a.projects = nil
	if len(a.projectDirectories()) != 0 {
		t.Fatal("empty list resurrected a project")
	}
	dir := t.TempDir()
	a.addProject(dir)
	a.addProject(filepath.Join(dir, "."))
	if len(a.projects) != 1 {
		t.Fatalf("duplicate projects: %v", a.projects)
	}
	a.removeProject(dir)
	if len(a.projects) != 0 {
		t.Fatalf("removed project remains: %v", a.projects)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("removing a project touched its folder:", err)
	}
}

func TestSidebarStartupDoesNotRediscoverRemovedFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newApp()
	db, err := a.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), sidebarMetadata{}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b := newApp()
	db, err = b.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.projectDirectories()) != 0 {
		t.Fatalf("removed folders reappeared: %v", b.projects)
	}
}

func TestMetadataSaveRollsBackOnFailure(t *testing.T) {
	db, err := openMetadata(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := sidebarMetadata{projects: []string{"/original"}, pins: []string{"saved"}, projectPins: map[string]bool{}}
	if err := db.save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), sidebarMetadata{projects: []string{"/duplicate", "/duplicate"}}); err == nil {
		t.Fatal("expected duplicate constraint failure")
	}
	got, err := db.load(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("partial save: %+v, %v", got, err)
	}
}

func TestMetadataRejectsFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := openMetadata(path); err == nil {
		db.Close()
		t.Fatal("opened an unsupported database version")
	}
}

func TestMetadataWriterDrainsLatestSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newApp()
	db, err := a.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	a.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	for i := range 100 {
		a.projects = []string{filepath.Join("/work", string(rune('a'+i)))}
		a.saveSidebar()
	}
	want := a.sidebarSnapshot()
	a.closeMetadata()
	db, err = openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.load(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown lost last save: %+v, %v", got, err)
	}
}

func TestSidebarSaveFailureIsVisibleAndStaleResultsIgnored(t *testing.T) {
	a := newApp()
	a.metadataVersion = 2
	a.acceptMetadataWrite(2, errors.New("disk full"))
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Sidebar changes are not saved: disk full") {
		t.Fatal("save failure is hidden")
	}
	a.acceptMetadataWrite(1, nil)
	if a.metadataError == "" {
		t.Fatal("stale save cleared the failure")
	}
	a.acceptMetadataWrite(2, nil)
	if a.metadataError != "" {
		t.Fatal("successful retry left an error")
	}
}

func TestAddProjectWithoutCreatingChat(t *testing.T) {
	a := newApp()
	a.projects = nil
	picked := t.TempDir()
	a.pickFolder = func(done func(path string)) { done(picked) }
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Add Projects"); err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 1 {
		t.Fatal("project was not added")
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("adding a folder created a chat: %+v", job)
	default:
	}
	project := a.projects[0]
	if want, err := filepath.EvalSymlinks(picked); err != nil || project != want {
		t.Fatalf("project %q, want %q (%v)", project, want, err)
	}
	tt.Frame()
	if err := tt.Click("Project options, " + dirBase(project)); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Pin project"); err != nil {
		t.Fatal(err)
	}
	if !a.projectPins[project] {
		t.Fatal("project menu did not pin the folder")
	}
	if err := tt.Click("Project options, " + dirBase(project)); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove project"); err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 0 || a.projectPins[project] {
		t.Fatal("project menu did not remove the folder and its pin")
	}
	a.sessions = []agentSession{{id: "b"}, {id: "a"}}
	a.togglePin(a.sessions[1])
	a.togglePin(a.sessions[0])
	if pins := a.pinnedSessions(); len(pins) != 2 || pins[0].id != "a" {
		t.Fatalf("pin order: %+v", pins)
	}
	a.dropSession("a")
	if a.pinned["a"] {
		t.Fatal("deleted session kept its pin")
	}
}

// TestAddProjectCancelKeepsSidebar: a folder chooser the user dismisses
// changes nothing.
func TestAddProjectCancelKeepsSidebar(t *testing.T) {
	a := newApp()
	a.projects = nil
	a.pickFolder = func(func(path string)) {}
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Add Projects"); err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 0 {
		t.Fatalf("cancel added a project: %v", a.projects)
	}
}

func TestSidebarActionsPersistAcrossAppRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newApp()
	db, err := a.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	a.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	a.projects = nil
	a.addFolder(t.TempDir())
	project := a.projects[0]
	a.toggleProjectPin(project)
	a.togglePin(agentSession{id: "saved-chat"})
	a.togglePin(agentSession{id: "unpin-me"})
	a.togglePin(agentSession{id: "unpin-me"})
	a.closeMetadata()
	b := newApp()
	db, err = b.loadSidebar(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.projects) != 1 || b.projects[0] != project || !b.projectPins[project] || !b.pinned["saved-chat"] || b.pinned["unpin-me"] {
		t.Fatalf("sidebar did not restore actions: %+v", b.sidebarSnapshot())
	}
}

func TestPinSearchResultOutsideLoadedHistory(t *testing.T) {
	a := newApp()
	a.togglePin(agentSession{id: "older-search-result", title: "Older chat"})
	if pins := a.pinnedSessions(); len(pins) != 1 || pins[0].title != "Older chat" {
		t.Fatalf("search result pin disappeared: %+v", pins)
	}
}
