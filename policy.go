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

// watchServerPolicy reads the shell level of one server workspace once,
// over its API: nothing of this machine to watch, so the pill leans ask
// until the server answers, and a fresh ask lands when the chat moves.
func (a *app) watchServerPolicy(dir string) {
	if a.policyDir == dir {
		return
	}
	a.policyDir, a.policyLevel = dir, "ask"
	if a.win == nil { // headless, as tests run the view
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		level, err := remotePermissionLevel(ctx, a.activeServerConn(), dir)
		if err != nil {
			return
		}
		a.policyLevel = level
		return
	}
	cfg := a.activeServerConn()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		level, err := remotePermissionLevel(ctx, cfg, dir)
		land := func() {
			if err != nil {
				return // an unreadable level leans to the safe name
			}
			if a.policyDir == dir {
				a.policyLevel = level
			}
		}
		a.win.Update(land)
	}()
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
	if permissionBashAsks(doc) {
		return "ask", nil
	}
	return "full", nil
}

// permissionBashAsks reads one config for its bash rule: a star line that
// is not "allow" holds the door.
func permissionBashAsks(doc map[string]any) bool {
	perm, _ := doc["permission"].(map[string]any)
	bash, _ := perm["bash"].(map[string]any)
	if bash != nil {
		if star, ok := bash["*"]; ok && star != "allow" {
			return true
		}
	}
	return false
}

// foldPermissionAsk turns the bash gate on or off in one config, touching
// only the rule it owns; it says whether the config came out different.
func foldPermissionAsk(doc map[string]any, ask bool) bool {
	perm, _ := doc["permission"].(map[string]any)
	bash, _ := perm["bash"].(map[string]any)
	if ask {
		if perm == nil {
			perm = map[string]any{}
			doc["permission"] = perm
		}
		if bash == nil {
			bash = map[string]any{}
			perm["bash"] = bash
		}
		if star, has := bash["*"]; has && star == "ask" {
			return false
		}
		bash["*"] = "ask"
		return true
	}
	if bash == nil {
		return false
	}
	if star, has := bash["*"]; !has || star == "" {
		return false
	}
	delete(bash, "*")
	if len(bash) == 0 {
		delete(perm, "bash")
	}
	if len(perm) == 0 {
		delete(doc, "permission")
	}
	return true
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
	if !foldPermissionAsk(doc, ask) {
		return nil // the rule already reads as asked
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pth, append(out, '\n'), 0644)
}

// remotePermissionLevel reads how shell actions run in one workspace of
// a remote server: the config documents it stacks up, and the project's
// own opencode.json among them holds the door like the local file does.
func remotePermissionLevel(ctx context.Context, cfg serverConn, dir string) (string, error) {
	ag, err := standaloneAgent(cfg)
	if err != nil {
		return "", err
	}
	entries, err := ag.listConfig(ctx, dir)
	if err != nil {
		return "", err
	}
	want := filepath.Join(filepath.Clean(dir), "opencode.json")
	for _, entry := range entries {
		if entry.Type != "document" || filepath.Clean(entry.Path) != want {
			continue
		}
		if permissionBashAsks(entry.Info) {
			return "ask", nil
		}
		return "full", nil
	}
	return "full", nil
}

// remoteSetPermissionAsk carries the ask-first rule to one workspace of a
// remote server: the config its documents report, the rule folded in,
// written back through the file API. It names the level the server will
// read out next, or why the write did not go through.
func remoteSetPermissionAsk(ctx context.Context, cfg serverConn, dir string, ask bool) (string, error) {
	ag, err := standaloneAgent(cfg)
	if err != nil {
		return "", err
	}
	entries, err := ag.listConfig(ctx, dir)
	if err != nil {
		return "", err
	}
	want := filepath.Join(filepath.Clean(dir), "opencode.json")
	doc := map[string]any{}
	for _, entry := range entries {
		if entry.Type == "document" && filepath.Clean(entry.Path) == want && entry.Info != nil {
			doc = entry.Info
		}
	}
	if !foldPermissionAsk(doc, ask) {
		// The rule already reads as asked; nothing of the server's files
		// to touch — the level is whatever the workspace says.
		return remotePermissionLevel(ctx, cfg, dir)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := ag.writeWorkspaceFile(ctx, dir, "opencode.json", out); err != nil {
		return "", err
	}
	if ask {
		return "ask", nil
	}
	return "full", nil
}
