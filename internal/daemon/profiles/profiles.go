// Package profiles reads agent profiles: named ways to start a coding agent,
// with its harness, its mode and the instructions its first prompt begins
// with.
//
// The user's profiles are in <DataDir>/agents.json; a project's are under
// "agents" in the wada.json of the folder an agent starts in, and take the
// place of the user's of the same name. Both map a name to a profile:
//
//	{"reviewer": {"provider": "claude", "description": "Reviews the diff",
//	  "mode": "ask", "instructions": "Review the uncommitted changes..."}}
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"wada-desktop-go/internal/protocol"
)

// UserFile is the user's profiles in the data directory.
const UserFile = "agents.json"

// projectFile is the file whose "agents" are a project's profiles.
const projectFile = "wada.json"

// Load is the profiles for an agent starting in dir, sorted by name; dir ""
// gives the user's only. A file that doesn't parse is an error naming it.
func Load(dataDir, dir string) ([]protocol.AgentProfile, error) {
	byName := map[string]protocol.AgentProfile{}
	user, err := readFile(filepath.Join(dataDir, UserFile), false)
	if err != nil {
		return nil, err
	}
	for _, p := range user {
		p.Source = protocol.ProfileUser
		byName[p.Name] = p
	}
	if dir != "" {
		project, err := readFile(filepath.Join(dir, projectFile), true)
		if err != nil {
			return nil, err
		}
		for _, p := range project {
			p.Source = protocol.ProfileProject
			byName[p.Name] = p
		}
	}
	out := make([]protocol.AgentProfile, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b protocol.AgentProfile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Find is the named profile for an agent starting in dir.
func Find(dataDir, dir, name string) (protocol.AgentProfile, error) {
	list, err := Load(dataDir, dir)
	if err != nil {
		return protocol.AgentProfile{}, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, nil
		}
	}
	return protocol.AgentProfile{}, fmt.Errorf("no agent profile %q", name)
}

// Prompt is a first prompt as the profile starts it: its instructions, then
// the prompt.
func Prompt(p protocol.AgentProfile, prompt string) string {
	ins := strings.TrimSpace(p.Instructions)
	if ins == "" {
		return prompt
	}
	return ins + "\n\n---\n\n" + prompt
}

type entry struct {
	Description  string `json:"description"`
	Provider     string `json:"provider"`
	Mode         string `json:"mode"`
	Instructions string `json:"instructions"`
}

// readFile reads a name→profile map, at the top of the file or, with nested,
// under its "agents". A missing file has none.
func readFile(path string, nested bool) ([]protocol.AgentProfile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]entry
	if nested {
		var wrap struct {
			Agents map[string]entry `json:"agents"`
		}
		err = json.Unmarshal(data, &wrap)
		m = wrap.Agents
	} else {
		err = json.Unmarshal(data, &m)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]protocol.AgentProfile, 0, len(m))
	for name, e := range m {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("%s: a profile needs a name", path)
		}
		switch e.Mode {
		case "", "ask", "full":
		default:
			return nil, fmt.Errorf("%s: profile %q: mode is \"ask\" or \"full\"", path, name)
		}
		out = append(out, protocol.AgentProfile{Name: name, Description: e.Description,
			Provider: strings.TrimSpace(e.Provider), Mode: e.Mode, Instructions: e.Instructions})
	}
	return out, nil
}
