package main

import (
	"context"
	"fmt"
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
	a.scope().sessions = []agentSession{{id: "old-chat", dir: a.home}}
	tt := ui.NewTester(a.view, 1400, 920)
	label := "New chat in " + dirBase(dir)
	if tt.HasText(label) {
		t.Fatal("quick action visible before hover")
	}
	box, ok := tt.Find("Hide chats in " + dirBase(dir))
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
	picked := t.TempDir()
	a.pickFolder = func(done func(path string)) { done(picked) }
	if err := tt.Click("New project…"); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(picked)
	if err != nil {
		t.Fatal(err)
	}
	if a.draftProject != want || a.draft != "keep my draft" {
		t.Fatalf("adding a project did not preserve and configure the draft, %q %q", a.draftProject, a.draft)
	}
	if a.projectPickerOpen {
		t.Fatal("the project picker stayed open")
	}
	if got := a.projects[len(a.projects)-1]; got != want {
		t.Fatalf("new project landed as %q, want %q", got, want)
	}
}

// TestProjectChatsNestUnderFolder keeps every chat that works in a
// project's folder nested under it, openable, and folded away with the
// folder.
func TestProjectChatsNestUnderFolder(t *testing.T) {
	a := newApp()
	picked := t.TempDir()
	a.addProject(picked)
	dir := canonDir(picked)
	other := canonDir(t.TempDir())
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{
		{id: "ses_here", title: "Folder wiring", dir: dir},
		{id: "ses_loose", title: "Loose wiring", dir: other},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Folder wiring") {
		t.Fatalf("project chat not nested under its folder: %v", tt.Texts())
	}
	if tt.HasText("Loose wiring") {
		t.Fatalf("a chat outside every project showed in the sidebar: %v", tt.Texts())
	}

	// Choosing a nested chat opens it like any other row.
	if err := tt.Click("Folder wiring"); err != nil {
		t.Fatal(err)
	}
	if a.selectedSession != "ses_here" {
		t.Fatalf("nested chat did not open: %q", a.selectedSession)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "open" || job.sid != "ses_here" {
			t.Fatalf("nested chat job %+v", job)
		}
	default:
		t.Fatal("opening a nested chat did not queue a job")
	}

	// The folder row folds its chats away and brings them back: with the
	// chat folded, only the title strip still carries its name.
	if err := tt.Click("Hide chats in " + dirBase(dir)); err != nil {
		t.Fatal(err)
	}
	if names := countTexts(tt.Texts(), "Folder wiring"); names != 1 {
		t.Fatalf("collapsed folder still lists its chats: %v", tt.Texts())
	}
	if err := tt.Click("Show chats in " + dirBase(dir)); err != nil {
		t.Fatal(err)
	}
	if names := countTexts(tt.Texts(), "Folder wiring"); names <= 1 {
		t.Fatalf("expanded folder lost its chats: %v", tt.Texts())
	}
}

// countTexts is how many of a frame's texts say s, a name a frame may
// carry more than once.
func countTexts(texts []string, s string) int {
	n := 0
	for _, text := range texts {
		if text == s {
			n++
		}
	}
	return n
}

// TestProjectChatsPreviewAndShowMore keeps a busy folder to a few rows
// until the rest is asked for.
func TestProjectChatsPreviewAndShowMore(t *testing.T) {
	a := newApp()
	picked := t.TempDir()
	a.addProject(picked)
	dir := canonDir(picked)
	a.scope().sessionsLoaded = true
	for i := range 7 {
		a.scope().sessions = append(a.scope().sessions, agentSession{id: fmt.Sprint(i),
			title: fmt.Sprintf("Chat %d", i), dir: dir})
	}
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Chat 0") || tt.HasText("Chat 6") {
		t.Fatalf("preview is not the first few chats: %v", tt.Texts())
	}
	if err := tt.Click("Show 2 more chats"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Chat 6") {
		t.Fatalf("more chats did not show: %v", tt.Texts())
	}
	if err := tt.Click("Show fewer chats"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Chat 6") {
		t.Fatal("fewer chats did not fold back")
	}
}

// TestPinnedChatAlsoNests prints a pinned chat under Pinned and under its
// folder without the two rows sharing state.
func TestPinnedChatAlsoNests(t *testing.T) {
	a := newApp()
	picked := t.TempDir()
	a.addProject(picked)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_pin", title: "Pinned wiring", dir: canonDir(picked)}}
	a.togglePin(a.scope().sessions[0])
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Pinned wiring") {
		t.Fatalf("pinned chat missing: %v", tt.Texts())
	}

	// Folding the folder takes the nested row away; the pinned one stays.
	if err := tt.Click("Hide chats in " + dirBase(picked)); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Pinned wiring") {
		t.Fatalf("folding the folder took the pinned row too: %v", tt.Texts())
	}

	// Unpinning leaves the chat only under its folder, still folded away.
	if err := tt.Click("Conversation options, Pinned wiring"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Unpin from Pinned"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Pinned wiring") {
		t.Fatalf("unpinned chat still shows with its folder folded: %v", tt.Texts())
	}
	if err := tt.Click("Show chats in " + dirBase(picked)); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Pinned wiring") {
		t.Fatalf("the chat left the folder with the pin: %v", tt.Texts())
	}
}

