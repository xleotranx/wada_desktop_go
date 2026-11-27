package pig

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"wada-desktop-go/internal/protocol"
)

func (r *Runtime) Snapshot(ctx context.Context, id string) (map[string][]byte, error) {
	_, c, release, err := r.acquireConversation(id)
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
	out := map[string][]byte{}
	for path, file := range files {
		if file.Exists {
			out[path] = file.Data
		}
	}
	return out, nil
}

func (r *Runtime) Diff(ctx context.Context, id string, before map[string][]byte) ([]protocol.FileChange, error) {
	after, err := r.Snapshot(ctx, id)
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
	var changes []protocol.FileChange
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
		status := protocol.FileModified
		if !existed {
			status = protocol.FileAdded
		} else if !exists {
			status = protocol.FileDeleted
		}
		changes = append(changes, protocol.FileChange{Path: path, Status: status, Patch: diff, Added: added, Removed: removed})
	}
	return changes, nil
}
