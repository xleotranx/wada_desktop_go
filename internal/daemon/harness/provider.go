package harness

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"wada-desktop-go/internal/protocol"
)

// SessionOptions configures a launched external CLI harness session.
type SessionOptions struct {
	SessionID string
	Directory string
	Mode      string
	Command   string
	Args      []string
	Env       []string
	// MCP, when set, is an MCP server the session's agent may call, given to
	// the CLI in its own way.
	MCP *MCPServer
}

// MCPServer is a streamable-HTTP MCP server and the bearer token it takes.
type MCPServer struct {
	Name  string
	URL   string
	Token string
}

// mcpTokenEnv carries the token to CLIs that read it from the environment.
const mcpTokenEnv = "WADA_MCP_TOKEN"

// ErrNotSent reports that Session.Send refused a prompt before any of it
// reached the harness, so sending it again cannot run it twice. Any other Send
// error leaves the outcome unknown.
var ErrNotSent = errors.New("the prompt was not sent")

// abortGrace is how long Abort waits for a harness to end its turn after asking
// it to stop, before it kills the process as a last resort.
var abortGrace = 5 * time.Second

// waitTurnEnd polls active until it reports false, the grace period ends
// (false), or ctx is done.
func waitTurnEnd(ctx context.Context, active func() bool) (bool, error) {
	deadline := time.NewTimer(abortGrace)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for active() {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-tick.C:
		}
	}
	return true, nil
}

// Session is an active conversation backed by an external coding CLI process.
type Session interface {
	Send(ctx context.Context, text string) error
	Abort(ctx context.Context) error
	Reply(ctx context.Context, approvalID string, decision protocol.Decision) error
	Events() <-chan protocol.Event
	Close() error
}

// Provider launches Sessions for one external CLI harness flavor.
type Provider interface {
	Info() protocol.HarnessInfo
	Start(ctx context.Context, opts SessionOptions) (Session, error)
}

// Registry tracks available external CLI harnesses.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates an empty harness Registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// DefaultRegistry returns a Registry populated with built-in adapters
// (Claude Code, Codex, and ACP presets).
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewClaudeProvider(ClaudeConfig{
		ID:          "claude",
		Name:        "Claude Code",
		Binary:      "claude",
		Description: "Anthropic Claude Code CLI (stream-json harness).",
	}))
	r.Register(NewCodexProvider(CodexConfig{
		ID:          "codex",
		Name:        "Codex",
		Binary:      "codex",
		Description: "OpenAI Codex CLI (exec JSON harness).",
	}))
	r.Register(NewACPProvider(ACPConfig{
		ID:          "opencode",
		Name:        "OpenCode (ACP)",
		Binary:      "opencode",
		Args:        []string{"acp"},
		Description: "OpenCode ACP stdio harness.",
	}))
	r.Register(NewACPProvider(ACPConfig{
		ID:          "gemini",
		Name:        "Gemini CLI (ACP)",
		Binary:      "gemini",
		Args:        []string{"--experimental-acp"},
		Description: "Gemini CLI ACP stdio harness.",
	}))
	return r
}

func (r *Registry) Register(p Provider) {
	if p == nil {
		return
	}
	info := p.Info()
	if info.ID == "" {
		return
	}
	r.mu.Lock()
	r.providers[info.ID] = p
	r.mu.Unlock()
}

func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	p, ok := r.providers[id]
	r.mu.RUnlock()
	return p, ok
}

func (r *Registry) List() []protocol.HarnessInfo {
	r.mu.RLock()
	out := make([]protocol.HarnessInfo, 0, len(r.providers))
	for _, p := range r.providers {
		info := p.Info()
		if info.Binary != "" {
			_, err := exec.LookPath(info.Binary)
			info.Available = err == nil
		}
		out = append(out, info)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeToolKind(name string) protocol.ToolKind {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case slices.Contains([]string{"bash", "shell", "execute", "run_command", "command_execution"}, n):
		return protocol.ToolShell
	case slices.Contains([]string{"read", "read_file", "view_file"}, n):
		return protocol.ToolRead
	case slices.Contains([]string{"glob", "grep", "find", "ls", "search", "find_files"}, n):
		return protocol.ToolSearch
	case slices.Contains([]string{"edit", "write", "write_file", "edit_file", "apply_patch", "file_change", "multiedit"}, n):
		return protocol.ToolEdit
	case slices.Contains([]string{"fetch", "webfetch", "web_fetch", "websearch", "web_search"}, n):
		return protocol.ToolFetch
	default:
		return protocol.ToolOther
	}
}
