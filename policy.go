package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type policyChange struct {
	dir string
	ask bool
}

type policyResult struct {
	dir, level string
	err        error
}

// watchPolicy keeps filesystem work away from rendering. Polling also
// notices edits made outside the app, including a newly created config.
func watchPolicy(ctx context.Context, dirs <-chan string, writes <-chan policyChange, publish func(policyResult)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var dir, lastDir, lastLevel string
	var lastInfo os.FileInfo
	var lastMissing bool
	refresh := func(force bool) {
		if dir == "" {
			return
		}
		info, statErr := os.Stat(filepath.Join(dir, "opencode.json"))
		unchanged := os.IsNotExist(statErr) && lastMissing
		if statErr == nil && lastInfo != nil {
			unchanged = os.SameFile(info, lastInfo) && info.Size() == lastInfo.Size() &&
				info.ModTime().Equal(lastInfo.ModTime()) && info.Mode() == lastInfo.Mode()
		}
		if !force && dir == lastDir && unchanged {
			return
		}
		level, err := permissionLevel(dir)
		if err != nil {
			level = "ask"
		}
		if force || dir != lastDir || level != lastLevel {
			publish(policyResult{dir: dir, level: level})
		}
		lastDir, lastLevel = dir, level
		lastInfo, lastMissing = info, os.IsNotExist(statErr)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case dir = <-dirs:
			refresh(true) // a returning workspace has reset its UI cache
		case change := <-writes:
			if err := setPermissionAsk(change.dir, change.ask); err != nil {
				publish(policyResult{dir: change.dir, err: err})
			}
			refresh(true)
		case <-tick.C:
			refresh(false)
		}
	}
}

func (a *app) watchWorkspacePolicy(dir string) {
	if a.policyDir == dir {
		return
	}
	a.policyDir, a.policyLevel = dir, "ask"
	select {
	case <-a.policyDirs:
	default:
	}
	a.policyDirs <- dir
}

func (a *app) acceptPolicy(result policyResult) {
	if result.err != nil {
		a.streamLine("The shell policy did not change: " + result.err.Error())
		return
	}
	if result.dir == a.policyDir {
		a.policyLevel = result.level
	}
}

// permissionLevel reads the workspace's opencode.json: how bash actions
// run in it, ask-first or open. A config the decoder cannot read shows
// itself as ask-first, the safe reading.
func permissionLevel(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "full", nil
		}
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("opencode.json does not read as plain JSON: %w", err)
	}
	perm, _ := doc["permission"].(map[string]any)
	bash, _ := perm["bash"].(map[string]any)
	if bash != nil {
		if star, ok := bash["*"]; ok && star != "allow" {
			return "ask", nil
		}
	}
	return "full", nil
}

// setPermissionAsk writes the ask-first rule for bash into the
// workspace's opencode.json, leaving its other keys alone; the running
// server picks the change up as the file it watches settles.
func setPermissionAsk(dir string, ask bool) error {
	pth := filepath.Join(dir, "opencode.json")
	doc := map[string]any{}
	if raw, err := os.ReadFile(pth); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("opencode.json does not read as plain JSON: %w", err)
		}
	}
	perm, _ := doc["permission"].(map[string]any)
	if ask {
		if perm == nil {
			perm = map[string]any{}
			doc["permission"] = perm
		}
		bash, _ := perm["bash"].(map[string]any)
		if bash == nil {
			bash = map[string]any{}
			perm["bash"] = bash
		}
		bash["*"] = "ask"
	} else {
		bash, _ := perm["bash"].(map[string]any)
		if bash == nil {
			return nil // nothing of ours to clear
		}
		delete(bash, "*")
		if len(bash) == 0 {
			delete(perm, "bash")
		}
		if len(perm) == 0 {
			delete(doc, "permission")
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pth, append(out, '\n'), 0644)
}
