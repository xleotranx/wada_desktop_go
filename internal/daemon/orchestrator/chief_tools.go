package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

type chiefTool struct {
	name    string
	label   string
	desc    string
	params  map[string]any
	execute func(ctx context.Context, params json.RawMessage) (any, error)
}

func (t *chiefTool) Name() string                        { return t.name }
func (t *chiefTool) Label() string                       { return t.label }
func (t *chiefTool) ExecutionMode() pa.ToolExecutionMode { return pa.ToolModeParallel }
func (t *chiefTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Description: t.desc, Parameters: t.params}
}
func (t *chiefTool) Execute(ctx context.Context, _ string, params json.RawMessage, _ pa.ToolUpdateCallback) (pa.AgentToolResult, error) {
	res, err := t.execute(ctx, params)
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	return pa.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: string(data)}},
	}, nil
}

// ChiefTools constructs PiG's Chief Agent orchestration tools bound to the
// calling conversation's session ID and workspace directory.
func (o *Orchestrator) ChiefTools(getCtx func() (sessionID, dir string)) []pa.AgentTool {
	return []pa.AgentTool{
		&chiefTool{
			name:   "list_providers",
			label:  "List coding agent harnesses",
			desc:   "List available coding-agent providers/harnesses (embedded PiG, Claude Code, Codex, ACP CLIs) and their availability.",
			params: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return o.Harnesses(ctx)
			},
		},
		&chiefTool{
			name:  "create_worktree",
			label: "Create Git worktree",
			desc:  "Create an isolated Git worktree and branch for parallel agent work in the current repository.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":     map[string]any{"type": "string", "description": "Short task/branch slug for the worktree"},
					"base_ref": map[string]any{"type": "string", "description": "Optional git ref/branch to branch from (default HEAD)"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					Name    string `json:"name"`
					BaseRef string `json:"base_ref"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				_, dir := getCtx()
				return o.worktrees.Create(ctx, dir, in.Name, in.BaseRef)
			},
		},
		&chiefTool{
			name:   "list_worktrees",
			label:  "List Git worktrees",
			desc:   "List Git worktrees and dirty status for the current repository.",
			params: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
				_, dir := getCtx()
				return o.worktrees.List(ctx, dir)
			},
		},
		&chiefTool{
			name:  "spawn_agent",
			label: "Spawn external CLI worker",
			desc:  "Spawn a worker session backed by an external harness (e.g. claude, codex, opencode, gemini) in the specified worktree directory.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"provider":      map[string]any{"type": "string"},
					"name":          map[string]any{"type": "string"},
					"worktree_path": map[string]any{"type": "string", "description": "Path to a created Git worktree or directory"},
					"branch":        map[string]any{"type": "string"},
					"mode":          map[string]any{"type": "string", "enum": []string{"ask", "full"}},
				},
				"required":             []string{"provider", "name"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					Provider     string `json:"provider"`
					Name         string `json:"name"`
					WorktreePath string `json:"worktree_path"`
					Branch       string `json:"branch"`
					Mode         string `json:"mode"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID, parentDir := getCtx()
				targetDir := in.WorktreePath
				if targetDir == "" {
					targetDir = parentDir
				}
				return o.spawnWorker(ctx, spawnWorkerArgs{
					ParentSession: parentID,
					Provider:      in.Provider,
					Name:          in.Name,
					Directory:     targetDir,
					Branch:        in.Branch,
					WorktreePath:  in.WorktreePath,
					Mode:          in.Mode,
				})
			},
		},
		&chiefTool{
			name:  "send_agent",
			label: "Send prompt to worker",
			desc:  "Send a prompt/task to a spawned worker agent.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{"type": "string"},
					"prompt":    map[string]any{"type": "string"},
				},
				"required":             []string{"worker_id", "prompt"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID string `json:"worker_id"`
					Prompt   string `json:"prompt"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID, _ := getCtx()
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
		&chiefTool{
			name:  "wait_agent",
			label: "Wait for workers",
			desc:  "Wait until all specified workers finish their active turn (completed, failed, or stopped), or timeout.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"timeout_seconds": map[string]any{"type": "integer"},
				},
				"required":             []string{"worker_ids"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerIDs      []string `json:"worker_ids"`
					TimeoutSeconds int      `json:"timeout_seconds"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID, _ := getCtx()
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
		&chiefTool{
			name:  "inspect_agent_diff",
			label: "Inspect worker Git diff",
			desc:  "Inspect uncommitted file changes produced by a worker in its worktree.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{"type": "string"},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID string `json:"worker_id"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID, _ := getCtx()
				w := o.getWorker(in.WorkerID)
				if w == nil || w.snapshot().ParentSession != parentID {
					return nil, fmt.Errorf("worker %q not found under this session", in.WorkerID)
				}
				return o.Changes(ctx, in.WorkerID)
			},
		},
		&chiefTool{
			name:  "stop_agent",
			label: "Stop worker",
			desc:  "Stop a worker agent session and optionally remove its worktree.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id":       map[string]any{"type": "string"},
					"remove_worktree": map[string]any{"type": "boolean"},
					"force":           map[string]any{"type": "boolean"},
				},
				"required":             []string{"worker_id"},
				"additionalProperties": false,
			},
			execute: func(ctx context.Context, params json.RawMessage) (any, error) {
				var in struct {
					WorkerID       string `json:"worker_id"`
					RemoveWorktree bool   `json:"remove_worktree"`
					Force          bool   `json:"force"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				parentID, parentDir := getCtx()
				w := o.getWorker(in.WorkerID)
				if w == nil || w.snapshot().ParentSession != parentID {
					return nil, fmt.Errorf("worker %q not found under this session", in.WorkerID)
				}
				info := w.snapshot()
				if err := o.Delete(ctx, in.WorkerID); err != nil {
					return nil, err
				}
				if in.RemoveWorktree && info.WorktreePath != "" {
					if err := o.worktrees.Remove(ctx, parentDir, info.WorktreePath, in.Force); err != nil {
						return nil, err
					}
				}
				return map[string]any{"stopped": true, "workerId": in.WorkerID}, nil
			},
		},
	}
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
