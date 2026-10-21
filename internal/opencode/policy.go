package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func PermissionLevel(dir string) (string, error) {
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
	return ShellGate(doc), nil
}

// ShellGate reads one config for how a bare shell command runs there:
// "ask" when the file holds shell commands back, "full" when it does not.
// V2 spells gates as permissions entries and the last match wins; the V1
// bash map an older app wrote reads the same way.
func ShellGate(doc map[string]any) string {
	verdict, matched := "full", false
	for _, raw := range PermissionRules(doc) {
		action, resource, effect, ok := RuleFields(raw)
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
	if star, ok := V1BashStar(doc); ok && star != "allow" {
		return "ask"
	}
	return "full"
}

// permissionRules is one config's V2 rules, in the order they claim.
func PermissionRules(doc map[string]any) []any {
	rules, _ := doc["permissions"].([]any)
	return rules
}

// ruleFields reads one rule's three strings; what is not a rule reads
// false.
func RuleFields(raw any) (action, resource, effect string, ok bool) {
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
func V1BashStar(doc map[string]any) (string, bool) {
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

// SetShellGate moves one config's shell gate between ask-first and open,
// touching only the rule the app owns: the bare shell ask rule, in the V2
// array it writes now and in the V1 wildcard an older app left behind for
// it to replace. What denies keeps standing — full access stops the
// asking, it does not lift a block. It says whether the config came out
// different.
func SetShellGate(doc map[string]any, ask bool) bool {
	rules := PermissionRules(doc)
	askCount, askIndex := 0, -1
	kept := make([]any, 0, len(rules)+1)
	if ask {
		// Last-match policies leave specific restrictions and exceptions
		// after the general rule, so the toggle cannot override them.
		kept = append(kept, map[string]any{"action": "shell", "resource": "*", "effect": "ask"})
	}
	for i, raw := range rules {
		if action, resource, effect, ok := RuleFields(raw); ok &&
			action == "shell" && resource == "*" && effect == "ask" {
			askCount++
			askIndex = i
			continue
		}
		kept = append(kept, raw)
	}
	star, spelled := V1BashStar(doc)
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

// SetPermissionAsk writes the ask-first rule for bash into the
// workspace's opencode.json, leaving its other keys alone; the running
// server picks the change up as the file it watches settles.
func SetPermissionAsk(dir string, ask bool) error {
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
	if !SetShellGate(doc, ask) {
		return nil // the rule already reads as asked
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pth, append(out, '\n'), 0644)
}

func remotePermissionLevel(ctx context.Context, client *Client, dir string) (string, error) {
	ag := client
	entries, err := ag.ListConfig(ctx, dir)
	if err != nil {
		return "", err
	}
	want := filepath.Join(filepath.Clean(dir), "opencode.json")
	for _, entry := range entries {
		if entry.Type != "document" || filepath.Clean(entry.Path) != want {
			continue
		}
		return ShellGate(entry.Info), nil
	}
	return "full", nil
}

// remoteSetPermissionAsk carries the ask-first rule to one workspace of a
// remote server: the config its documents report, the rule folded in,
// written back through the file API. It names the level the server will
// read out next, or why the write did not go through.
func remoteSetPermissionAsk(ctx context.Context, client *Client, dir string, ask bool) (string, error) {
	ag := client
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
	if !SetShellGate(doc, ask) {
		// The rule already reads as asked; nothing of the server's files
		// to touch — the level is whatever the workspace says.
		return remotePermissionLevel(ctx, client, dir)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := ag.WriteWorkspaceFile(ctx, dir, "opencode.json", out); err != nil {
		return "", err
	}
	return ShellGate(doc), nil
}

func (a *Client) ReadPolicy(ctx context.Context, dir string) (string, error) {
	if a.Local {
		return PermissionLevel(dir)
	}
	return remotePermissionLevel(ctx, a, dir)
}
func (a *Client) WritePolicy(ctx context.Context, dir string, ask bool) (string, error) {
	if a.Local {
		if err := SetPermissionAsk(dir, ask); err != nil {
			return "", err
		}
		return PermissionLevel(dir)
	}
	return remoteSetPermissionAsk(ctx, a, dir, ask)
}
