package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"wada-desktop-go/internal/protocol"
)

const authCacheTTL = 5 * time.Minute

type cmdRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// Forge integrates Git worktrees with the local authenticated `gh` CLI while
// strictly refusing to probe remotes for unauthenticated or unknown hosts.
type Forge struct {
	run cmdRunner

	mu        sync.Mutex
	authCache map[string]authEntry
}

type authEntry struct {
	ok      bool
	checked time.Time
}

func newForge() *Forge {
	return &Forge{
		run:       defaultCmdRunner,
		authCache: map[string]authEntry{},
	}
}

func defaultCmdRunner(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, msg)
		}
		return "", err
	}
	return out.String(), nil
}

// PRStatus looks up the open/merged PR for dir's current branch via `gh`, or
// returns (nil, nil) if origin is missing, unauthenticated, or has no PR.
func (m *Manager) PRStatus(ctx context.Context, dir string) (*protocol.PRStatus, error) {
	return m.forgeOrDefault().PRStatus(ctx, dir)
}

// CreatePR creates a GitHub pull request for dir's current branch via `gh`.
func (m *Manager) CreatePR(ctx context.Context, req protocol.WorktreeCreatePRRequest) (*protocol.PRStatus, error) {
	return m.forgeOrDefault().CreatePR(ctx, req)
}

func (m *Manager) forgeOrDefault() *Forge {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forge == nil {
		m.forge = newForge()
	}
	return m.forge
}

func (f *Forge) PRStatus(ctx context.Context, dir string) (*protocol.PRStatus, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	host, err := f.originHost(ctx, dir)
	if err != nil || host == "" || !f.isHostAuthenticated(ctx, host) {
		return nil, nil
	}
	raw, err := f.run(ctx, dir, "gh", "pr", "view", "--json", "number,title,state,isDraft,url,reviewDecision,statusCheckRollup")
	if err != nil {
		// Most branches simply have no PR yet; keep status clean unless cancelled.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	return parseGHPRJSON([]byte(raw))
}

func (f *Forge) CreatePR(ctx context.Context, req protocol.WorktreeCreatePRRequest) (*protocol.PRStatus, error) {
	if strings.TrimSpace(req.Directory) == "" {
		return nil, errors.New("forge: directory is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, errors.New("forge: title is required")
	}
	host, err := f.originHost(ctx, req.Directory)
	if err != nil || host == "" {
		return nil, errors.New("forge: git origin remote is missing or unsupported")
	}
	if !f.isHostAuthenticated(ctx, host) {
		return nil, fmt.Errorf("forge: gh is not authenticated for host %s", host)
	}

	// gh cannot create a PR for a branch the remote doesn't have, and won't
	// offer to push it with prompts disabled.
	raw, err := f.run(ctx, req.Directory, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(raw)
	if branch == "" || branch == "HEAD" {
		return nil, errors.New("forge: the worktree is not on a branch")
	}
	if _, err := f.run(ctx, req.Directory, "git", "push", "--set-upstream", "origin", branch); err != nil {
		return nil, err
	}

	args := []string{"pr", "create", "--head", branch, "--title", req.Title, "--body", req.Body}
	if req.Base != "" {
		args = append(args, "--base", req.Base)
	}
	if req.Draft {
		args = append(args, "--draft")
	}
	if _, err := f.run(ctx, req.Directory, "gh", args...); err != nil {
		return nil, err
	}
	return f.PRStatus(ctx, req.Directory)
}

func (f *Forge) originHost(ctx context.Context, dir string) (string, error) {
	raw, err := f.run(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return parseRemoteHost(strings.TrimSpace(raw)), nil
}

func (f *Forge) isHostAuthenticated(ctx context.Context, host string) bool {
	f.mu.Lock()
	if ent, ok := f.authCache[host]; ok && time.Since(ent.checked) < authCacheTTL {
		f.mu.Unlock()
		return ent.ok
	}
	f.mu.Unlock()

	_, err := f.run(ctx, ".", "gh", "auth", "status", "--hostname", host)
	authed := err == nil

	f.mu.Lock()
	f.authCache[host] = authEntry{ok: authed, checked: time.Now()}
	f.mu.Unlock()
	return authed
}

func parseRemoteHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	// SCP-like SSH syntax: git@github.com:org/repo.git
	if at := strings.IndexByte(raw, '@'); at >= 0 {
		rest := raw[at+1:]
		if colon := strings.IndexByte(rest, ':'); colon > 0 {
			host := rest[:colon]
			if _, err := strconv.Atoi(host); err != nil {
				return strings.ToLower(host)
			}
		}
	}
	return ""
}

type ghPRView struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	URL            string `json:"url"`
	ReviewDecision string `json:"reviewDecision"`
	StatusRollup   []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

func parseGHPRJSON(raw []byte) (*protocol.PRStatus, error) {
	var parsed ghPRView
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Number == 0 {
		return nil, nil
	}
	return &protocol.PRStatus{
		Number:         parsed.Number,
		Title:          parsed.Title,
		State:          strings.ToUpper(parsed.State),
		Draft:          parsed.IsDraft,
		URL:            parsed.URL,
		Checks:         aggregateChecks(parsed.StatusRollup),
		ReviewDecision: parsed.ReviewDecision,
	}, nil
}

func aggregateChecks(rollup []struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}) string {
	if len(rollup) == 0 {
		return "none"
	}
	pending := false
	for _, c := range rollup {
		conc := strings.ToUpper(strings.TrimSpace(c.Conclusion))
		st := strings.ToUpper(strings.TrimSpace(c.Status))
		state := strings.ToUpper(strings.TrimSpace(c.State))
		switch {
		case conc == "FAILURE" || conc == "TIMED_OUT" || conc == "CANCELLED" || conc == "ACTION_REQUIRED" || state == "FAILURE" || state == "ERROR":
			return "fail"
		case st != "" && st != "COMPLETED":
			pending = true
		case state == "PENDING" || state == "EXPECTED":
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}
