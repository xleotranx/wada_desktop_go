package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/protocol"
)

// ChiefTools constructs PiG's Chief Agent orchestration tools bound to the
// calling conversation's session, workspace directory and mode.
func (o *Orchestrator) ChiefTools(getCtx func() pig.ChiefContext) []pig.Tool {
	return []pig.Tool{
		{
			Name:        "list_providers",
			Label:       "List coding agent harnesses",
			Description: "List available coding-agent providers/harnesses (embedded PiG, Claude Code, Codex, ACP CLIs) and their availability.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			Execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return o.Harnesses(ctx)
			},
		},
		{
			Name:  "create_workspace",
			Label: "Create workspace",
			Description: "Create a workspace for parallel work in this conversation's project: a new Git worktree on its own branch " +
				"(isolation \"worktree\"; mode \"branch-off\" from a ref, \"checkout-branch\" for an existing branch, or " +
				"\"checkout-pr\" for a pull request), or an existing worktree (isolation \"existing\" with its path). " +
				"Returns the workspace, whose id spawn_agent takes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"isolation": map[string]any{"type": "string", "enum": []string{"worktree", "existing"}},
					"mode":      map[string]any{"type": "string", "enum": []string{"branch-off", "checkout-branch", "checkout-pr"}},
					"from":      map[string]any{"type": "string", "description": "branch-off: the ref to start from (default: origin's default branch); checkout-branch: the branch"},
					"pr":        map[string]any{"type": "integer", "description": "checkout-pr: the pull request number"},
					"path":      map[string]any{"type": "string", "description": "existing: the worktree's directory"},
					"slug":      map[string]any{"type": "string", "description": "Short task slug naming the worktree and its new branch"},
					"title":     map[string]any{"type": "string"},
				},
				"required":             []string{"isolation"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					Isolation protocol.Isolation `json:"isolation"`
					Mode      protocol.StartMode `json:"mode"`
					From      string             `json:"from"`
					PR        int                `json:"pr"`
					Path      string             `json:"path"`
					Slug      string             `json:"slug"`
					Title     string             `json:"title"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				here, err := o.workspaceHere(getCtx().Dir)
				if err != nil {
					return nil, err
				}
				return o.workspaces.Create(ctx, protocol.WorkspaceCreateRequest{Project: here.Project, Isolation: in.Isolation,
					Mode: in.Mode, From: in.From, PR: in.PR, Path: in.Path, Slug: in.Slug, Title: in.Title})
			},
		},
		{
			Name:        "list_workspaces",
			Label:       "List workspaces",
			Description: "List the active workspaces of this conversation's project, with their directories, branches and bases.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			Execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
				here, err := o.workspaceHere(getCtx().Dir)
				if err != nil {
					return nil, err
				}
				return o.workspaces.Workspaces(here.Project, false), nil
			},
		},
		{
			Name:  "archive_workspace",
			Label: "Archive workspace",
			Description: "Archive a worktree workspace of this conversation's project, removing a worktree Wada made. One with " +
				"uncommitted or unpushed work is refused unless force, which discards that work.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id": map[string]any{"type": "string"},
					"force":        map[string]any{"type": "boolean"},
				},
				"required":             []string{"workspace_id"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkspaceID string `json:"workspace_id"`
					Force       bool   `json:"force"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				if _, err := o.projectWorkspace(getCtx().Dir, in.WorkspaceID); err != nil {
					return nil, err
				}
				if err := o.workspaces.Archive(ctx, in.WorkspaceID, in.Force); err != nil {
					return nil, err
				}
				return map[string]any{"archived": true, "workspaceId": in.WorkspaceID}, nil
			},
		},
		{
			Name:        "spawn_agent",
			Label:       "Spawn external CLI worker",
			Description: "Spawn a worker session backed by an external harness (e.g. claude, codex, opencode, gemini) in this conversation's folder, a folder inside it, or a workspace of its project (workspace_id, from create_workspace). Mode \"full\" runs without approvals and applies only when this conversation itself works without approvals.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"provider":     map[string]any{"type": "string"},
					"name":         map[string]any{"type": "string"},
					"workspace_id": map[string]any{"type": "string", "description": "A workspace of this conversation's project"},
					"directory":    map[string]any{"type": "string", "description": "A folder inside this conversation's folder"},
					"mode":         map[string]any{"type": "string", "enum": []string{"ask", "full"}},
				},
				"required":             []string{"provider", "name"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					Provider    string `json:"provider"`
					Name        string `json:"name"`
					WorkspaceID string `json:"workspace_id"`
					Directory   string `json:"directory"`
					Mode        string `json:"mode"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				cc := getCtx()
				// A worker never gets more access than the conversation
				// that spawns it: approvals in ask mode stay approvals.
				mode := in.Mode
				if mode == "" || !cc.FullAccess {
					mode = "ask"
				}
				args := spawnWorkerArgs{ParentSession: cc.SessionID, Provider: in.Provider, Name: in.Name,
					Directory: cc.Dir, Mode: mode}
				switch {
				case in.WorkspaceID != "":
					w, err := o.projectWorkspace(cc.Dir, in.WorkspaceID)
					if err != nil {
						return nil, err
					}
					args.Directory, args.Branch, args.WorktreePath = w.Directory, w.Branch, w.WorktreeRoot
				case in.Directory != "":
					dir, err := o.workerDir(cc.Dir, in.Directory)
					if err != nil {
						return nil, err
					}
					args.Directory = dir
				}
				return o.spawnWorker(ctx, args)
			},
		},
		{
			Name:        "send_agent",
			Label:       "Send prompt to worker",
			Description: "Send a prompt/task to a spawned worker agent.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{"type": "string"},
					"prompt":    map[string]any{"type": "string"},
				},
				"required":             []string{"worker_id", "prompt"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID string `json:"worker_id"`
					Prompt   string `json:"prompt"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID := getCtx().SessionID
				w := o.getWorker(in.WorkerID)
				if w == nil || w.snapshot().ParentSession != parentID {
					return nil, fmt.Errorf("worker %q not found under this session", in.WorkerID)
				}
				if _, err := o.Send(ctx, protocol.SendRequest{Session: in.WorkerID, Text: in.Prompt}); err != nil {
					return nil, err
				}
				return map[string]string{"status": "sent", "workerId": in.WorkerID}, nil
			},
		},
		{
			Name:        "wait_agent",
			Label:       "Wait for workers",
			Description: "Wait until all specified workers finish their active turn (completed, failed, or stopped), or timeout.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"timeout_seconds": map[string]any{"type": "integer"},
				},
				"required":             []string{"worker_ids"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerIDs      []string `json:"worker_ids"`
					TimeoutSeconds int      `json:"timeout_seconds"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID := getCtx().SessionID
				timeout := 300 * time.Second
				if in.TimeoutSeconds > 0 {
					timeout = time.Duration(in.TimeoutSeconds) * time.Second
				}
				wctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()

				results := make([]protocol.WorkerInfo, 0, len(in.WorkerIDs))
				for _, wid := range in.WorkerIDs {
					info, err := o.waitOneWorker(wctx, parentID, wid)
					if err != nil {
						return nil, err
					}
					results = append(results, info)
				}
				return results, nil
			},
		},
		{
			Name:        "inspect_agent_diff",
			Label:       "Inspect worker Git diff",
			Description: "Inspect uncommitted file changes produced by a worker in its worktree.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{"type": "string"},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID string `json:"worker_id"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID := getCtx().SessionID
				w := o.getWorker(in.WorkerID)
				if w == nil || w.snapshot().ParentSession != parentID {
					return nil, fmt.Errorf("worker %q not found under this session", in.WorkerID)
				}
				return o.Changes(ctx, in.WorkerID)
			},
		},
		{
			Name:        "stop_agent",
			Label:       "Stop worker",
			Description: "Stop a worker agent session. Its workspace stays; archive_workspace removes it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{"type": "string"},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID string `json:"worker_id"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				w := o.getWorker(in.WorkerID)
				if w == nil || w.snapshot().ParentSession != getCtx().SessionID {
					return nil, fmt.Errorf("worker %q not found under this session", in.WorkerID)
				}
				if err := o.Delete(ctx, in.WorkerID); err != nil {
					return nil, err
				}
				return map[string]any{"stopped": true, "workerId": in.WorkerID}, nil
			},
		},
	}
}

