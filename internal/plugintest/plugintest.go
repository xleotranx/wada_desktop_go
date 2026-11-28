// Package plugintest provides fake plugins for the contracts, so a plugin's
// tests can run it in a kerneltest harness without a backend or a window.
package plugintest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
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
	return kernel.Manifest{ID: "fake-agent", Provides: []kernel.Named{agent.ServiceKey, agent.ChildrenKey, agent.ModelsKey, agent.ModesKey, agent.InteractionsKey, agent.RewindKey, agent.ChangesKey}}
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

// Workspace provides workspace.ServiceKey in memory. Each of Dirs is a
// project with one workspace there, "prj:<dir>" and "wks:<dir>", named after
// the directory; Inbox, when set, is the inbox's directory. ChooseProject
// picks Chosen at once, adding it to Dirs; an empty Chosen is a cancelled
// dialog.
type Workspace struct {
	ctx     *kernel.Context
	Convs   []workspace.Conversation // newest first
	Dirs    []string
	Inbox   string
	Names   map[string]string // a workspace's name by directory, instead of its base name
	Chosen  string
	Pending bool // the record has not been read yet
	// ArchivedProjects lists the projects ArchiveProject was asked to archive.
	ArchivedProjects []agent.ProjectID
	// Extra are worktree workspaces, listed after their project's.
	Extra []agent.Workspace
	// Made lists CreateWorkspace's requests; it makes the workspace at
	// "<project root>-<slug>" unless CreateErr is set.
	Made      []agent.CreateWorkspace
	CreateErr error
	// Dropped lists ArchiveWorkspace's calls, "<id>" or "<id> force"; a call
	// without force fails with ArchiveErr when it is set.
	Dropped    []string
	ArchiveErr error
	RefList    agent.Refs
	// Gone are the archived workspaces Archived lists: those set here and
	// those ArchiveWorkspace archived. RestoreWorkspace moves one back to
	// Extra, or fails with RestoreErr when it is set.
	Gone       []agent.Workspace
	RestoreErr error
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
func (w *Workspace) Conversations() []workspace.Conversation { return w.Convs }

func (w *Workspace) Projects() []agent.Project {
	var out []agent.Project
	for _, d := range w.all() {
		out = append(out, agent.Project{ID: agent.ProjectID("prj:" + d), Root: d, Name: w.Name(d), Inbox: d == w.Inbox})
	}
	return out
}

func (w *Workspace) Workspaces() []agent.Workspace {
	var out []agent.Workspace
	for _, d := range w.all() {
		out = append(out, w.space(d))
		for _, x := range w.Extra {
			if x.Project == agent.ProjectID("prj:"+d) {
				out = append(out, x)
			}
		}
	}
	return out
}

func (w *Workspace) Project(id agent.ProjectID) (agent.Project, bool) {
	for _, p := range w.Projects() {
		if p.ID == id {
			return p, true
		}
	}
	return agent.Project{}, false
}

func (w *Workspace) CreateWorkspace(req agent.CreateWorkspace, done func(agent.Workspace, error)) {
	w.Made = append(w.Made, req)
	if w.CreateErr != nil {
		done(agent.Workspace{}, w.CreateErr)
		return
	}
	dir := strings.TrimPrefix(string(req.Project), "prj:") + "-" + req.Slug
	made := agent.Workspace{ID: agent.WorkspaceID("wks:" + dir), Project: req.Project, Isolation: req.Isolation,
		Directory: dir, WorktreeRoot: dir, Branch: "wada/" + req.Slug, Title: req.Title}
	w.Extra = append(w.Extra, made)
	w.changed()
	done(made, nil)
}

func (w *Workspace) ArchiveWorkspace(id agent.WorkspaceID, force bool, done func(error)) {
	call := string(id)
	if force {
		call += " force"
	}
	w.Dropped = append(w.Dropped, call)
	if w.ArchiveErr != nil && !force {
		done(w.ArchiveErr)
		return
	}
	w.Extra = slices.DeleteFunc(w.Extra, func(x agent.Workspace) bool {
		if x.ID == id {
			x.Archived = 1
			w.Gone = append(w.Gone, x)
		}
		return x.ID == id
	})
	w.changed()
	done(nil)
}

func (w *Workspace) Archived(done func([]agent.Workspace, error)) { done(slices.Clone(w.Gone), nil) }

func (w *Workspace) RestoreWorkspace(id agent.WorkspaceID, done func(agent.Workspace, error)) {
	if w.RestoreErr != nil {
		done(agent.Workspace{}, w.RestoreErr)
		return
	}
	i := slices.IndexFunc(w.Gone, func(x agent.Workspace) bool { return x.ID == id })
	if i < 0 {
		done(agent.Workspace{}, errors.New("no such workspace"))
		return
	}
	x := w.Gone[i]
	x.Archived = 0
	w.Gone = slices.Delete(w.Gone, i, i+1)
	w.Extra = append(w.Extra, x)
	w.changed()
	done(x, nil)
}

func (w *Workspace) Refs(_ agent.ProjectID, done func(agent.Refs, error)) { done(w.RefList, nil) }

func (w *Workspace) all() []string {
	out := slices.Clone(w.Dirs)
	if w.Inbox != "" {
		out = append(out, w.Inbox)
	}
	return out
}

// space is the workspace at d, created in the order of Dirs.
func (w *Workspace) space(d string) agent.Workspace {
	return agent.Workspace{ID: agent.WorkspaceID("wks:" + d), Project: agent.ProjectID("prj:" + d), Directory: d,
		Created: int64(slices.Index(w.all(), d))}
}

func (w *Workspace) Workspace(id agent.WorkspaceID) (agent.Workspace, bool) {
	for _, x := range w.Extra {
		if x.ID == id {
			return x, true
		}
	}
	d, ok := strings.CutPrefix(string(id), "wks:")
	if !ok || !slices.Contains(w.all(), d) {
		return agent.Workspace{}, false
	}
	return w.space(d), true
}

func (w *Workspace) At(dir string) (agent.Workspace, bool) {
	if dir == "" {
		dir = w.Inbox
	}
	for _, x := range w.Extra {
		if x.Directory == dir {
			return x, true
		}
	}
	if dir == "" || !slices.Contains(w.all(), dir) {
		return agent.Workspace{}, false
	}
	return w.space(dir), true
}

func (w *Workspace) Name(dir string) string {
	if dir == "" || dir == w.Inbox {
		return "Inbox"
	}
	if n := w.Names[dir]; n != "" {
		return n
	}
	for _, x := range w.Extra {
		if x.Directory == dir && x.Branch != "" {
			return cmpOr(x.Title, x.Branch)
		}
	}
	return path.Base(dir)
}

func (w *Workspace) Lookup(id agent.SessionID) (workspace.Conversation, bool) {
	i := slices.IndexFunc(w.Convs, func(c workspace.Conversation) bool { return c.ID == id })
	if i < 0 {
		return workspace.Conversation{}, false
	}
	return w.Convs[i], true
}

func (w *Workspace) ChooseProject(done func(agent.Workspace)) {
	if w.Chosen == "" {
		return
	}
	if !slices.Contains(w.all(), w.Chosen) {
		w.Dirs = append(w.Dirs, w.Chosen)
		w.changed()
	}
	done(w.space(w.Chosen))
}

func (w *Workspace) ArchiveProject(id agent.ProjectID, done func()) {
	w.ArchivedProjects = append(w.ArchivedProjects, id)
	d := strings.TrimPrefix(string(id), "prj:")
	w.Dirs = slices.DeleteFunc(w.Dirs, func(x string) bool { return x == d })
	w.Convs = slices.DeleteFunc(w.Convs, func(c workspace.Conversation) bool { return c.Directory == d })
	w.changed()
	done()
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

// Projects provides agent.WorkspacesKey in memory, as a backend that keeps
// projects does: each of Dirs, and Inbox when set, is a project with one
// workspace there, "prj:<dir>" and "wks:<dir>". Its methods may be called
// from any goroutine.
type Projects struct {
	mu    sync.Mutex
	Dirs  []string
	Inbox string
	Fail  map[string]error // by method name, e.g. "AddProject"
	// Made are the worktree workspaces CreateWorkspace made, each at
	// "<project root>-<slug>" on branch "wada/<slug>" from RefList.Default.
	Made []agent.Workspace
	// Created and Archived record the requests.
	Created  []agent.CreateWorkspace
	Archived []string // "<id>" or "<id> force"
	RefList  agent.Refs
	Base     []agent.FileChange // what BaseChanges returns
	// Configs are the workspaces' wada.json files. TrustConfig trusts one;
	// RunSetup and StartService start a run as "term-setup" and
	// "term-<name>" once it is trusted. Ran records "setup <id>",
	// "start <id> <name>" and "stop <id> <name>".
	Configs map[agent.WorkspaceID]agent.WorkspaceConfig
	Ran     []string
}

func (f *Projects) New() kernel.Plugin { return f }
func (*Projects) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-projects", Provides: []kernel.Named{agent.WorkspacesKey}}
}

func (f *Projects) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.Workspaces](c, agent.WorkspacesKey, f)
}