// TestProjectRowKeepsItsColumns keeps folder names in one column,
// whether or not a folder shows the chevron of its chats.
func TestProjectRowKeepsItsColumns(t *testing.T) {
	a := newApp()
	empty := t.TempDir()
	busy := t.TempDir()
	a.addProject(empty)
	a.addProject(busy)
	a.scope().sessions = []agentSession{{id: "ses_b", title: "Busy wiring", dir: canonDir(busy)}}
	tt := ui.NewTester(a.view, 1400, 920)
	emptyRow, ok := tt.Find(dirBase(empty))
	if !ok {
		t.Fatalf("empty project row missing: %v", tt.Texts())
	}
	busyRow, ok := tt.Find(dirBase(busy))
	if !ok {
		t.Fatalf("busy project row missing: %v", tt.Texts())
	}
	if emptyRow.X != busyRow.X {
		t.Fatalf("folder names at x %v and %v", emptyRow.X, busyRow.X)
	}
}

func TestProjectChatsMatchFolder(t *testing.T) {
	a := newApp()
	a.scope().sessions = []agentSession{
		{id: "a", dir: "/work/app/."},
		{id: "b", dir: "/work/app"},
		{id: "c", dir: "/work/other"},
		{id: "d"},
	}
	chats := a.projectChats("/work/app")
	if len(chats) != 2 || chats[0].id != "a" || chats[1].id != "b" {
		t.Fatalf("folder chats: %+v", chats)
	}
	if len(a.projectChats("/nowhere")) != 0 || len(a.projectChats("")) != 0 {
		t.Fatal("folder matching is too loose")
	}
}

func TestLegacyImportedProjectsAreRemovedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.save(context.Background(), appMetadata{projects: []string{"/imported"}, pins: map[string][]string{localServerID: {"keep-chat"}}, projectPins: map[string]bool{}, modelOff: map[string]bool{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), "PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a := newApp()
	db, err = a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 0 || !a.pinned["keep-chat"] {
		t.Fatal("migration must clear project imports while keeping chat pins")
	}
	a.addFolder(t.TempDir())
	if err := db.save(context.Background(), a.stateSnapshot()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b := newApp()
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(b.projects) != 1 {
		t.Fatal("explicit project removed on next restart")
	}
}

// TestRemoteServerListsItsProjects shows a remote server's folders as the
// picker's and the sidebar's projects, with nothing of this machine to
// add, while the local server keeps composing its list from the folders
// the user adds.
func TestRemoteServerListsItsProjects(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.activeServer = "srv-box"
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_remote", title: "Remote wiring", dir: "/srv/app"}}
	a.scope().projects = []string{"/srv/app", "/srv/docs"}
	a.projectPickerOpen = true
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Use project app") || !tt.HasText("Use project docs") {
		t.Fatalf("the server's projects are not all offered: %v", tt.Texts())
	}
	if tt.HasText("New project…") {
		t.Fatal("a remote server offered to browse this machine's folders")
	}
	if !tt.HasText("Remote wiring") {
		t.Fatalf("the server chat is not nested under its folder: %v", tt.Texts())
	}

	// The server's list dropped a folder its chats still work in: the
	// chat's own folder steps up as the row, here and in the picker.
	a.scope().projects = nil
	a.projectFilter = "app"
	tt.Frame()
	if !tt.HasText("Use project app") {
		t.Fatalf("the chat's folder did not offer itself: %v", tt.Texts())
	}
	if !tt.HasText("Remote wiring") {
		t.Fatalf("the chat lost its row: %v", tt.Texts())
	}

	// Before its first dial lands, the server has nothing to list.
	a.scope().sessions = nil
	a.projectFilter = ""
	tt.Frame()
	if !tt.HasText("No projects match") {
		t.Fatalf("the empty picker read wrong: %v", tt.Texts())
	}
	if !tt.HasText("The server lists its projects here") {
		t.Fatalf("the projects section read wrong: %v", tt.Texts())
	}

	// The local server composes its list out of added folders.
	a.activeServer = localServerID
	a.projectFilter = ""
	tt.Frame()
	if !tt.HasText("New project…") {
		t.Fatalf("the local server lost the way to add a folder: %v", tt.Texts())
	}
}
