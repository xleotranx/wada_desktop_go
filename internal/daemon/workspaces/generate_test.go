package workspaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

// fakeModel answers each system prompt with a canned reply and records what
// it was asked.
type fakeModel struct{ asked []string }

func (f *fakeModel) gen(_ context.Context, system, prompt string) (string, error) {
	f.asked = append(f.asked, prompt)
	switch system {
	case nameSystem:
		return "Sure:\n```json\n{\"title\": \"Fix the parser\", \"branch\": \"Fix parser crash\"}\n```", nil
	case commitSystem:
		return "```\nAdd the parser test\n\nCovers the empty input.\n```", nil
	}
	return `{"title": "Fix the parser crash", "body": "Fixes it.\n\n## Changes\n- parser"}`, nil
}

func TestAnUnnamedWorkspaceIsNamedFromItsFirstMessage(t *testing.T) {
	s, root := service(t), repo(t)
	p, _, err := s.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree})
	if err != nil {
		t.Fatal(err)
	}
	named, _ := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree, Slug: "mine"})
	if !s.HasAutoNamed() {
		t.Fatal("an unnamed workspace doesn't wait to be named")
	}
	model := &fakeModel{}
	if err := s.NameFromMessage(t.Context(), named.Directory, "anything", model.gen); err != nil || len(model.asked) != 0 {
		t.Fatalf("a named workspace was renamed: %v %q", err, model.asked)
	}
	if err := s.NameFromMessage(t.Context(), w.Directory, "the parser crashes on empty input", model.gen); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Workspace(w.ID)
	if got.Branch != "wada/fix-parser-crash" || got.Title != "Fix the parser" || s.HasAutoNamed() {
		t.Fatalf("named %q %q, still waiting %v", got.Branch, got.Title, s.HasAutoNamed())
	}
	if !s.git.BranchExists(t.Context(), root, "wada/fix-parser-crash") {
		t.Fatal("the branch wasn't renamed in git")
	}
	if err := s.NameFromMessage(t.Context(), w.Directory, "again", model.gen); err != nil || len(model.asked) != 1 {
		t.Fatalf("named twice: %v %q", err, model.asked)
	}
}

func TestAutomaticNamingPreservesTheOriginalBranchPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, want string
	}{
		{"nested", "team/agents/", "team/agents/fix-parser-crash"},
		{"unprefixed", "", "fix-parser-crash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, root := t.TempDir(), repo(t)
			reg := open(t, data, nil)
			manager, err := worktree.NewAt(t.Context(), filepath.Join(data, "worktrees"), tc.prefix)
			if err != nil {
				t.Fatal(err)
			}
			s := NewService(reg, manager)
			p, _, err := s.AddProject(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(w.Branch, tc.prefix) || strings.Contains(strings.TrimPrefix(w.Branch, tc.prefix), "/") {
				t.Fatalf("unexpected temporary branch %q", w.Branch)
			}
			s = NewService(reg, worktree.New(data))
			if err := s.NameFromMessage(t.Context(), w.Directory, "fix the parser", (&fakeModel{}).gen); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Workspace(w.ID)
			if got.Branch != tc.want || !s.git.BranchExists(t.Context(), root, tc.want) {
				t.Fatalf("branch = %q, want %q in Git and the workspace", got.Branch, tc.want)
			}
		})
	}
}

func TestCommitAndPullRequestDrafts(t *testing.T) {
	s, root := service(t), repo(t)
	p, _, _ := s.AddProject(t.Context(), root)
	w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree, Slug: "x"})
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{}
	if _, err := s.DraftCommit(t.Context(), w.ID, model.gen); err == nil {
		t.Fatal("a clean workspace drafted a commit")
	}
	if _, _, err := s.DraftPR(t.Context(), w.ID, model.gen); err == nil {
		t.Fatal("a workspace without commits drafted a pull request")
	}
	if err := os.WriteFile(filepath.Join(w.WorktreeRoot, "parser_test.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := s.DraftCommit(t.Context(), w.ID, model.gen)
	if err != nil || msg != "Add the parser test\n\nCovers the empty input." || !strings.Contains(model.asked[0], "parser_test.go") {
		t.Fatalf("draft %q %v, asked %q", msg, err, model.asked)
	}
	sha, err := s.Commit(t.Context(), w.ID, msg)
	if err != nil || len(sha) < 7 {
		t.Fatalf("commit %q %v", sha, err)
	}
	title, body, err := s.DraftPR(t.Context(), w.ID, model.gen)
	if err != nil || title != "Fix the parser crash" || !strings.Contains(body, "## Changes") ||
		!strings.Contains(model.asked[1], "Add the parser test") {
		t.Fatalf("pr %q %q %v, asked %q", title, body, err, model.asked)
	}
}