// workerDir resolves a requested worker folder, which must exist and, with
// symlinks resolved, be the conversation's folder or inside it.
func (o *Orchestrator) workerDir(parentDir, requested string) (string, error) {
	dir := requested
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(parentDir, dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("directory %q: %w", requested, err)
	}
	if root, err := filepath.EvalSymlinks(parentDir); err == nil && within(root, real) {
		return real, nil
	}
	return "", fmt.Errorf("directory %q is not inside this conversation's folder; use a workspace", requested)
}

// workspaceHere is the active workspace the conversation in dir works in.
func (o *Orchestrator) workspaceHere(dir string) (protocol.WorkspaceInfo, error) {
	if o.workspaces == nil {
		return protocol.WorkspaceInfo{}, errors.New("this daemon keeps no workspaces")
	}
	w, ok := o.workspaces.Active(dir)
	if !ok {
		return protocol.WorkspaceInfo{}, errors.New("this conversation's folder is no active workspace")
	}
	if p, _ := o.workspaces.Project(w.Project); p.Inbox {
		return protocol.WorkspaceInfo{}, errors.New("a conversation in the Inbox has no project; start it in a project")
	}
	return w, nil
}

// projectWorkspace is an active workspace of the project the conversation in
// dir works in.
func (o *Orchestrator) projectWorkspace(dir, id string) (protocol.WorkspaceInfo, error) {
	here, err := o.workspaceHere(dir)
	if err != nil {
		return protocol.WorkspaceInfo{}, err
	}
	w, ok := o.workspaces.Workspace(id)
	if !ok || w.Archived != 0 || w.Project != here.Project {
		return protocol.WorkspaceInfo{}, fmt.Errorf("workspace %q is no active workspace of this project", id)
	}
	return w, nil
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func isTerminalWorkerStatus(st protocol.WorkerStatus) bool {
	switch st {
	case protocol.WorkerCompleted, protocol.WorkerFailed, protocol.WorkerStopped:
		return true
	case protocol.WorkerIdle, protocol.WorkerRunning, protocol.WorkerWaitingApproval:
		return false
	}
	return false
}

func (o *Orchestrator) waitOneWorker(ctx context.Context, parentID, workerID string) (protocol.WorkerInfo, error) {
	for {
		w := o.getWorker(workerID)
		if w == nil {
			return protocol.WorkerInfo{}, fmt.Errorf("worker %q not found", workerID)
		}
		info := w.snapshot()
		if info.ParentSession != parentID {
			return protocol.WorkerInfo{}, fmt.Errorf("worker %q does not belong to this session", workerID)
		}
		if isTerminalWorkerStatus(info.Status) {
			return info, nil
		}
		waitCh, unsub := o.subscribeWaiter(workerID)
		// Re-check after registering waiter so we never miss a racing completion.
		info = w.snapshot()
		if isTerminalWorkerStatus(info.Status) {
			unsub()
			return info, nil
		}
		select {
		case <-waitCh:
		case <-ctx.Done():
			unsub()
			return protocol.WorkerInfo{}, errors.New("timed out waiting for worker " + workerID)
		}
	}
}
