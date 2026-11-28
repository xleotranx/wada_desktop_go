package worktrees

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

type fixture struct {
	h    *kerneltest.Harness
	p    *Plugin
	chat *plugintest.Chat
	ws   *plugintest.Workspace
	host *kittest.Host
}

func start(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{chat: &plugintest.Chat{}, host: &kittest.Host{},
		ws: &plugintest.Workspace{Dirs: []string{"/w/app"}, Inbox: "/data/inbox", RefList: agent.Refs{
			Default:   "origin/main",
			Branches:  []string{"main", "feature"},
			Remotes:   []string{"origin/main"},
			PRs:       []agent.PullRequest{{Number: 7, Title: "Fix the parser", Branch: "fix"}},
			Worktrees: []agent.Worktree{{Path: "/w/app", Branch: "main", IsMain: true}, {Path: "/w/old", Branch: "old"}},
		}}}
	f.h = kerneltest.New(t, f.chat.New, f.ws.New, f.host.New, kerneltest.Keep(&f.p, func() *Plugin { return &Plugin{} }))
	f.h.Flush()
	return f
}

func TestNewWorkspaceBranchesOffTheDefault(t *testing.T) {
	f := start(t)
	f.chat.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	f.p.openNew()
	f.h.Flush()
	if f.p.project != "prj:/w/app" || f.p.start != "ref:origin/main" {
		t.Fatalf("dialog opened on %q from %q", f.p.project, f.p.start)
	}
	tt := ui.NewTester(f.p.dialogs, 600, 600)
	tt.Frame()
	for _, want := range []string{"New workspace", "app", "origin/main", "Branch off", "Create"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from %q", want, tt.Texts())
		}
	}
	f.p.name = "parser fix"
	f.p.create()
	f.h.Flush()
	want := agent.CreateWorkspace{Project: "prj:/w/app", Isolation: agent.IsolateWorktree, Mode: agent.BranchOff,
		From: "origin/main", Slug: "parser fix", Title: "parser fix"}
	if len(f.ws.Made) != 1 || f.ws.Made[0] != want {
		t.Fatalf("made %+v, want %+v", f.ws.Made, want)
	}
	if f.p.newOn || f.chat.Conv.Directory != "/w/app-parser fix" || f.chat.Conv.Session != "" {
		t.Fatalf("after creating: dialog %v, chat %+v", f.p.newOn, f.chat.Conv)
	}
}

