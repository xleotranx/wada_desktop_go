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
