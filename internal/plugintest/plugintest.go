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
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

// Agent provides the service, models, modes, interactions and rewinding with
// answers the test sets. Calls are recorded as short strings, e.g. "send s1 hello".
type Agent struct {
	ctx         *kernel.Context
	mu          sync.Mutex
	calls       []string
	replies     []agent.Reply
	created     int
	Stored      map[agent.SessionID][]agent.Message
	Pages       map[string]agent.SessionPage // by "cursor|search"
	Fail        map[string]error             // by method name, e.g. "Send"
	State       agent.Status
	Disposition agent.SendDisposition
	// Forks makes Rewind go on in a new conversation, "<id>-fork", as Pi
	// does, instead of rewinding in place.
	Forks bool
	// Numbered gives each created conversation its own ID, "new-1", "new-2",
	// instead of "new" every time.
	Numbered bool
	// OnSend runs on the sending goroutine for every admitted Send, so a test
	// can script what the agent does next with Emit and Respond.
	OnSend func(req agent.Send)

	// Changed is what Changes returns, by conversation.
	Changed map[agent.SessionID][]agent.FileChange

	// The model capabilities.
	Catalog  []agent.Model
	Default  agent.ModelRef
	ModeList []agent.Mode
}

func NewAgent() *Agent {
	return &Agent{Stored: map[agent.SessionID][]agent.Message{}, Pages: map[string]agent.SessionPage{},
		Changed: map[agent.SessionID][]agent.FileChange{},
		Fail:    map[string]error{}, State: agent.Status{State: agent.Ready, Attempt: 1, Origin: "fake"}}
}

func (*Agent) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-agent", Provides: []kernel.Named{agent.ServiceKey, agent.ModelsKey, agent.ModesKey, agent.InteractionsKey, agent.RewindKey, agent.ChangesKey}}
}

func (a *Agent) Setup(c *kernel.Context) error {
	a.ctx = c
	if err := kernel.Provide[agent.Service](c, agent.ServiceKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Models](c, agent.ModelsKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Modes](c, agent.ModesKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Rewinder](c, agent.RewindKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Changes](c, agent.ChangesKey, a); err != nil {
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
	id := agent.SessionID("new")
	if a.Numbered {
		a.mu.Lock()
		a.created++
		id = agent.SessionID(fmt.Sprintf("new-%d", a.created))
		a.mu.Unlock()
	}
	return agent.Session{ID: id, Directory: req.Directory}, nil
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
	if err := a.record("Send", call); err != nil {
		return a.Disposition, err
	}
	if a.OnSend != nil {
		a.OnSend(req)
	}
	return a.Disposition, nil
}

// Emit delivers events the way a backend does: as one batch on the UI
// thread. Call it from any goroutine.
func (a *Agent) Emit(events ...agent.Event) {
	a.ctx.Post(func() { kernel.Emit(a.ctx, agent.Events, events) })
}

// Respond emits a turn of session that ends with text as its final answer.
func (a *Agent) Respond(session agent.SessionID, text string) {
	a.Emit(
		agent.Event{Kind: agent.TurnStarted, Session: session},
		agent.Event{Kind: agent.TextEnded, Session: session, Text: &agent.TextEvent{Message: "m", Text: text}},
		agent.Event{Kind: agent.TurnSucceeded, Session: session})
}

func (a *Agent) Abort(_ context.Context, id agent.SessionID) error {
	return a.record("Abort", "abort "+string(id))
}

func (a *Agent) Reply(_ context.Context, req agent.Reply) error {
	a.mu.Lock()
	a.replies = append(a.replies, req)
	a.mu.Unlock()
	return a.record("Reply", "reply "+string(req.Approval))
}

// Replies returns the approval replies so far.
func (a *Agent) Replies() []agent.Reply {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.replies)
}

func (a *Agent) Answer(_ context.Context, answer agent.InteractionAnswer) error {
	return a.record("Answer", fmt.Sprintf("answer %s value=%s confirmed=%v cancelled=%v", answer.ID, answer.Value, answer.Confirmed, answer.Cancelled))
}

func (a *Agent) Rename(_ context.Context, id agent.SessionID, title string) error {
	return a.record("Rename", "rename "+string(id)+" "+title)
}

