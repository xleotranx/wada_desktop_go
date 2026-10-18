package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"wada-desktop-go/internal/opencode"
)

type policyTarget struct {
	dir      string
	revision uint64
}

type policyChange struct {
	revision uint64
	dir      string
	ask      bool
}

type policyResult struct {
	revision   uint64
	dir, level string
	err        error
}

// watchPolicy keeps filesystem work away from rendering. Polling also
// notices edits made outside the app, including a newly created config.
func watchPolicy(ctx context.Context, dirs <-chan policyTarget, writes <-chan policyChange, publish func(policyResult)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var target policyTarget
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
			publish(policyResult{dir: dir, level: level, revision: target.revision})
		}
		lastDir, lastLevel = dir, level
		lastInfo, lastMissing = info, os.IsNotExist(statErr)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case target = <-dirs:
			dir = target.dir
			refresh(true) // a returning workspace has reset its UI cache
		case change := <-writes:
			if err := setPermissionAsk(change.dir, change.ask); err != nil {
				publish(policyResult{dir: change.dir, err: err, revision: change.revision})
			}
			refresh(true)
		case <-tick.C:
			refresh(false)
		}
	}
}

func (a *app) watchWorkspacePolicy(dir string) {
	if a.workspace.policyDir == dir && a.workspace.policyRoute == a.connections.route && a.workspace.policyRemote == !a.localActive() {
		return
	}
	a.workspace.policyRequest.stop()
	a.workspace.policyVersion++
	a.workspace.policyRoute = a.connections.route
	a.workspace.policyRemote = !a.localActive()
	a.workspace.policyDir, a.workspace.policyLevel = dir, "ask"
	select {
	case <-a.workspace.policyDirs:
	default:
	}
	a.workspace.policyDirs <- policyTarget{dir: dir, revision: a.workspace.policyVersion}
}

func (a *app) acceptPolicy(result policyResult) {
	if a.runtime.closed || result.revision != a.workspace.policyVersion || result.dir != a.workspace.policyDir {
		return
	}
	if result.err != nil {
		a.streamLine("The shell policy did not change: " + result.err.Error())
		return
	}
	if result.dir == a.workspace.policyDir {
		a.workspace.policyLevel = result.level
	}
}

// watchServerPolicy reads the shell level of one server workspace once,
// over its API: nothing of this machine to watch, so the pill leans ask
// until the server answers, and a fresh ask lands when the chat moves.
func (a *app) watchServerPolicy(dir string) {
	if a.workspace.policyDir == dir && a.workspace.policyRoute == a.connections.route && a.workspace.policyRemote == !a.localActive() {
		return
	}
	a.workspace.policyRequest.stop()
	a.workspace.policyVersion++
	a.workspace.policyRoute = a.connections.route
	a.workspace.policyRemote = !a.localActive()
	a.workspace.policyDir, a.workspace.policyLevel = dir, "ask"
	select {
	case <-a.workspace.policyDirs:
	default:
	}
	a.workspace.policyDirs <- policyTarget{revision: a.workspace.policyVersion}
	if dir == "" {
		return
	}
	cfg := a.activeServerConn()
	runRequest(a, &a.workspace.policyRequest, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return remotePermissionLevel(ctx, cfg, dir)
	}, func(level string, err error) {
		if err == nil && a.workspace.policyDir == dir {
			a.workspace.policyLevel = level
		}
	})
}

// permissionLevel reads the workspace's json: how bash actions
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
	if doc == nil {
		return "", fmt.Errorf("opencode.json must contain a JSON object")
	}
	return shellGate(doc), nil
}

// shellGate reads one config for how a bare shell command runs there:
// "ask" when the file holds shell commands back, "full" when it does not.
// V2 spells gates as permissions entries and the last match wins; the V1
// bash map an older app wrote reads the same way.
func shellGate(doc map[string]any) string {
	verdict, matched := "full", false
	for _, raw := range permissionRules(doc) {
		action, resource, effect, ok := ruleFields(raw)
		if !ok || resource != "*" || (action != "shell" && action != "*") {
			continue
		}
		matched, verdict = true, "full"
		if effect == "ask" || effect == "deny" {
			verdict = "ask"
		}
	}
	if matched {
		return verdict
	}
	if star, ok := v1BashStar(doc); ok && star != "allow" {
		return "ask"
	}
	return "full"
}

// permissionRules is one config's V2 rules, in the order they claim.
func permissionRules(doc map[string]any) []any {
	rules, _ := doc["permissions"].([]any)
	return rules
}

