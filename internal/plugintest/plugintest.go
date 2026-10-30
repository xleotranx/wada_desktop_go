// Package plugintest provides fake plugins for the contracts, so a plugin's
// tests can run it in a kerneltest harness without a backend or a window.
package plugintest

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/xleotranx/wada-kit/kernel"
)

// Agent provides the service, models, modes, interactions and rewinding with
// answers the test sets. Calls are recorded as short strings, e.g. "send s1 hello".
type Agent struct {
	ctx         *kernel.Context
	mu          sync.Mutex
	calls       []string
	replies     []agent.Reply
	created     int
	spawned     int
	children    []agent.Spawn
	Stored      map[agent.SessionID][]agent.Message
	Pages       map[string]agent.SessionPage // by "cursor|search"
	Fail        map[string]error             // by method name, e.g. "Send"
	State       agent.Status
	Disposition agent.SendDisposition
	// Forks makes Rewind go on in a new conversation, "<id>-fork", as a forking backend
	// does, instead of rewinding in place.
	Forks bool
	// Numbered gives each created conversation its own ID, "new-1", "new-2",
	// instead of "new" every time.
	Numbered bool
	// OnSend runs on the sending goroutine for every admitted Send, so a test
	// can script what the agent does next with Emit and Respond.
	OnSend func(req agent.Send)
	// OnSpawn scripts a partial creation or a backend-specific child response.
	OnSpawn func(req agent.Spawn) (agent.Session, error)

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

func (f *Agent) New() kernel.Plugin { return f }
func (*Agent) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-agent", Provides: []kernel.Named{agent.ServiceKey, agent.ChildrenKey, agent.ModelsKey, agent.ModesKey, agent.InteractionsKey, agent.RewindKey, agent.ChangesKey, agent.SnapshotsKey}}
}

func (a *Agent) Setup(c *kernel.Context) error {
	a.ctx = c
	if err := kernel.Provide[agent.Service](c, agent.ServiceKey, a); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Children](c, agent.ChildrenKey, a); err != nil {
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
	kernel.Provide[agent.Snapshots](c, agent.SnapshotsKey, a)
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

func (a *Agent) Spawn(_ context.Context, req agent.Spawn) (agent.Session, error) {
	if err := a.record("Spawn", "spawn "+string(req.Parent)+" "+req.Name); err != nil {
		return agent.Session{}, err
	}
	if a.OnSpawn != nil {
		return a.OnSpawn(req)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.spawned++
	a.children = append(a.children, req)
	return agent.Session{ID: agent.SessionID(fmt.Sprintf("child-%d", a.spawned)), Directory: req.Directory}, nil
}

// SpawnRequests returns the child agents requested so far.
func (a *Agent) SpawnRequests() []agent.Spawn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.children)
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
// thread, in a quiet post whose listeners ask for a frame when what they show
// changed. Call it from any goroutine.
func (a *Agent) Emit(events ...agent.Event) {
	a.ctx.PostQuiet(func() { kernel.Emit(a.ctx, agent.Events, events) })
}

// Respond emits a turn of session that ends with text as its final answer.
func (a *Agent) Respond(session agent.SessionID, text string) {
	a.Emit(
		agent.Event{Kind: agent.TurnStarted, Session: session},
		agent.Event{Kind: agent.TextEnded, Session: session, Text: &agent.TextEvent{Message: "m", Text: text}},
		agent.Event{Kind: agent.TurnSucceeded, Session: session})
}

func (a *Agent) Abort(ctx context.Context, id agent.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if answer.Answers != nil {
		return a.record("Answer", fmt.Sprintf("answer %s answers=%v", answer.ID, answer.Answers))
	}
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

// Chat provides chat.ControllerKey without a page. Draft is the composer's
// text: it goes out on chat.Leaving and empties when another conversation
// opens, as in the chat.
type Chat struct {
	ctx       *kernel.Context
	nextDraft uint64
	Conv      chat.Conversation
	Draft     string
	Work      bool // what WorkShown reports
}

func (f *Chat) New() kernel.Plugin { return f }
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
	f.nextDraft++
	f.Conv = chat.Conversation{DraftID: f.nextDraft, Directory: dir}
	kernel.Emit(f.ctx, chat.Changed, f.Conv)
}

func (f *Chat) leave() {
	kernel.Emit(f.ctx, chat.Leaving, chat.Unsent{Conversation: f.Conv, Text: f.Draft})
	f.Draft = ""
}

func (f *Chat) SetDraft(text string) { f.Draft = text }

func (f *Chat) DraftText() string { return f.Draft }

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

func (f *Files) New() kernel.Plugin { return f }
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

// Find lists the files and their folders whose path contains query, in path
// order.
func (f *Files) Find(_ context.Context, directory, query string, limit int) ([]agent.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "find "+directory+"|"+query)
	if err := f.Fail["Find"]; err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	for file := range f.Tree {
		for dir := path.Dir(file); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	var paths []string
	for file := range f.Tree {
		if strings.Contains(file, query) {
			paths = append(paths, file)
		}
	}
	for dir := range dirs {
		if strings.Contains(dir, query) {
			paths = append(paths, dir+"/")
		}
	}
	slices.Sort(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	entries := make([]agent.FileEntry, len(paths))
	for i, p := range paths {
		entries[i] = agent.FileEntry{Path: strings.TrimSuffix(p, "/"), IsDir: strings.HasSuffix(p, "/")}
	}
	return entries, nil
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

// Skills provides agent.SkillsKey over a fixed list, so a test can offer
// skills without a backend. Calls are recorded as "skills <directory>".
type Skills struct {
	mu    sync.Mutex
	calls []string
	List  []agent.Skill
	Fail  error
}

func NewSkills(list ...agent.Skill) *Skills { return &Skills{List: list} }

func (s *Skills) New() kernel.Plugin { return s }
func (*Skills) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-skills", Provides: []kernel.Named{agent.SkillsKey}}
}

func (s *Skills) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Skills](c, agent.SkillsKey, s)
}

// Calls returns the calls so far.
func (s *Skills) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *Skills) Skills(_ context.Context, directory string) ([]agent.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "skills "+directory)
	if s.Fail != nil {
		return nil, s.Fail
	}
	return slices.Clone(s.List), nil
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

func (f *Workspace) New() kernel.Plugin { return f }
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

func (a *Agent) Snapshot(ctx context.Context, id agent.SessionID) (agent.Snapshot, error) {
	if err := a.record("Snapshot", "snapshot "+string(id)); err != nil {
		return nil, err
	}
	return agent.Snapshot{}, nil
}
func (a *Agent) Diff(ctx context.Context, id agent.SessionID, before agent.Snapshot) ([]agent.FileChange, error) {
	return a.Changes(ctx, id)
}
