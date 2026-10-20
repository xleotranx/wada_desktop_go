package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

func TestMetadataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	want := appMetadata{
		projects:    []string{"/work/b", "/work/a"},
		pins:        map[string][]string{localServerID: {"old", "new"}, "srv-one": {"a"}},
		projectPins: map[string]bool{"/work/a": true},
		servers: []serverConn{
			{id: localServerID, name: "Local server", local: true, managed: true, password: "key-1"},
			{id: "srv-one", name: "Build box", url: "http://10.0.0.12:4096"},
			{id: "srv-two", name: "Home box", url: "https://box.tailnet:4096", password: "token-1",
				paired: true, pairedAt: 1792314306306},
		},
		active:     "srv-one",
		modelOff:   map[string]bool{modelKey(localServerID, "opencode-go", "minimax-m3"): true},
		sidebarW:   412,
		browserW:   536,
		terminalH:  248,
		appearance: appearanceDark,
		chatText:   chatTextLarge,
		keymap:     map[string]chord{"palette": {Mods: ui.Cmd | ui.Alt, Key: ui.KeyK}},
		recent: map[string][]recentFile{
			localServerID: {{Dir: "/work/a", Path: "main.go"}},
		},
		unread: map[string][]string{localServerID: {"ses_1", "ses_2"}},
		drafts: map[string]map[string]string{
			localServerID: {"ses_a": "still typing", "": "a new idea"},
			"srv-one":     {"ses_remote": "remote words"},
		},
		tabs: map[string][]chatTab{
			localServerID: {{id: "ses_a", title: "Alpha"}, {id: "ses_b", title: "Beta"}},
			"srv-one":     {{id: "ses_remote", title: "Remote chat"}},
		},
		activeTabs: map[string]string{localServerID: "ses_b"},
	}
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
	empty := appMetadata{projectPins: map[string]bool{}, pins: map[string][]string{}, modelOff: map[string]bool{}}
	if err := db.save(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	got, err = db.load(context.Background())
	if err != nil || len(got.projects) != 0 || len(got.pins) != 0 || len(got.servers) != 0 ||
		len(got.modelOff) != 0 || got.active != "" {
		t.Fatalf("empty restore: %+v, %v", got, err)
	}
}

func TestSidebarProjectActions(t *testing.T) {
	a := newTestApp(t)
	a.preferences.projects = nil
	if len(a.projectDirectories()) != 0 {
		t.Fatal("empty list resurrected a project")
	}
	dir := t.TempDir()
	a.addProject(dir)
	a.addProject(filepath.Join(dir, "."))
	if len(a.preferences.projects) != 1 {
		t.Fatalf("duplicate projects: %v", a.preferences.projects)
	}
	a.removeProject(dir)
	if len(a.preferences.projects) != 0 {
		t.Fatalf("removed project remains: %v", a.preferences.projects)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("removing a project touched its folder:", err)
	}
}

func TestSidebarStartupDoesNotRediscoverRemovedFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), appMetadata{}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b := newTestApp(t)
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.projectDirectories()) != 0 {
		t.Fatalf("removed folders reappeared: %v", b.preferences.projects)
	}
}

func TestMetadataSaveRollsBackOnFailure(t *testing.T) {
	db, err := openMetadata(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := appMetadata{projects: []string{"/original"}, pins: map[string][]string{localServerID: {"saved"}}, projectPins: map[string]bool{}, modelOff: map[string]bool{}, appearance: appearanceSystem, chatText: chatTextDefault}
	if err := db.save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), appMetadata{projects: []string{"/duplicate", "/duplicate"}}); err == nil {
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
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.preferences.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	for i := range 100 {
		a.preferences.projects = []string{filepath.Join("/work", string(rune('a'+i)))}
		a.saveState()
	}
	want := a.stateSnapshot()
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
	a := newTestApp(t)
	a.preferences.metadataVersion = 2
	a.acceptMetadataWrite(2, errors.New("disk full"))
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Changes are not saved: disk full") {
		t.Fatal("save failure is hidden")
	}
	a.acceptMetadataWrite(1, nil)
	if a.preferences.metadataError == "" {
		t.Fatal("stale save cleared the failure")
	}
	a.acceptMetadataWrite(2, nil)
	if a.preferences.metadataError != "" {
		t.Fatal("successful retry left an error")
	}
}

func TestAddProjectWithoutCreatingChat(t *testing.T) {
	a := newTestApp(t)
	a.preferences.projects = nil
	picked := t.TempDir()
	a.runtime.pickFolder = func(done func(path string)) { done(picked) }
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Add Projects"); err != nil {
		t.Fatal(err)
	}
	if len(a.preferences.projects) != 1 {
		t.Fatal("project was not added")
	}
	select {
	case job := <-commandsOf(a):
		t.Fatalf("adding a folder created a chat: %+v", job)
	default:
	}
	project := a.preferences.projects[0]
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
	if !a.preferences.projectPins[project] {
		t.Fatal("project menu did not pin the folder")
	}
	if err := tt.Click("Project options, " + dirBase(project)); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove project"); err != nil {
		t.Fatal(err)
	}
	if len(a.preferences.projects) != 0 || a.preferences.projectPins[project] {
		t.Fatal("project menu did not remove the folder and its pin")
	}
	a.scope().sessions = []opencode.Session{{ID: "b"}, {ID: "a"}}
	a.togglePin(a.scope().sessions[1])
	a.togglePin(a.scope().sessions[0])
	if pins := a.pinnedSessions(); len(pins) != 2 || pins[0].ID != "a" {
		t.Fatalf("pin order: %+v", pins)
	}
	a.dropSession("a")
	if a.scope().pinned["a"] {
		t.Fatal("deleted session kept its pin")
	}
}

// TestAddProjectCancelKeepsSidebar: a folder chooser the user dismisses
// changes nothing.
func TestAddProjectCancelKeepsSidebar(t *testing.T) {
	a := newTestApp(t)
	a.preferences.projects = nil
	a.runtime.pickFolder = func(func(path string)) {}
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Add Projects"); err != nil {
		t.Fatal(err)
	}
	if len(a.preferences.projects) != 0 {
		t.Fatalf("cancel added a project: %v", a.preferences.projects)
	}
}

func TestSidebarActionsPersistAcrossAppRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.preferences.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	a.preferences.projects = nil
	a.addFolder(t.TempDir())
	project := a.preferences.projects[0]
	a.toggleProjectPin(project)
	a.togglePin(opencode.Session{ID: "saved-chat"})
	a.togglePin(opencode.Session{ID: "unpin-me"})
	a.togglePin(opencode.Session{ID: "unpin-me"})
	a.closeMetadata()
	b := newTestApp(t)
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.preferences.projects) != 1 || b.preferences.projects[0] != project || !b.preferences.projectPins[project] || !b.scope().pinned["saved-chat"] || b.scope().pinned["unpin-me"] {
		t.Fatalf("sidebar did not restore actions: %+v", b.stateSnapshot())
	}
}

func TestPinSearchResultOutsideLoadedHistory(t *testing.T) {
	a := newTestApp(t)
	a.togglePin(opencode.Session{ID: "older-search-result", Title: "Older chat"})
	if pins := a.pinnedSessions(); len(pins) != 1 || pins[0].Title != "Older chat" {
		t.Fatalf("search result pin disappeared: %+v", pins)
	}
}
