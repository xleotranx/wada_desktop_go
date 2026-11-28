package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/daemon/worktree"
	"wada-desktop-go/internal/protocol"
)

// Generator asks a model for a short piece of text.
type Generator func(ctx context.Context, system, prompt string) (string, error)

// promptLimit bounds what a prompt carries of a diff or a message.
const promptLimit = 24000

const nameSystem = `You name a coding task. Reply with JSON only: {"title": "...", "branch": "..."}.
title: at most 6 words, sentence case, no trailing period. branch: 2 to 5 lowercase words joined by dashes.`

const commitSystem = `You write git commit messages. Reply with the message only: a subject line in the
imperative mood of at most 60 characters, a blank line, then a short body that says what changed and why,
wrapped at 72 characters. No code fences, no quotes.`

const prSystem = `You write pull request descriptions. Reply with JSON only: {"title": "...", "body": "..."}.
title: at most 70 characters, imperative mood. body: Markdown with a short summary paragraph, then a
"Changes" list of the notable changes.`

// NameFromMessage names a workspace waiting to be named from the first
// message sent in its directory: its title, and its branch while no remote
// has it. A failure leaves the name as it was; it is not tried again.
func (s *Service) NameFromMessage(ctx context.Context, dir, text string, gen Generator) error {
	w, ok := s.autoNamed(dir)
	if !ok || strings.TrimSpace(text) == "" {
		return nil
	}
	// Whatever happens, the workspace is named once.
	if _, err := s.update(ctx, w.ID, func(r *store.WorkspaceRow) { r.AutoName = false }); err != nil {
		return err
	}
	out, err := gen(ctx, nameSystem, limit(text))
	if err != nil {
		return err
	}
	var name struct {
		Title  string `json:"title"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(jsonObject(out)), &name); err != nil {
		return err
	}
	title := strings.TrimSpace(name.Title)
	branch := w.Branch
	if slug := worktree.SanitizeSlug(name.Branch); slug != "" && strings.HasPrefix(w.Branch, "wada/") {
		want := "wada/" + slug
		for i := 2; s.git.BranchExists(ctx, w.Dir, want) && i < 10; i++ {
			want = "wada/" + slug + "-" + string(rune('0'+i))
		}
		if err := s.git.RenameBranch(ctx, w.Dir, w.Branch, want); err == nil {
			branch = want
		}
	}
	_, err = s.update(ctx, w.ID, func(r *store.WorkspaceRow) {
		r.Branch = branch
		if r.Title == "" {
			r.Title = title
		}
	})
	return err
}

// DraftCommit writes a commit message for everything changed in the
// workspace.
func (s *Service) DraftCommit(ctx context.Context, id string, gen Generator) (string, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return "", ErrNotFound
	}
	changes, err := s.git.WorkingChanges(ctx, w.Directory)
	if err != nil {
		return "", err
	}
	if changes == "" {
		return "", errors.New("workspaces: nothing to commit")
	}
	out, err := gen(ctx, commitSystem, limit(changes))
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "`")), err
}

// Commit commits everything changed in the workspace.
func (s *Service) Commit(ctx context.Context, id, message string) (string, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return "", ErrNotFound
	}
	return s.git.Commit(ctx, w.Directory, message)
}

// DraftPR writes a pull request's title and body for the workspace's
// commits since its base.
func (s *Service) DraftPR(ctx context.Context, id string, gen Generator) (string, string, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return "", "", ErrNotFound
	}
	if w.BaseRef == "" {
		return "", "", errors.New("workspaces: the workspace has no base to compare with")
	}
	history, err := s.git.History(ctx, w.Directory, w.BaseRef)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(strings.ReplaceAll(history, "---", "")) == "" {
		return "", "", errors.New("workspaces: no commits since " + w.BaseRef)
	}
	out, err := gen(ctx, prSystem, limit(history))
	if err != nil {
		return "", "", err
	}
	var pr struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(jsonObject(out)), &pr); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(pr.Title), strings.TrimSpace(pr.Body), nil
}

// CreatePR pushes the workspace's branch, opens a pull request against its
// base and records the pull request's number.
func (s *Service) CreatePR(ctx context.Context, id, title, body string, draft bool) (*protocol.PRStatus, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return nil, ErrNotFound
	}
	base := strings.TrimPrefix(w.BaseRef, "origin/")
	pr, err := s.git.CreatePR(ctx, protocol.WorktreeCreatePRRequest{Directory: w.Directory, Title: title, Body: body,
		Base: base, Draft: draft})
	if err != nil || pr == nil {
		return pr, err
	}
	_, err = s.update(ctx, id, func(r *store.WorkspaceRow) { r.PR = pr.Number })
	return pr, err
}

// limit is the start of s, at most promptLimit bytes.
func limit(s string) string {
	if len(s) <= promptLimit {
		return s
	}
	return s[:promptLimit] + "\n…"
}

// jsonObject is the outermost {...} of a model's reply, which may wrap it
// in prose or a code fence.
func jsonObject(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