// Rewind drops message and what follows from the stored conversation, or
// copies what precedes it into "<id>-fork" when Forks is set.
func (a *Agent) Rewind(_ context.Context, id agent.SessionID, message agent.MessageID) (agent.SessionID, error) {
	if err := a.record("Rewind", "rewind "+string(id)+" "+string(message)); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored := a.Stored[id]
	i := slices.IndexFunc(stored, func(m agent.Message) bool { return m.ID == message })
	if i < 0 {
		return "", fmt.Errorf("no message %s in %s", message, id)
	}
	if a.Forks {
		fork := id + "-fork"
		a.Stored[fork] = slices.Clone(stored[:i])
		return fork, nil
	}
	a.Stored[id] = stored[:i]
	return id, nil
}

func (a *Agent) Changes(_ context.Context, id agent.SessionID) ([]agent.FileChange, error) {
	if err := a.record("Changes", "changes "+string(id)); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.Changed[id]), nil
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
	ctx        *kernel.Context
	PageID     string
	Toasts     []string
	Copied     string
	Opened     []string
	Glass      bool // what Translucent reports
	Mode       string
	PluginList []kernel.Status
	Toggled    []string
	Notices    []shell.Notice
	Clip       shell.Clipboard // what Clipboard returns
	Background bool            // the window is not in front
	Data       string          // what DataDir returns
}

func (*Host) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-host", Provides: []kernel.Named{shell.HostKey}}
}

func (h *Host) Setup(c *kernel.Context) error {
	h.ctx = c
	return kernel.Provide[shell.Host](c, shell.HostKey, h)
}

// ShowPage records the page and emits shell.PageChanged when it changed, as
// the shell does.
func (h *Host) ShowPage(id string) {
	if id == h.PageID {
		return
	}
	h.PageID = id
	if h.ctx != nil {
		kernel.Emit(h.ctx, shell.PageChanged, id)
	}
}

func (h *Host) Page() string               { return h.PageID }
func (h *Host) Toast(t ui.Toast)           { h.Toasts = append(h.Toasts, t.Title) }
func (h *Host) Copy(text string) error     { h.Copied = text; return nil }
func (h *Host) Clipboard() shell.Clipboard { return h.Clip }
func (h *Host) OpenURL(url string)         { h.Opened = append(h.Opened, url) }
func (h *Host) Appearance() string         { return h.Mode }
func (h *Host) SetAppearance(mode string)  { h.Mode = mode }
func (h *Host) Translucent() bool          { return h.Glass }
func (h *Host) SetTranslucent(on bool)     { h.Glass = on }
func (h *Host) Plugins() []kernel.Status   { return h.PluginList }
func (h *Host) Enable(id string) error     { h.Toggled = append(h.Toggled, "+"+id); return nil }
func (h *Host) Disable(id string) error    { h.Toggled = append(h.Toggled, "-"+id); return nil }
func (h *Host) Notify(n shell.Notice)      { h.Notices = append(h.Notices, n) }
func (h *Host) Foreground() bool           { return !h.Background }
func (h *Host) DataDir() string            { return h.Data }

// Chat provides chat.ControllerKey without a page. Draft is the composer's
// text: it goes out on chat.Leaving and empties when another conversation
// opens, as in the chat.
type Chat struct {
	ctx   *kernel.Context
	Conv  chat.Conversation
	Draft string
	Work  bool // what WorkShown reports
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
	if s.ID != "" && s.ID == f.Conv.Session {
		return
	}
	f.leave()
	f.Conv = chat.Conversation{Session: s.ID, Title: s.Title, Directory: s.Directory, Model: s.Model}
	kernel.Emit(f.ctx, chat.Changed, f.Conv)
}

func (f *Chat) NewChat(dir string) {
	f.leave()
	f.Conv = chat.Conversation{Directory: dir}
	kernel.Emit(f.ctx, chat.Changed, f.Conv)
}

func (f *Chat) leave() {
	kernel.Emit(f.ctx, chat.Leaving, chat.Unsent{Conversation: f.Conv, Text: f.Draft})
	f.Draft = ""
}

func (f *Chat) SetDraft(text string) { f.Draft = text }

func (f *Chat) WorkShown() bool { return f.Work }