func TestNewWorkspaceChecksOutABranchAPullRequestOrAWorktree(t *testing.T) {
	f := start(t)
	f.p.openNew()
	f.h.Flush()
	cases := []struct {
		set  func()
		want agent.CreateWorkspace
	}{
		{func() { f.p.start, f.p.checkout = "ref:feature", true },
			agent.CreateWorkspace{Isolation: agent.IsolateWorktree, Mode: agent.CheckoutBranch, From: "feature"}},
		{func() { f.p.start = "pr:7" },
			agent.CreateWorkspace{Isolation: agent.IsolateWorktree, Mode: agent.CheckoutPR, PR: 7}},
		{func() { f.p.existing, f.p.path = true, "/w/old" },
			agent.CreateWorkspace{Isolation: agent.IsolateExisting, Path: "/w/old"}},
	}
	for i, tc := range cases {
		tc.set()
		got, err := f.p.request()
		tc.want.Project = "prj:/w/app"
		if err != nil || got != tc.want {
			t.Fatalf("case %d: %+v %v, want %+v", i, got, err, tc.want)
		}
	}
	f.p.path = ""
	if _, err := f.p.request(); err == nil {
		t.Fatal("an existing worktree was asked for without one")
	}
	// The dialog offers only the worktrees that are no workspace yet.
	tt := ui.NewTester(f.p.dialogs, 600, 600)
	tt.Frame()
	if err := tt.Click("Choose a worktree"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("old") || slices.Contains(tt.Texts(), "main") {
		t.Fatalf("worktrees offered: %q", tt.Texts())
	}
}

func TestAFailedCreationStaysInTheDialog(t *testing.T) {
	f := start(t)
	f.ws.CreateErr = errors.New("the branch exists")
	f.p.openNew()
	f.h.Flush()
	f.p.create()
	f.h.Flush()
	if !f.p.newOn || f.p.problem != "the branch exists" || f.chat.Conv.Directory != "" {
		t.Fatalf("dialog %v problem %q chat %+v", f.p.newOn, f.p.problem, f.chat.Conv)
	}
}

func TestArchiveAsksBeforeDiscardingWork(t *testing.T) {
	f := start(t)
	w := agent.Workspace{ID: "wks:/w/app-x", Project: "prj:/w/app", Isolation: agent.IsolateWorktree,
		Directory: "/w/app-x", Branch: "wada/x", BaseRef: "origin/main"}
	f.ws.Extra = []agent.Workspace{w}
	f.chat.Open(agent.Session{ID: "s1", Directory: w.Directory})
	f.h.Flush()

	st := ui.NewTester(f.p.section, 320, 300)
	st.Frame()
	for _, want := range []string{"wada/x", "from origin/main", "Archive workspace…"} {
		if !st.HasText(want) {
			t.Fatalf("%q is missing from the section: %q", want, st.Texts())
		}
	}

	f.ws.ArchiveErr = fmt.Errorf("%w: the worktree has 2 uncommitted files", agent.ErrWorkUnsaved)
	f.p.askArchive(w)
	f.p.archive(false)
	f.h.Flush()
	tt := ui.NewTester(f.p.dialogs, 600, 400)
	tt.Frame()
	if !tt.HasText("Discard and archive") || !f.p.archiveOn {
		t.Fatalf("the refusal didn't ask to discard: %q", tt.Texts())
	}
	f.p.archive(true)
	f.h.Flush()
	if !slices.Equal(f.ws.Dropped, []string{"wks:/w/app-x", "wks:/w/app-x force"}) || f.p.archiveOn {
		t.Fatalf("archive calls %q, dialog %v", f.ws.Dropped, f.p.archiveOn)
	}
	if _, ok := f.p.current(); ok {
		t.Fatal("the archived workspace is still the current one")
	}
}

func TestAProjectDirectoryHasNoWorkspaceSection(t *testing.T) {
	f := start(t)
	f.chat.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	if _, ok := f.p.current(); ok {
		t.Fatal("a project's own directory shows the Workspace section")
	}
}

func TestRestoreListsArchivedWorkspacesAndOpensAChat(t *testing.T) {
	f := start(t)
	w := agent.Workspace{ID: "wks:/w/app-x", Project: "prj:/w/app", Isolation: agent.IsolateWorktree,
		Directory: "/w/app-x", Branch: "wada/x", Archived: 5}
	f.ws.Gone = []agent.Workspace{w}
	f.ws.RestoreErr = errors.New("the branch is gone")
	f.p.openRestore()
	f.h.Flush()
	tt := ui.NewTester(f.p.dialogs, 600, 500)
	tt.Frame()
	for _, want := range []string{"Restore workspace", "wada/x", "app · /w/app-x", "Restore"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from %q", want, tt.Texts())
		}
	}

	// A failure stays in the dialog.
	f.p.restore(w.ID)
	f.h.Flush()
	if !f.p.restoreOn || f.p.restoreErr != "the branch is gone" || f.chat.Conv.Directory != "" {
		t.Fatalf("dialog %v problem %q chat %+v", f.p.restoreOn, f.p.restoreErr, f.chat.Conv)
	}

	f.ws.RestoreErr = nil
	f.p.restore(w.ID)
	f.h.Flush()
	if f.p.restoreOn || f.chat.Conv.Directory != "/w/app-x" || len(f.ws.Gone) != 0 {
		t.Fatalf("after restoring: dialog %v, chat %+v, archived %+v", f.p.restoreOn, f.chat.Conv, f.ws.Gone)
	}
	if got, ok := f.ws.At("/w/app-x"); !ok || got.ID != w.ID {
		t.Fatalf("the restored workspace isn't active: %+v %v", got, ok)
	}
}

func TestRestoreSaysWhenNothingIsArchived(t *testing.T) {
	f := start(t)
	f.p.openRestore()
	f.h.Flush()
	tt := ui.NewTester(f.p.dialogs, 600, 400)
	tt.Frame()
	if !tt.HasText("No workspace is archived.") {
		t.Fatalf("empty list: %q", tt.Texts())
	}
}

