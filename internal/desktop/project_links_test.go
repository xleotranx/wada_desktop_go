package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// TestProjectGroupsFolders groups chats from a linked folder under one
// project row and leaves the linked folder out of the project list.
func TestProjectGroupsFolders(t *testing.T) {
	a := newTestApp(t)
	a.connections.activeServer = localServerID
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	linked := filepath.Join(root, "linked")
	for _, dir := range []string{primary, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.preferences.projects = []string{canonDir(primary)}
	a.linkProjectFolder(primary, linked)
	if got := a.projectPrimaryFor(canonDir(linked)); got != canonDir(primary) {
		t.Fatalf("linked folder belongs to %q", got)
	}
	if len(a.projectFolders(primary)) != 2 {
		t.Fatalf("folders %v", a.projectFolders(primary))
	}
	a.scope().sessions = []agent.Session{
		{ID: "ses_a", Title: "Primary chat", Directory: canonDir(primary)},
		{ID: "ses_b", Title: "Linked chat", Directory: canonDir(linked)},
	}
	a.scope().sessionsLoaded = true
	if got := len(a.projectChats(primary)); got != 2 {
		t.Fatalf("project chats %d", got)
	}
	if got := len(a.projectDirectories()); got != 1 {
		t.Fatalf("project rows %v", a.projectDirectories())
	}
	a.addFolder(linked)
	if got := len(a.preferences.projects); got != 1 {
		t.Fatalf("adding a linked folder made %d projects", got)
	}
	a.startChatIn(linked)
	if a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("new chat landed in %q", a.conversation.draftProject)
	}
	if got := len(a.preferences.projects); got != 1 {
		t.Fatalf("starting in a linked folder made %d projects", got)
	}

	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Primary chat") || !tt.HasText("Linked chat") || !tt.HasText("2 folders") {
		t.Fatalf("grouped rows: %v", tt.Texts())
	}
}

// TestProjectFolderMenu starts a chat in a linked folder and unlinks it.
func TestProjectFolderMenu(t *testing.T) {
	a := newTestApp(t)
	a.connections.activeServer = localServerID
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	linked := filepath.Join(root, "linked")
	for _, dir := range []string{primary, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.preferences.projects = []string{canonDir(primary)}
	a.linkProjectFolder(primary, linked)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Project options, primary"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("New chat in linked"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("menu draft in %q", a.conversation.draftProject)
	}
	tt.Frame()
	if err := tt.Click("Project options, primary"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Unlink linked"); err != nil {
		t.Fatal(err)
	}
	if len(a.preferences.projectLinks[canonDir(primary)]) != 0 {
		t.Fatalf("links after unlink: %v", a.preferences.projectLinks)
	}
}

// TestProjectLinkRefusals keeps linked folders out of projects twice and
// cleans links when the project is removed.
func TestProjectLinkRefusals(t *testing.T) {
	a := newTestApp(t)
	a.connections.activeServer = localServerID
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(root, "two")
	for _, dir := range []string{one, two} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.preferences.projects = []string{canonDir(one), canonDir(two)}
	a.linkProjectFolder(one, two)
	if len(a.preferences.projectLinks) != 0 {
		t.Fatalf("linked an existing project: %v", a.preferences.projectLinks)
	}
	a.removeProject(one)
	if _, ok := a.preferences.projectLinks[canonDir(one)]; ok {
		t.Fatal("removing a project kept its links")
	}
}

// TestProjectAddFolderMenu links a folder from the project menu's picker.
func TestProjectAddFolderMenu(t *testing.T) {
	a := newTestApp(t)
	a.connections.activeServer = localServerID
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	linked := filepath.Join(root, "picked")
	for _, dir := range []string{primary, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.preferences.projects = []string{canonDir(primary)}
	a.runtime.pickFolder = func(done func(path string)) { done(linked) }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Project options, primary"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Add folder…"); err != nil {
		t.Fatal(err)
	}
	if got := a.projectPrimaryFor(canonDir(linked)); got != canonDir(primary) {
		t.Fatalf("picked folder belongs to %q", got)
	}
	if len(a.preferences.projects) != 1 {
		t.Fatalf("picking made %d projects", len(a.preferences.projects))
	}
}

// TestProjectPickerOffersLinkedFolders lists a project's linked folders
// under it, starts the draft in the one chosen, and reopens on the draft's
// own folder so Enter keeps the chat where it is.
func TestProjectPickerOffersLinkedFolders(t *testing.T) {
	a := newTestApp(t)
	a.connections.activeServer = localServerID
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	linked := filepath.Join(root, "linked")
	for _, dir := range []string{primary, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.preferences.projects = []string{canonDir(primary)}
	a.linkProjectFolder(primary, linked)
	a.layout.projectCollapsed[canonDir(primary)] = true
	a.openProjectPicker()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Use project primary") || !tt.HasText("Use folder linked in primary") {
		t.Fatalf("picker rows: %v", tt.Texts())
	}
	if !tt.HasText("2 folders") {
		t.Fatalf("the project row misses its folder count: %v", tt.Texts())
	}
	if err := tt.Click("Use folder linked in primary"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("the draft landed in %q", a.conversation.draftProject)
	}
	if a.layout.projectPickerOpen {
		t.Fatal("the picker stayed open after choosing a folder")
	}
	if a.layout.projectCollapsed[canonDir(primary)] {
		t.Fatal("choosing a linked folder left its project folded")
	}

	// The picker opens on the draft's folder; Enter keeps it there.
	a.openProjectPicker()
	tt.Frame()
	choices := a.projectChoices()
	if got := choices[a.layout.projectSelected]; got.dir != canonDir(linked) {
		t.Fatalf("the picker opened on %+v", got)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.layout.projectPickerOpen || a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("Enter moved the draft to %q (open=%v)",
			a.conversation.draftProject, a.layout.projectPickerOpen)
	}

	// A filter naming the linked folder keeps its project row for context;
	// typing starts the selection over at the first match, and the arrows
	// walk it.
	a.openProjectPicker()
	tt.Frame()
	tt.Type("linked")
	tt.Frame()
	if !tt.HasText("Use project primary") || !tt.HasText("Use folder linked in primary") {
		t.Fatalf("filtered rows: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("the keyboard picked %q", a.conversation.draftProject)
	}

	// A filter that matches nothing leaves the picker open, not a crash.
	a.openProjectPicker()
	tt.Frame()
	tt.Type("nothing-here")
	tt.Frame()
	if !tt.HasText("No projects match") {
		t.Fatalf("the empty picker read wrong: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.layout.projectPickerOpen || a.conversation.draftProject != canonDir(linked) {
		t.Fatalf("Enter without matches changed the draft to %q", a.conversation.draftProject)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.layout.projectPickerOpen {
		t.Fatal("Escape did not close the picker while the filter had the focus")
	}
}