func (f *Chat) SetDirectory(dir string) {
	if f.Conv.Session == "" && !f.Conv.Starting {
		f.Conv.Directory = dir
		kernel.Emit(f.ctx, chat.Changed, f.Conv)
	}
}

// Files provides agent.FilesKey over an in-memory tree, so a test can mention
// files without a backend. Calls are recorded as "find <dir>|<query>" and
// "read <dir>|<path>".
type Files struct {
	mu    sync.Mutex
	calls []string
	Tree  map[string][]byte // contents by path, relative to any directory
	Fail  map[string]error  // by method name, "Find" or "Read"
}

func NewFiles(tree map[string]string) *Files {
	f := &Files{Tree: map[string][]byte{}, Fail: map[string]error{}}
	for path, data := range tree {
		f.Tree[path] = []byte(data)
	}
	return f
}

func (*Files) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-files", Provides: []kernel.Named{agent.FilesKey}}
}

func (f *Files) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Files](c, agent.FilesKey, f)
}

// Calls returns the calls so far.
func (f *Files) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Find lists the paths that contain query, in path order.
func (f *Files) Find(_ context.Context, directory, query string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "find "+directory+"|"+query)
	if err := f.Fail["Find"]; err != nil {
		return nil, err
	}
	var paths []string
	for path := range f.Tree {
		if strings.Contains(path, query) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	return paths, nil
}

func (f *Files) Read(_ context.Context, directory, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "read "+directory+"|"+path)
	if err := f.Fail["Read"]; err != nil {
		return nil, err
	}
	data, ok := f.Tree[path]
	if !ok {
		return nil, fmt.Errorf("%s: no such file", path)
	}
	return data, nil
}

// Workspace provides workspace.ServiceKey in memory. ChooseFolder picks
// Chosen at once; an empty Chosen is a cancelled dialog.
type Workspace struct {
	ctx       *kernel.Context
	Convs     []workspace.Conversation // newest first
	FolderSet []workspace.Folder       // by name
	Chosen    string
	Pending   bool // the record has not been read yet
}

func (*Workspace) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-workspace", Provides: []kernel.Named{workspace.ServiceKey}}
}

func (w *Workspace) Setup(c *kernel.Context) error {
	w.ctx = c
	return kernel.Provide[workspace.Service](c, workspace.ServiceKey, w)
}

func (w *Workspace) changed() { kernel.Emit(w.ctx, workspace.Changed, struct{}{}) }

func (w *Workspace) Loaded() bool                            { return !w.Pending }
func (w *Workspace) Folders() []workspace.Folder             { return w.FolderSet }
func (w *Workspace) Conversations() []workspace.Conversation { return w.Convs }

func (w *Workspace) Lookup(id agent.SessionID) (workspace.Conversation, bool) {
	i := slices.IndexFunc(w.Convs, func(c workspace.Conversation) bool { return c.ID == id })
	if i < 0 {
		return workspace.Conversation{}, false
	}
	return w.Convs[i], true
}

func (w *Workspace) AddFolder(path string) error {
	if !slices.ContainsFunc(w.FolderSet, func(f workspace.Folder) bool { return f.Path == path }) {
		w.FolderSet = append(w.FolderSet, workspace.Folder{Path: path})
		w.changed()
	}
	return nil
}

func (w *Workspace) RemoveFolder(path string) {
	w.FolderSet = slices.DeleteFunc(w.FolderSet, func(f workspace.Folder) bool { return f.Path == path })
	w.Convs = slices.DeleteFunc(w.Convs, func(c workspace.Conversation) bool { return c.Folder == path })
	w.changed()
}

func (w *Workspace) ChooseFolder(done func(path string)) {
	if w.Chosen != "" {
		_ = w.AddFolder(w.Chosen)
		done(w.Chosen)
	}
}

func (w *Workspace) Retitle(id agent.SessionID, title string) {
	for i := range w.Convs {
		if w.Convs[i].ID == id {
			w.Convs[i].Title = title
		}
	}
	w.changed()
}

func (w *Workspace) Forget(id agent.SessionID) {
	w.Convs = slices.DeleteFunc(w.Convs, func(c workspace.Conversation) bool { return c.ID == id })
	w.changed()
}
