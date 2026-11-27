package pig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"wada-desktop-go/internal/protocol"
)

type fileSnapshot struct {
	Exists bool
	Data   []byte
}

type mutationCapture struct {
	root   string
	id     string
	before map[string]fileSnapshot
	all    bool
	lock   *sync.Mutex
}

func snapshotFile(root, path string) (fileSnapshot, error) {
	full, err := contained(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	info, err := os.Stat(full)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, errors.New("cannot snapshot a non-regular file")
	}
	if info.Size() > 8<<20 {
		return fileSnapshot{}, errors.New("cannot track a file larger than 8 MiB")
	}
	data, err := os.ReadFile(full)
	return fileSnapshot{Exists: true, Data: data}, err
}

func workspaceSnapshot(ctx context.Context, root string) (map[string]fileSnapshot, error) {
	files, err := projectFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	out := map[string]fileSnapshot{}
	var size int
	for _, path := range files {
		snap, err := snapshotFile(root, path)
		if err != nil {
			return nil, fmt.Errorf("could not track %s: %w", path, err)
		}
		size += len(snap.Data)
		if size > 128<<20 {
			return nil, errors.New("the workspace exceeds the 128 MiB session snapshot limit")
		}
		out[path] = snap
	}
	return out, nil
}

func (e *engine) beginMutation(ctx context.Context, c *conversation, id, name string, args json.RawMessage) error {
	c.mu.Lock()
	root := c.rec.Session.Directory
	c.mu.Unlock()
	if name == projectToolName {
		c.turnMu.Lock()
		if c.turn != nil && c.turn.project != nil {
			root = c.turn.project.Directory
		}
		c.turnMu.Unlock()
	}
	lock := e.mutationLock(root)
	for !lock.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.ctx.Done():
			return ErrNotConnected
		case <-time.After(10 * time.Millisecond):
		}
	}
	release := true
	defer func() {
		if release {
			lock.Unlock()
		}
	}()
	before := map[string]fileSnapshot{}
	if singleFileMutation(name) {
		var arg struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &arg); err != nil {
			return err
		}
		path := arg.Path
		if filepath.IsAbs(path) {
			var err error
			path, err = filepath.Rel(root, path)
			if err != nil {
				return err
			}
		}
		snap, err := snapshotFile(root, path)
		if err != nil {
			return err
		}
		before[filepath.ToSlash(path)] = snap
	} else {
		var err error
		before, err = workspaceSnapshot(ctx, root)
		if err != nil {
			return err
		}
	}
	c.captureMu.Lock()
	c.capture = &mutationCapture{root: root, id: id, before: before, all: !singleFileMutation(name), lock: lock}
	c.captureMu.Unlock()
	release = false
	return nil
}

func (e *engine) finishMutation(c *conversation, id string) {
	c.captureMu.Lock()
	capture := c.capture
	if capture == nil || capture.id != id {
		c.captureMu.Unlock()
		return
	}
	c.capture = nil
	c.captureMu.Unlock()
	if capture.lock != nil {
		defer capture.lock.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	root := capture.root
	if root == "" {
		root = c.rec.Session.Directory
	}
	base, err := directory(c.rec.Session.Directory)
	if err != nil {
		c.rec.ChangesError = "Session changes are unavailable: invalid conversation root"
		return
	}
	root, err = directory(root)
	if err != nil {
		c.rec.ChangesError = "Session changes are unavailable: invalid capture root"
		return
	}
	prefix, err := filepath.Rel(base, root)
	if err != nil || !filepath.IsLocal(prefix) {
		c.rec.ChangesError = "Session changes are unavailable: invalid project root"
		return
	}
	paths := map[string]bool{}
	for path := range capture.before {
		paths[path] = true
	}
	if capture.all {
		if files, err := projectFiles(e.ctx, root); err == nil {
			for _, path := range files {
				if _, ok := capture.before[path]; !ok {
					paths[path] = true
				}
			}
		}
	}
	baseMap, err := e.loadBaselines(c.rec.Session.ID)
	if err != nil {
		c.rec.ChangesError = "Session changes could not be read"
	} else {
		updated := false
		for path := range paths {
			before := capture.before[path]
			after, err := snapshotFile(root, path)
			if err != nil {
				c.rec.ChangesError = "Session changes are unavailable: " + err.Error()
				continue
			}
			if before.Exists == after.Exists && bytes.Equal(before.Data, after.Data) {
				continue
			}
			key := filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(path)))
			if _, ok := baseMap[key]; !ok {
				baseMap[key] = before
				updated = true
			}
		}
		if updated {
			if err := e.saveBaselines(c.rec.Session.ID, baseMap); err != nil {
				c.rec.ChangesError = "Session changes could not be saved"
			}
		}
	}
	if err := e.saveLocked(c); err != nil {
		c.rec.ChangesError = "Session changes could not be saved"
	}
}

func (r *Runtime) Changes(ctx context.Context, id string) ([]protocol.FileChange, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	c, err := e.get(id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	root := c.rec.Session.Directory
	trackingError := c.rec.ChangesError
	base, baseErr := e.loadBaselines(id)
	c.mu.Unlock()
	if trackingError != "" {
		return nil, errors.New(trackingError)
	}
	if baseErr != nil {
		return nil, baseErr
	}
	var out []protocol.FileChange
	for path, before := range base {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		after, err := snapshotFile(root, path)
		if err != nil {
			return nil, err
		}
		if before.Exists == after.Exists && bytes.Equal(before.Data, after.Data) {
			continue
		}
		change := protocol.FileChange{Path: path, Status: protocol.FileModified}
		if !before.Exists {
			change.Status = protocol.FileAdded
		} else if !after.Exists {
			change.Status = protocol.FileDeleted
		}
		patch, added, removed, err := fileDiff(ctx, path, before.Data, after.Data)
		if err != nil {
			return nil, err
		}
		change.Patch, change.Added, change.Removed = patch, added, removed
		out = append(out, change)
	}
	slices.SortFunc(out, func(a, b protocol.FileChange) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func fileDiff(ctx context.Context, path string, before, after []byte) (string, int, int, error) {
	dir, err := os.MkdirTemp("", "wada-diff-")
	if err != nil {
		return "", 0, 0, err
	}
	defer os.RemoveAll(dir)
	a, b := filepath.Join(dir, "before"), filepath.Join(dir, "after")
	if err = os.WriteFile(a, before, 0o600); err != nil {
		return "", 0, 0, err
	}
	if err = os.WriteFile(b, after, 0o600); err != nil {
		return "", 0, 0, err
	}
	data, err := exec.CommandContext(ctx, "git", "diff", "--no-index", "--no-ext-diff", "--no-color", "--", a, b).Output()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", 0, 0, err
	}
	patch := string(data)
	patch = strings.ReplaceAll(patch, a, "a/"+path)
	patch = strings.ReplaceAll(patch, b, "b/"+path)
	added, removed := 0, 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removed++
		}
	}
	return patch, added, removed, nil
}

func singleFileMutation(name string) bool {
	return name == "write" || name == "edit"
}
