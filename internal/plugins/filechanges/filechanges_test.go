package filechanges

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat) {
	t.Helper()
	a := plugintest.NewAgent()
	a.Changed["s1"] = []agent.FileChange{
		{Path: "cmd/main.go", Added: 3, Removed: 1, Patch: "--- a/cmd/main.go\n+++ b/cmd/main.go\n@@ -1,2 +1,4 @@\n package main\n-var x = 1\n+var x = 2\n"},
		{Path: "README.md", Status: agent.FileAdded, Added: 12},
		{Path: "old.go", Status: agent.FileDeleted, Removed: 2},
	}
	chat := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.New(t, a.New, chat.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	h.Flush()
	return h, p, a, chat
}

func panel(p *Plugin) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(268).Children(func() { p.changesView(c) })
	}, 268, 900)
}

func TestOpeningAConversationShowsItsFilesAndDiffs(t *testing.T) {
	h, p, a, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	if a.Called("messages ") != 0 {
		t.Fatal("file changes fetched command history")
	}
	tt := panel(p)
	tt.Frame()
	for _, want := range []string{"3 files", "+15", "−3", "main.go", "cmd", "README.md", "A", "D"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("cmd/main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+var x = 2") || !tt.HasText("@@ -1,2 +1,4 @@") || tt.HasText("+++ b/cmd/main.go") {
		t.Fatalf("diff %q", tt.Texts())
	}
	// Another frame keeps identity and expansion stable.
	tt.Frame()
	if err := tt.Click("cmd/main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("+var x = 2") {
		t.Fatal("diff did not collapse")
	}
	// Expansion belongs to this conversation, even if another has the same path.
	if err := tt.Click("cmd/main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.Changed["s2"] = a.Changed["s1"]
	ctl.Open(agent.Session{ID: "s2"})
	h.Flush()
	tt.Frame()
	if tt.HasText("+var x = 2") {
		t.Fatal("a diff stayed expanded across navigation")
	}
	ctl.NewChat("")
	h.Flush()
	if p.changes != nil || len(p.open) != 0 {
		t.Fatal("file changes outlived their conversation")
	}
	for _, e := range kernel.Items(h, chat.Work) {
		if e.Value.Shown() {
			t.Fatal("new chat shows a file changes section")
		}
	}
}

func TestCollapsingTheFileListKeepsSummaryAndRestoresOpenDiffs(t *testing.T) {
	h, p, _, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if err := tt.Click("cmd/main.go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Collapse file list"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("main.go") || tt.HasText("+var x = 2") || !tt.HasText("3 files") || !tt.HasText("+15") || !tt.HasText("−3") {
		t.Fatalf("collapsed list: %q", tt.Texts())
	}
	// The summary remains focused; Space reopens it without losing a file's state.
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !tt.HasText("main.go") || !tt.HasText("+var x = 2") {
		t.Fatalf("reopened list: %q", tt.Texts())
	}
}

func TestExpandAndCollapseAllDiffsSurviveRefresh(t *testing.T) {
	h, p, a, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if err := tt.Click("Expand all diffs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+var x = 2") || !tt.HasText("No diff to show") {
		t.Fatalf("expanded diffs: %q", tt.Texts())
	}
	// A refreshed result can reorder files and add a file with the same name.
	a.Changed["s1"] = append([]agent.FileChange{{Path: "other/main.go", Patch: "+new file"}}, a.Changed["s1"]...)
	if err := tt.Click("Refresh file changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !tt.HasText("+var x = 2") || tt.HasText("+new file") || !tt.HasText("4 files") {
		t.Fatalf("refreshed diffs: %q", tt.Texts())
	}
	if err := tt.Click("Expand all diffs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+new file") {
		t.Fatalf("new diff: %q", tt.Texts())
	}
	if err := tt.Click("Collapse all diffs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("+var x = 2") || tt.HasText("+new file") || tt.HasText("No diff to show") || !tt.HasText("main.go") {
		t.Fatalf("collapsed diffs: %q", tt.Texts())
	}
}

func TestTurnsRefreshOnlyTheOpenConversationsChanges(t *testing.T) {
	h, p, a, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	reads := a.Called("changes s1")
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "other"}})
	h.Flush()
	if a.Called("changes s1") != reads {
		t.Fatal("an unrelated turn refreshed the changes")
	}
	a.Changed["s1"] = a.Changed["s1"][:1]
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s1"}})
	h.Flush()
	if len(p.changes) != 1 {
		t.Fatalf("changes %+v", p.changes)
	}
}