func (f *Projects) fail(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Fail[method]
}

func (f *Projects) dirs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.Dirs)
	if f.Inbox != "" {
		out = append(out, f.Inbox)
	}
	return out
}

func (f *Projects) project(d string) agent.Project {
	name := path.Base(d)
	if d == f.Inbox {
		name = "Inbox"
	}
	return agent.Project{ID: agent.ProjectID("prj:" + d), Root: d, Name: name, Inbox: d == f.Inbox}
}

func (f *Projects) Projects(context.Context, bool) ([]agent.Project, error) {
	if err := f.fail("Projects"); err != nil {
		return nil, err
	}
	var out []agent.Project
	for _, d := range f.dirs() {
		out = append(out, f.project(d))
	}
	return out, nil
}

func (f *Projects) Workspaces(_ context.Context, archived bool) ([]agent.Workspace, error) {
	if err := f.fail("Workspaces"); err != nil {
		return nil, err
	}
	var out []agent.Workspace
	for _, d := range f.dirs() {
		out = append(out, agent.Workspace{ID: agent.WorkspaceID("wks:" + d), Project: agent.ProjectID("prj:" + d), Directory: d})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.Made {
		if w.Archived == 0 || archived {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *Projects) CreateWorkspace(_ context.Context, req agent.CreateWorkspace) (agent.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Created = append(f.Created, req)
	if err := f.Fail["CreateWorkspace"]; err != nil {
		return agent.Workspace{}, err
	}
	root := strings.TrimPrefix(string(req.Project), "prj:")
	dir := root + "-" + req.Slug
	w := agent.Workspace{ID: agent.WorkspaceID("wks:" + dir), Project: req.Project, Isolation: req.Isolation,
		Directory: dir, WorktreeRoot: dir, Branch: "wada/" + req.Slug, BaseRef: f.RefList.Default, Title: req.Title}
	f.Made = append(f.Made, w)
	return w, nil
}

func (f *Projects) ArchiveWorkspace(_ context.Context, id agent.WorkspaceID, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := string(id)
	if force {
		call += " force"
	}
	f.Archived = append(f.Archived, call)
	if err := f.Fail["ArchiveWorkspace"]; err != nil && !force {
		return err
	}
	for i := range f.Made {
		if f.Made[i].ID == id {
			f.Made[i].Archived = 1
		}
	}
	return nil
}

func (f *Projects) Config(_ context.Context, id agent.WorkspaceID) (agent.WorkspaceConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Configs[id], nil
}

func (f *Projects) TrustConfig(_ context.Context, id agent.WorkspaceID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.Configs[id]
	if c.Hash != hash {
		return errors.New("wada.json changed")
	}
	c.Trusted = true
	f.Configs[id] = c
	return nil
}

func (f *Projects) RunSetup(_ context.Context, id agent.WorkspaceID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ran = append(f.Ran, "setup "+string(id))
	c := f.Configs[id]
	if !c.Trusted {
		return "", agent.ErrUntrusted
	}
	c.SetupRun = &agent.Run{Terminal: "term-setup", Running: true}
	f.Configs[id] = c
	return "term-setup", nil
}

func (f *Projects) StartService(_ context.Context, id agent.WorkspaceID, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ran = append(f.Ran, "start "+string(id)+" "+name)
	c := f.Configs[id]
	if !c.Trusted {
		return "", agent.ErrUntrusted
	}
	for i := range c.Services {
		if c.Services[i].Name == name {
			c.Services[i].Run = &agent.Run{Terminal: "term-" + name, Running: true}
		}
	}
	return "term-" + name, nil
}

func (f *Projects) StopService(_ context.Context, id agent.WorkspaceID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ran = append(f.Ran, "stop "+string(id)+" "+name)
	c := f.Configs[id]
	for i := range c.Services {
		if c.Services[i].Name == name && c.Services[i].Run != nil {
			c.Services[i].Run.Running = false
		}
	}
	return nil
}

func (f *Projects) RestoreWorkspace(_ context.Context, id agent.WorkspaceID) (agent.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Made {
		if f.Made[i].ID == id {
			f.Made[i].Archived = 0
			return f.Made[i], nil
		}
	}
	return agent.Workspace{}, errors.New("no such workspace")
}

func (f *Projects) Refs(context.Context, agent.ProjectID) (agent.Refs, error) {
	if err := f.fail("Refs"); err != nil {
		return agent.Refs{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.RefList, nil
}

func (f *Projects) BaseChanges(context.Context, agent.WorkspaceID) ([]agent.FileChange, error) {
	if err := f.fail("BaseChanges"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Base), nil
}

func (f *Projects) AddProject(_ context.Context, root string) (agent.Project, agent.Workspace, error) {
	if err := f.fail("AddProject"); err != nil {
		return agent.Project{}, agent.Workspace{}, err
	}
	f.mu.Lock()
	if !slices.Contains(f.Dirs, root) {
		f.Dirs = append(f.Dirs, root)
	}
	f.mu.Unlock()
	return f.project(root), agent.Workspace{ID: agent.WorkspaceID("wks:" + root), Project: agent.ProjectID("prj:" + root), Directory: root}, nil
}

func (f *Projects) RenameProject(context.Context, agent.ProjectID, string) error {
	return f.fail("RenameProject")
}

func (f *Projects) ArchiveProject(_ context.Context, id agent.ProjectID) error {
	if err := f.fail("ArchiveProject"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Dirs = slices.DeleteFunc(f.Dirs, func(d string) bool { return "prj:"+d == string(id) })
	return nil
}

func (f *Projects) RenameWorkspace(context.Context, agent.WorkspaceID, string) error {
	return f.fail("RenameWorkspace")
}

// Send runs the chat.Outgoing pipeline as the chat does, committing what it accepts.
func Send(o kernel.Owner, d chat.Draft) (chat.Draft, error) {
	out, commit, err := kernel.Run(o, chat.Outgoing, d)
	if err == nil {
		commit()
	}
	return out, err
}

// Outgoing runs Send and fails the test if any transform rejects the draft.
func Outgoing(t *testing.T, o kernel.Owner, d chat.Draft) chat.Draft {
	t.Helper()
	out, err := Send(o, d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Tabs provides tabs.ServiceKey over test state. Open, Select and Close
// keep List and Current and tell pane kinds what comes on and goes off
// show, as the tabs plugin does; Opened records Open's calls.
type Tabs struct {
	List    []tabs.Tab
	Current tabs.Tab
	Opened  []string // "<pane> <ref> <folder>"
	ctx     *kernel.Context
}

func (t *Tabs) New() kernel.Plugin { return t }
func (*Tabs) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-tabs", Provides: []kernel.Named{tabs.ServiceKey}}
}
func (t *Tabs) Setup(c *kernel.Context) error {
	t.ctx = c
	return kernel.Provide[tabs.Service](c, tabs.ServiceKey, t)
}
func (t *Tabs) Tabs() []tabs.Tab {
	if len(t.List) > 0 {
		return t.List
	}
	if t.Current.ID != "" {
		return []tabs.Tab{t.Current}
	}
	return nil
}
func (t *Tabs) Active() (tabs.Tab, bool) { return t.Current, t.Current.ID != "" }
func (t *Tabs) Space() string            { return t.Current.Folder }

func (t *Tabs) Select(id string) {
	if i := slices.IndexFunc(t.List, func(x tabs.Tab) bool { return x.ID == id }); i >= 0 {
		t.show(t.List[i])
	}
}

func (t *Tabs) Close(id string) {
	t.List = slices.DeleteFunc(t.List, func(x tabs.Tab) bool { return x.ID == id })
	if t.Current.ID == id {
		t.show(tabs.Tab{Folder: t.Current.Folder})
	}
}

func (t *Tabs) Open(pane, ref, folder string) {
	t.Opened = append(t.Opened, pane+" "+ref+" "+folder)
	id := "pane:" + pane + ":" + folder + ":" + ref
	if !slices.ContainsFunc(t.List, func(x tabs.Tab) bool { return x.ID == id }) {
		t.List = append(t.List, tabs.Tab{ID: id, Kind: tabs.Pane, Pane: pane, Ref: ref, Folder: folder})
	}
	t.Select(id)
}

// show makes next the current tab, telling the pane kinds.
func (t *Tabs) show(next tabs.Tab) {
	prev := t.Current
	t.Current = next
	if prev.Kind == tabs.Pane && prev.ID != "" && prev.Pane != next.Pane {
		t.pane(prev.Pane, func(k tabs.PaneKind) {
			if k.Hide != nil {
				k.Hide()
			}
		})
	}
	if next.Kind == tabs.Pane && next.ID != "" && next.ID != prev.ID {
		t.pane(next.Pane, func(k tabs.PaneKind) {
			if k.Show != nil {
				k.Show(next)
			}
		})
	}
}

func (t *Tabs) pane(kind string, fn func(tabs.PaneKind)) {
	for _, e := range kernel.Items(t.ctx, tabs.Panes) {
		if e.ID == kind {
			kernel.Invoke(t.ctx, e, fn)
		}
	}
}

// Images provides attachment.ServiceKey in memory for tests.
type Images struct {
	Attached []agent.File
	Current  attachment.Image
	Details  []attachment.Detail
	Replaced int
}

func (f *Images) New() kernel.Plugin { return f }
func (*Images) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-images", Provides: []kernel.Named{attachment.ServiceKey}}
}
func (f *Images) Setup(c *kernel.Context) error {
	return kernel.Provide[attachment.Service](c, attachment.ServiceKey, f)
}
func (f *Images) Snapshot(id uint64) (attachment.Image, bool) {
	return f.Current, id == f.Current.ID && id != 0
}
func (f *Images) Attach(file agent.File, _ *ui.Bitmap) (uint64, error) {
	f.Attached = append(f.Attached, file)
	f.Current = attachment.Image{ID: f.Current.ID + 1, Revision: 1, File: file}
	return f.Current.ID, nil
}
func (f *Images) Replace(_ attachment.Image, file agent.File, _ *ui.Bitmap, notes string, details []attachment.Detail) error {
	file.Data = bytes.Clone(file.Data)
	f.Current.File, f.Current.Notes, f.Details = file, notes, details
	f.Current.Revision++
	f.Replaced++
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
