package pig

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"wada-desktop-go/internal/contract/agent"
)

func (p *Plugin) Snapshot(ctx context.Context, id agent.SessionID) (agent.Snapshot, error) {
	_, c, release, err := p.acquireConversation(id)
	defer release()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	root := c.rec.Session.Directory
	c.mu.Unlock()
	files, err := workspaceSnapshot(ctx, root)
	if err != nil {
		return nil, err
	}
	out := agent.Snapshot{}
	for path, file := range files {
		if file.Exists {
			out[path] = file.Data
		}
	}
	return out, nil
}

func (p *Plugin) Diff(ctx context.Context, id agent.SessionID, before agent.Snapshot) ([]agent.FileChange, error) {
	after, err := p.Snapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	var names []string
	for path := range paths {
		names = append(names, path)
	}
	slices.Sort(names)
	var changes []agent.FileChange
	for _, path := range names {
		old, existed := before[path]
		now, exists := after[path]
		if existed == exists && bytes.Equal(old, now) {
			continue
		}
		diff, added, removed, err := fileDiff(ctx, path, old, now)
		if err != nil {
			return nil, fmt.Errorf("diff %s: %w", path, err)
		}
		status := agent.FileModified
		if !existed {
			status = agent.FileAdded
		} else if !exists {
			status = agent.FileDeleted
		}
		changes = append(changes, agent.FileChange{Path: path, Status: status, Patch: diff, Added: added, Removed: removed})
	}
	return changes, nil
}