// startWithRecord adds the backend's record and the tabs to the fixture,
// with a wada.json for the workspace "parser fix" makes.
func startWithRecord(t *testing.T, cfg agent.WorkspaceConfig) (*fixture, *plugintest.Projects, *plugintest.Tabs) {
	t.Helper()
	f := &fixture{chat: &plugintest.Chat{}, host: &kittest.Host{},
		ws: &plugintest.Workspace{Dirs: []string{"/w/app"}, Inbox: "/data/inbox", RefList: agent.Refs{
			Default: "origin/main",
			PRs:     []agent.PullRequest{{Number: 9, Title: "From a fork", Branch: "patch", Fork: true}},
		}}}
	rec := &plugintest.Projects{Configs: map[agent.WorkspaceID]agent.WorkspaceConfig{"wks:/w/app-x": cfg, "wks:/w/app": cfg}}
	strip := &plugintest.Tabs{}
	f.h = kerneltest.New(t, f.chat.New, f.ws.New, f.host.New, rec.New, strip.New,
		kerneltest.Keep(&f.p, func() *Plugin { return &Plugin{} }))
	f.h.Flush()
	return f, rec, strip
}

var npmConfig = agent.WorkspaceConfig{Found: true, Hash: "h1", Setup: []string{"npm ci"},
	Services: []agent.WorkspaceService{{Name: "web", Command: "npm run dev", Port: 41000}}}

func (f *fixture) createX(t *testing.T) {
	t.Helper()
	f.p.openNew()
	f.h.Flush()
	f.p.name = "x"
	f.p.create()
	f.h.Flush()
}

func TestATrustedSetupRunsAfterNewWorkspace(t *testing.T) {
	cfg := npmConfig
	cfg.Trusted = true
	f, rec, _ := startWithRecord(t, cfg)
	f.createX(t)
	if !slices.Equal(rec.Ran, []string{"setup wks:/w/app-x"}) || f.p.trustOn {
		t.Fatalf("ran %q, asked %v", rec.Ran, f.p.trustOn)
	}
}

func TestAnUntrustedSetupAsksFirst(t *testing.T) {
	f, rec, _ := startWithRecord(t, npmConfig)
	f.createX(t)
	if len(rec.Ran) != 0 || !f.p.trustOn || f.p.trustThen != "setup" {
		t.Fatalf("ran %q, asked %v for %q", rec.Ran, f.p.trustOn, f.p.trustThen)
	}
	tt := ui.NewTester(f.p.dialogs, 600, 500)
	tt.Frame()
	for _, want := range []string{"Run this workspace’s setup?", "npm ci", "web: npm run dev", "Trust and run"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from %q", want, tt.Texts())
		}
	}
	f.p.trust()
	f.h.Flush()
	if !rec.Configs["wks:/w/app-x"].Trusted || !slices.Equal(rec.Ran, []string{"setup wks:/w/app-x"}) {
		t.Fatalf("trusted %v, ran %q", rec.Configs["wks:/w/app-x"].Trusted, rec.Ran)
	}
}

func TestAForksSetupAlwaysAsks(t *testing.T) {
	cfg := npmConfig
	cfg.Trusted = true
	f, rec, _ := startWithRecord(t, cfg)
	f.p.openNew()
	f.h.Flush()
	f.p.start, f.p.name = "pr:9", "x"
	f.p.create()
	f.h.Flush()
	if len(rec.Ran) != 0 || !f.p.trustOn || !f.p.trustFork {
		t.Fatalf("ran %q, asked %v, fork %v", rec.Ran, f.p.trustOn, f.p.trustFork)
	}
	tt := ui.NewTester(f.p.dialogs, 600, 500)
	tt.Frame()
	if !tt.HasText("This pull request comes from a fork: its wada.json, and the scripts its commands run, come from someone else.") {
		t.Fatalf("no fork warning: %q", tt.Texts())
	}
}