func TestFailedRefreshKeepsFilesUntilASuccessfulEmptyResult(t *testing.T) {
	h, p, a, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	a.Fail["Changes"] = errors.New("offline")
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnFailed, Session: "s1"}})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if !tt.HasText("Couldn’t load file changes") || !tt.HasText("main.go") || tt.HasText("No file changes") {
		t.Fatalf("failed refresh: %q", tt.Texts())
	}
	delete(a.Fail, "Changes")
	a.Changed["s1"] = nil
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnInterrupted, Session: "s1"}})
	h.Flush()
	tt.Frame()
	if !tt.HasText("No file changes") || tt.HasText("main.go") || tt.HasText("Couldn’t load file changes") {
		t.Fatalf("empty refresh: %q", tt.Texts())
	}
}

func TestChangesAreReadAgainOnceTheBackendConnects(t *testing.T) {
	h, p, a, ctl := start(t)
	a.Fail["Changes"] = errors.New("the agent is not connected")
	ctl.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if !tt.HasText("Couldn’t load file changes") || tt.HasText("No file changes") {
		t.Fatalf("failed initial read: %q", tt.Texts())
	}
	delete(a.Fail, "Changes")
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	tt.Frame()
	if !tt.HasText("main.go") || tt.HasText("Couldn’t load file changes") {
		t.Fatalf("reconnected: %q", tt.Texts())
	}
}

func TestSourceDropdownSwitchesToGitAndRefreshesExternalEdits(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "local.txt", "local change\n")
	h, p, _, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1", Directory: dir})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if err := tt.Click("Change source"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Uncommitted Git changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !tt.HasText("local.txt") || tt.HasText("main.go") {
		t.Fatalf("Git view: %q", tt.Texts())
	}
	writeFile(t, dir, "external.txt", "external change\n")
	if err := tt.Click("Refresh file changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !tt.HasText("external.txt") {
		t.Fatalf("refresh: %q", tt.Texts())
	}
	if err := tt.Click("local.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+local change") {
		t.Fatalf("Git diff: %q", tt.Texts())
	}
	for _, name := range []string{"local.txt", "external.txt"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("Refresh file changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !tt.HasText("No uncommitted changes") || tt.HasText("local.txt") {
		t.Fatalf("clean repository: %q", tt.Texts())
	}
	if err := tt.Click("Change source"); err != nil {
		t.Fatal(err)
	}
	// The source selector also supports native keyboard navigation.
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyEnter)
	h.Flush()
	tt.Frame()
	if !tt.HasText("main.go") || tt.HasText("local.txt") || tt.HasText("+local change") {
		t.Fatalf("session view: %q", tt.Texts())
	}
}

func TestSourceAndFolderChangesDropPendingResults(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "first.txt", "first\n")
	h, p, _, ctl := start(t)
	ctl.Open(agent.Session{ID: "s1", Directory: dir})
	h.RunWork(0) // Session result is posted but has not reached the UI yet.
	tt := panel(p)
	tt.Frame()
	if err := tt.Click("Change source"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Uncommitted Git changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if tt.HasText("main.go") || !tt.HasText("first.txt") {
		t.Fatalf("stale session result: %q", tt.Texts())
	}
	second := repository(t)
	writeFile(t, second, "second.txt", "second\n")
	// Even the same session can receive a corrected folder later.
	conv := ctl.Current()
	conv.Directory = second
	kernel.Emit(h, chat.Changed, conv)
	h.Flush()
	tt.Frame()
	if tt.HasText("first.txt") || !tt.HasText("second.txt") {
		t.Fatalf("stale folder result: %q", tt.Texts())
	}
}

type serviceOnly struct{ backend *plugintest.Agent }

func (*serviceOnly) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "service-only", Provides: []kernel.Named{agent.ServiceKey}}
}
func (s *serviceOnly) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Service](c, agent.ServiceKey, s.backend)
}

func TestGitIsAvailableWithoutBackendSessionDiffs(t *testing.T) {
	dir := repository(t)
	writeFile(t, dir, "local.txt", "local\n")
	ctl := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.New(t,
		func() kernel.Plugin { return &serviceOnly{backend: plugintest.NewAgent()} },
		ctl.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	ctl.Open(agent.Session{ID: "s1", Directory: dir})
	h.Flush()
	tt := panel(p)
	tt.Frame()
	if !tt.HasText("Session changes aren’t available with this backend") {
		t.Fatalf("unavailable: %q", tt.Texts())
	}
	if err := tt.Click("Change source"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Uncommitted Git changes"); err != nil {
		t.Fatal(err)
	}
	h.Flush()
	tt.Frame()
	if !tt.HasText("local.txt") || tt.HasText("Session changes aren’t available with this backend") {
		t.Fatalf("Git unavailable: %q", tt.Texts())
	}
}