// ruleFields reads one rule's three strings; what is not a rule reads
// false.
func ruleFields(raw any) (action, resource, effect string, ok bool) {
	rule, _ := raw.(map[string]any)
	if rule == nil {
		return "", "", "", false
	}
	action, _ = rule["action"].(string)
	resource, _ = rule["resource"].(string)
	effect, _ = rule["effect"].(string)
	if action == "" || resource == "" || effect == "" {
		return "", "", "", false
	}
	return action, resource, effect, true
}

// v1BashStar reads the V1 wildcard one config carries, and whether the
// file spells one at all.
func v1BashStar(doc map[string]any) (string, bool) {
	perm, _ := doc["permission"].(map[string]any)
	bash, _ := perm["bash"].(map[string]any)
	star, ok := bash["*"].(string)
	return star, ok
}

// clearV1BashStar takes the V1 wildcard out, the containers it leaves
// empty with it, so a config the app wrote carries no deprecated key of
// its own once the V2 rule has taken over.
func clearV1BashStar(doc map[string]any) {
	perm, _ := doc["permission"].(map[string]any)
	bash, _ := perm["bash"].(map[string]any)
	if bash == nil {
		return
	}
	delete(bash, "*")
	if len(bash) == 0 {
		delete(perm, "bash")
	}
	if len(perm) == 0 {
		delete(doc, "permission")
	}
}

// setShellGate moves one config's shell gate between ask-first and open,
// touching only the rule the app owns: the bare shell ask rule, in the V2
// array it writes now and in the V1 wildcard an older app left behind for
// it to replace. What denies keeps standing — full access stops the
// asking, it does not lift a block. It says whether the config came out
// different.
func setShellGate(doc map[string]any, ask bool) bool {
	rules := permissionRules(doc)
	askCount, askIndex := 0, -1
	kept := make([]any, 0, len(rules)+1)
	if ask {
		// Last-match policies leave specific restrictions and exceptions
		// after the general rule, so the toggle cannot override them.
		kept = append(kept, map[string]any{"action": "shell", "resource": "*", "effect": "ask"})
	}
	for i, raw := range rules {
		if action, resource, effect, ok := ruleFields(raw); ok &&
			action == "shell" && resource == "*" && effect == "ask" {
			askCount++
			askIndex = i
			continue
		}
		kept = append(kept, raw)
	}
	star, spelled := v1BashStar(doc)
	hasV1 := spelled && star == "ask"
	if !hasV1 && askCount == 0 && !ask {
		// Nothing of the app's to take out, and nothing to add: a config
		// another rule holds back is not this toggle's to change.
		return false
	}
	if !hasV1 && ask && askCount == 1 && askIndex == 0 {
		// One leading rule already preserves the user's precedence.
		return false
	}
	if hasV1 {
		clearV1BashStar(doc)
	}
	if len(kept) == 0 {
		delete(doc, "permissions")
	} else {
		doc["permissions"] = kept
	}
	return true
}

// setPermissionAsk writes the ask-first rule for bash into the
// workspace's opencode.json, leaving its other keys alone; the running
// server picks the change up as the file it watches settles.
func setPermissionAsk(dir string, ask bool) error {
	pth := filepath.Join(dir, "opencode.json")
	doc := map[string]any{}
	raw, err := os.ReadFile(pth)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("opencode.json does not read as plain JSON: %w", err)
		}
		if doc == nil {
			return fmt.Errorf("opencode.json must contain a JSON object")
		}
	}
	if !setShellGate(doc, ask) {
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
	ag, err := opencode.NewClient(cfg.transport())
	if err != nil {
		return "", err
	}
	entries, err := ag.ListConfig(ctx, dir)
	if err != nil {
		return "", err
	}
	want := filepath.Join(filepath.Clean(dir), "opencode.json")
	for _, entry := range entries {
		if entry.Type != "document" || filepath.Clean(entry.Path) != want {
			continue
		}
		return shellGate(entry.Info), nil
	}
	return "full", nil
}

// remoteSetPermissionAsk carries the ask-first rule to one workspace of a
// remote server: the config its documents report, the rule folded in,
// written back through the file API. It names the level the server will
// read out next, or why the write did not go through.
func remoteSetPermissionAsk(ctx context.Context, cfg serverConn, dir string, ask bool) (string, error) {
	ag, err := opencode.NewClient(cfg.transport())
	if err != nil {
		return "", err
	}
	entries, err := ag.ListConfig(ctx, dir)
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
	if !setShellGate(doc, ask) {
		// The rule already reads as asked; nothing of the server's files
		// to touch — the level is whatever the workspace says.
		return remotePermissionLevel(ctx, cfg, dir)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := ag.WriteWorkspaceFile(ctx, dir, "opencode.json", out); err != nil {
		return "", err
	}
	return shellGate(doc), nil
}
