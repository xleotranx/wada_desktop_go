package workspaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/protocol"
)

// ConfigFile is the file at a workspace's directory that says how to set it
// up and which services it runs.
const ConfigFile = "wada.json"

// Config is a workspace's wada.json.
type Config struct {
	// Setup runs once in a new worktree, one command after another.
	Setup []string
	// Services are long-running commands, sorted by name; each takes the
	// next port of the workspace's block.
	Services []ServiceDef
}

// ServiceDef is one service of a wada.json.
type ServiceDef struct {
	Name    string
	Command string
}

// ErrUntrusted refuses to run a wada.json the user has not allowed.
var ErrUntrusted = errors.New("workspaces: this wada.json hasn't been trusted to run")

// Loaded is a workspace's wada.json as read now.
type Loaded struct {
	Workspace protocol.WorkspaceInfo
	Root      string // its project's root
	Found     bool
	Hash      string // of the file's bytes
	Trusted   bool   // the project's user allowed this hash
	Err       error  // the file didn't parse
	Config    Config
	PortBase  int // the first of the workspace's ports, when it has services
}

// parseConfig reads a wada.json. "setup" is a command or a list of them;
// "services" maps names to a command or to {"command": ...}.
func parseConfig(data []byte) (Config, error) {
	var raw struct {
		Setup    json.RawMessage            `json:"setup"`
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("wada.json: %w", err)
	}
	var cfg Config
	if len(raw.Setup) > 0 {
		var one string
		if err := json.Unmarshal(raw.Setup, &one); err == nil {
			cfg.Setup = []string{one}
		} else if err := json.Unmarshal(raw.Setup, &cfg.Setup); err != nil {
			return Config{}, errors.New("wada.json: setup is a command or a list of commands")
		}
	}
	cfg.Setup = slices.DeleteFunc(cfg.Setup, func(c string) bool { return strings.TrimSpace(c) == "" })
	for name, v := range raw.Services {
		var def ServiceDef
		var one string
		if err := json.Unmarshal(v, &one); err == nil {
			def.Command = one
		} else {
			var obj struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(v, &obj); err != nil {
				return Config{}, fmt.Errorf("wada.json: service %q is a command or {\"command\": ...}", name)
			}
			def.Command = obj.Command
		}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(def.Command) == "" {
			return Config{}, fmt.Errorf("wada.json: service %q needs a name and a command", name)
		}
		def.Name = name
		cfg.Services = append(cfg.Services, def)
	}
	slices.SortFunc(cfg.Services, func(a, b ServiceDef) int { return strings.Compare(a.Name, b.Name) })
	if len(cfg.Services) > PortBlock {
		return Config{}, fmt.Errorf("wada.json: at most %d services", PortBlock)
	}
	return cfg, nil
}

// LoadConfig reads the wada.json of an active workspace, giving the
// workspace its ports when it has services.
func (s *Service) LoadConfig(ctx context.Context, id string) (Loaded, error) {
	w, ok := s.Workspace(id)
	if !ok || w.Archived != 0 {
		return Loaded{}, ErrNotFound
	}
	p, ok := s.Project(w.Project)
	if !ok {
		return Loaded{}, ErrNotFound
	}
	l := Loaded{Workspace: w, Root: p.Root}
	data, err := os.ReadFile(filepath.Join(w.Directory, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return Loaded{}, err
	}
	sum := sha256.Sum256(data)
	l.Found, l.Hash = true, hex.EncodeToString(sum[:])
	l.Trusted = l.Hash == s.trustedConfig(w.Project)
	l.Config, l.Err = parseConfig(data)
	if l.Err == nil && len(l.Config.Services) > 0 {
		if l.PortBase, err = s.portBase(ctx, id); err != nil {
			return Loaded{}, err
		}
	}
	return l, nil
}

// Trust allows the workspace's wada.json to run in its project, as long as
// it still has the hash the user saw.
func (s *Service) Trust(ctx context.Context, id, hash string) error {
	l, err := s.LoadConfig(ctx, id)
	if err != nil {
		return err
	}
	if !l.Found || l.Hash != hash {
		return errors.New("workspaces: wada.json changed since it was shown; read it again")
	}
	return s.setTrustedConfig(ctx, l.Workspace.Project, hash)
}

// Runnable is the loaded config when it may run: found, parsed and trusted.
func (l Loaded) Runnable() error {
	switch {
	case !l.Found:
		return fmt.Errorf("workspaces: %s has no %s", l.Workspace.Directory, ConfigFile)
	case l.Err != nil:
		return l.Err
	case !l.Trusted:
		return ErrUntrusted
	}
	return nil
}

// SetupCommand is the setup as one shell command that stops at the first
// failure; "" when there is none.
func (l Loaded) SetupCommand() string { return strings.Join(l.Config.Setup, " && ") }

// Service is a service of the config and its port.
func (l Loaded) Service(name string) (ServiceDef, int, bool) {
	for i, def := range l.Config.Services {
		if def.Name == name {
			return def, l.PortBase + i, true
		}
	}
	return ServiceDef{}, 0, false
}

// Env is what setup and services run with: the workspace, its project's
// root and its ports; a service also gets PORT.
func (l Loaded) Env(service string) []string {
	env := []string{"WADA_WORKSPACE=" + l.Workspace.ID, "WADA_PROJECT_ROOT=" + l.Root}
	if l.PortBase != 0 {
		env = append(env, "WADA_PORT="+strconv.Itoa(l.PortBase))
		for i, def := range l.Config.Services {
			env = append(env, "WADA_PORT_"+envName(def.Name)+"="+strconv.Itoa(l.PortBase+i))
		}
	}
	if _, port, ok := l.Service(service); ok && service != "" {
		env = append(env, "PORT="+strconv.Itoa(port))
	}
	return env
}

// envName is a service name as part of a variable name: upper case, with
// anything but letters and digits as underscores.
func envName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, name)
}

// Roles name the terminals setup and services run in.
const SetupRole = "setup"

// ServiceRole is a service's terminal role.
func ServiceRole(name string) string { return "service:" + name }