func TestTheSectionRunsServicesInAProjectsOwnWorkspace(t *testing.T) {
	f, rec, strip := startWithRecord(t, npmConfig)
	f.chat.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	f.h.Flush()
	if !f.p.sectionShown() {
		t.Fatal("a project with a wada.json shows no Workspace section")
	}
	st := ui.NewTester(f.p.section, 320, 300)
	st.Frame()
	for _, want := range []string{"wada.json isn’t trusted yet", "Setup not run", "web", ":41000", "Start"} {
		if !st.HasText(want) {
			t.Fatalf("%q is missing from the section: %q", want, st.Texts())
		}
	}
	if st.HasText("Archive workspace…") {
		t.Fatal("a project's own workspace offers to be archived")
	}

	// Starting while untrusted asks, then starts it once trusted.
	w, _ := f.p.here()
	f.p.startService(w, "web")
	f.h.Flush()
	if !f.p.trustOn || f.p.trustThen != "service:web" {
		t.Fatalf("asked %v for %q", f.p.trustOn, f.p.trustThen)
	}
	f.p.trust()
	f.h.Flush()
	if !slices.Equal(rec.Ran, []string{"start wks:/w/app web", "start wks:/w/app web"}) {
		t.Fatalf("ran %q", rec.Ran)
	}
	st.Frame()
	for _, want := range []string{"Open", "Output", "Stop"} {
		if !st.HasText(want) {
			t.Fatalf("%q is missing once running: %q", want, st.Texts())
		}
	}
	if err := st.Click("Show web’s output"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if !slices.Equal(strip.Opened, []string{"terminal.shell term-web /w/app"}) {
		t.Fatalf("opened %q", strip.Opened)
	}
	f.p.stopService(w, "web")
	f.h.Flush()
	if rec.Ran[len(rec.Ran)-1] != "stop wks:/w/app web" {
		t.Fatalf("ran %q", rec.Ran)
	}
}

func TestCommitAndPullRequestUseTheModelsDrafts(t *testing.T) {
	f, rec, _ := startWithRecord(t, agent.WorkspaceConfig{})
	rec.Drafts = map[string]string{"commit": "Fix the parser", "title": "Fix the parser crash", "body": "Fixes it."}
	w := agent.Workspace{ID: "wks:/w/app-x", Project: "prj:/w/app", Isolation: agent.IsolateWorktree,
		Directory: "/w/app-x", Branch: "wada/x", BaseRef: "origin/main"}
	f.ws.Extra = []agent.Workspace{w}
	f.chat.Open(agent.Session{ID: "s1", Directory: w.Directory})
	f.h.Flush()

	st := ui.NewTester(f.p.section, 320, 400)
	st.Frame()
	if err := st.Click("Commit…"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if !f.p.commitOn || f.p.commitMsg != "Fix the parser" {
		t.Fatalf("commit dialog %v with %q", f.p.commitOn, f.p.commitMsg)
	}
	f.p.commitMsg = "Fix the parser\n\nEdited."
	f.p.commit()
	f.h.Flush()
	if !slices.Equal(rec.Commits, []string{"wks:/w/app-x: Fix the parser\n\nEdited."}) || f.p.commitOn {
		t.Fatalf("commits %q, dialog %v", rec.Commits, f.p.commitOn)
	}

	f.p.openPR(w)
	f.h.Flush()
	tt := ui.NewTester(f.p.dialogs, 700, 600)
	tt.Frame()
	for _, want := range []string{"Create a pull request", "Pushes wada/x and opens a pull request into main.", "Create pull request"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from %q", want, tt.Texts())
		}
	}
	f.p.prDraft = true
	f.p.createPR()
	f.h.Flush()
	if !slices.Equal(rec.PRs, []string{"wks:/w/app-x: Fix the parser crash | Fixes it. | draft=true"}) || f.p.prOn {
		t.Fatalf("prs %q, dialog %v", rec.PRs, f.p.prOn)
	}

	// A failed commit stays in the dialog with the reason.
	rec.Fail = map[string]error{"Commit": errors.New("nothing to commit")}
	f.p.openCommit(w)
	f.h.Flush()
	f.p.commit()
	f.h.Flush()
	if !f.p.commitOn || f.p.writeProblem != "nothing to commit" {
		t.Fatalf("dialog %v problem %q", f.p.commitOn, f.p.writeProblem)
	}
}
