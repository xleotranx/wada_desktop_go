package desktop

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

// TestProjectFolderActions copies a project's folder and hands it to the
// file manager from its row menu.
func TestProjectFolderActions(t *testing.T) {
	a := newTestApp(t)
	dir := canonDir(filepath.Join(t.TempDir(), "wada"))
	a.addProject(dir)
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Project options, wada"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Copy folder path"); err != nil {
		t.Fatal(err)
	}
	if copied != dir {
		t.Fatalf("copied %q", copied)
	}
	if err := tt.Click("Project options, wada"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Show in Finder"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 1 || !strings.Contains(urls[0], "wada") {
		t.Fatalf("opened %v", urls)
	}
}

// TestWorkspaceChipActions reaches the same folder from the title chip.
func TestWorkspaceChipActions(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "wada")
	a.scope().sessions = []agent.Session{{ID: "ses_a", Title: "Alpha", Directory: dir}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	codingModule(a).VCS = map[string]workspace.VCSInfo{
		vcsKey(a.connections.activeServer, dir): {Provider: "git", Branch: "main"},
	}
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	label := "Workspace wada on main"
	if err := tt.RightClick(label); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Copy workspace path", "Show in Finder", "Review changes"} {
		if !slices.Contains(tt.Menu(), want) {
			t.Fatalf("the chip menu lacks %q: %v", want, tt.Menu())
		}
	}
	if err := tt.ChooseMenuItem("Copy workspace path"); err != nil {
		t.Fatal(err)
	}
	if copied != dir {
		t.Fatalf("copied %q", copied)
	}
	if err := tt.RightClick(label); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Show in Finder"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 1 || !strings.Contains(urls[0], "wada") {
		t.Fatalf("opened %v", urls)
	}
}

// TestWorkspaceChipKeepsRemoteFoldersOnTheServer leaves Show in Finder out
// of a remote workspace but copies its path.
func TestWorkspaceChipKeepsRemoteFoldersOnTheServer(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []agent.Session{{ID: "ses_r", Title: "Remote", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote"
	codingModule(a).VCS = map[string]workspace.VCSInfo{
		vcsKey("srv-box", "/srv/app"): {Provider: "git", Branch: "main"},
	}
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.RightClick("Workspace app on main"); err != nil {
		t.Fatal(err)
	}
	items := tt.Menu()
	if slices.Contains(items, "Show in Finder") {
		t.Fatalf("a remote folder offered Finder: %v", items)
	}
	if err := tt.ChooseMenuItem("Copy workspace path"); err != nil {
		t.Fatal(err)
	}
	if copied != "/srv/app" {
		t.Fatalf("copied %q", copied)
	}
}
