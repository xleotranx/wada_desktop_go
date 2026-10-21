// Package plugintest provides fake plugins for the contracts, so a plugin's
// tests can run it in a kerneltest harness without a backend or a window.
package plugintest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

// Agent provides the service, models, modes and interactions with answers the test sets. Calls are
// recorded as short strings, e.g. "send s1 hello".
type Agent struct {
	mu          sync.Mutex
	calls       []string
	Stored      map[agent.SessionID][]agent.Message
	Pages       map[string]agent.SessionPage // by "cursor|search"
	Fail        map[string]error             // by method name, e.g. "Send"
	State       agent.Status
	Disposition agent.SendDisposition

	// The model capabilities.
	Catalog  []agent.Model
	Default  agent.ModelRef
	ModeList []agent.Mode
}

func NewAgent() *Agent {
	return &Agent{Stored: map[agent.SessionID][]agent.Message{}, Pages: map[string]agent.SessionPage{},
		Fail: map[string]error{}, State: agent.Status{State: agent.Ready, Attempt: 1}}
}

func (*Agent) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-agent", Provides: []kernel.Named{agent.ServiceKey, agent.ModelsKey, agent.ModesKey, agent.InteractionsKey}}
}

func (a *Agent) Setup(c *kernel.Context) error {
	if err := kernel.Provide[agent.Service](c, agent.ServiceKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Models](c, agent.ModelsKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Modes](c, agent.ModesKey, a); err != nil {
		return err
	}
	return kernel.Provide[agent.Interactions](c, agent.InteractionsKey, a)
}

func (a *Agent) record(method, call string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call)
	return a.Fail[method]
}

// Calls returns the calls so far.
func (a *Agent) Calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// Called counts calls starting with prefix.
func (a *Agent) Called(prefix string) int {
	n := 0
	for _, c := range a.Calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (a *Agent) Status() agent.Status { return a.State }

func (a *Agent) Sessions(_ context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	key := q.Cursor + "|" + q.Search
	if err := a.record("Sessions", "sessions "+key); err != nil {
		return agent.SessionPage{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Pages[key], nil
}

func (a *Agent) Create(_ context.Context, req agent.CreateSession) (agent.Session, error) {
	if err := a.record("Create", strings.TrimSpace(fmt.Sprintf("create %s %s %s", req.Directory, req.Model.ID, req.Mode))); err != nil {
		return agent.Session{}, err
	}
	return agent.Session{ID: "new", Directory: req.Directory}, nil
}

func (a *Agent) Messages(_ context.Context, id agent.SessionID) ([]agent.Message, error) {
	if err := a.record("Messages", "messages "+string(id)); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Stored[id], nil
}

func (a *Agent) Send(_ context.Context, req agent.Send) (agent.SendDisposition, error) {
	call := "send " + string(req.Session) + " " + req.Text
	for _, f := range req.Files {
		call += " +" + f.Name
	}
	return a.Disposition, a.record("Send", call)
}

func (a *Agent) Abort(_ context.Context, id agent.SessionID) error {
	return a.record("Abort", "abort "+string(id))
}

func (a *Agent) Reply(_ context.Context, req agent.Reply) error {
	return a.record("Reply", "reply "+string(req.Approval))
}

func (a *Agent) Answer(_ context.Context, answer agent.InteractionAnswer) error {
	return a.record("Answer", fmt.Sprintf("answer %s value=%s confirmed=%v cancelled=%v", answer.ID, answer.Value, answer.Confirmed, answer.Cancelled))
}

func (a *Agent) Rename(_ context.Context, id agent.SessionID, title string) error {
	return a.record("Rename", "rename "+string(id)+" "+title)
}

func (a *Agent) Delete(_ context.Context, id agent.SessionID) error {
	return a.record("Delete", "delete "+string(id))
}

func (a *Agent) Models(context.Context) ([]agent.Model, error) {
	if err := a.record("Models", "models"); err != nil {
		return nil, err
	}
	return a.Catalog, nil
}

func (a *Agent) DefaultModel(context.Context) (agent.ModelRef, error) {
	return a.Default, a.record("DefaultModel", "default-model")
}

func (a *Agent) SetModel(_ context.Context, id agent.SessionID, m agent.ModelRef) error {
	return a.record("SetModel", strings.TrimSpace("set-model "+string(id)+" "+m.Provider+"/"+m.ID+" "+m.Variant))
}

func (a *Agent) Modes(context.Context) ([]agent.Mode, error) {
	return a.ModeList, a.record("Modes", "modes")
}

// Host provides shell.HostKey and records what plugins ask of the window.
type Host struct {
	PageID     string
	Toasts     []string
	Copied     string
	Opened     []string
	Mode       string
	PluginList []kernel.Status
	Toggled    []string
	Notices    []shell.Notice
	Clip       shell.Clipboard // what Clipboard returns
	Background bool            // the window is not in front
}

func (*Host) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-host", Provides: []kernel.Named{shell.HostKey}}
}

func (h *Host) Setup(c *kernel.Context) error {
	return kernel.Provide[shell.Host](c, shell.HostKey, h)
}

func (h *Host) ShowPage(id string)         { h.PageID = id }
func (h *Host) Page() string               { return h.PageID }
func (h *Host) Toast(t ui.Toast)           { h.Toasts = append(h.Toasts, t.Title) }
func (h *Host) Copy(text string) error     { h.Copied = text; return nil }
func (h *Host) Clipboard() shell.Clipboard { return h.Clip }
func (h *Host) OpenURL(url string)         { h.Opened = append(h.Opened, url) }
func (h *Host) Appearance() string         { return h.Mode }
func (h *Host) SetAppearance(mode string)  { h.Mode = mode }
func (h *Host) Plugins() []kernel.Status   { return h.PluginList }
func (h *Host) Enable(id string) error     { h.Toggled = append(h.Toggled, "+"+id); return nil }
func (h *Host) Disable(id string) error    { h.Toggled = append(h.Toggled, "-"+id); return nil }
func (h *Host) Notify(n shell.Notice)      { h.Notices = append(h.Notices, n) }
func (h *Host) Foreground() bool           { return !h.Background }

// Chat provides chat.ControllerKey without a page.
type Chat struct {
	ctx  *kernel.Context
	Conv chat.Conversation
}

func (*Chat) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-chat", Provides: []kernel.Named{chat.ControllerKey}}
}

func (f *Chat) Setup(c *kernel.Context) error {
	f.ctx = c
	return kernel.Provide[chat.Controller](c, chat.ControllerKey, f)
}

func (f *Chat) Current() chat.Conversation { return f.Conv }

func (f *Chat) Open(s agent.Session) {
	f.Conv = chat.Conversation{Session: s.ID, Title: s.Title, Directory: s.Directory, Model: s.Model}
	kernel.Emit(f.ctx, chat.Changed, f.Conv)
}

func (f *Chat) NewChat(dir string) {
	f.Conv = chat.Conversation{Directory: dir}
	kernel.Emit(f.ctx, chat.Changed, f.Conv)
}
