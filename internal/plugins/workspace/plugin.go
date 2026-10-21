// Package workspace keeps the app's own record of where the user works: the
// folders they opened and the conversations started in this app, in
// workspace.db beside the settings. The sidebar and the palette list only
// these. It also adds "Open Folder…" and the composer's folder picker.
package workspace

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// syncPages bounds how far a full sync reads back through the backend's
// conversations looking for the ones the app started.
const syncPages = 20

// Plugin is the workspace record. The zero value is ready to install.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller
	host  shell.Host

	store   *store // nil when nothing is saved
	loaded  bool
	folders []workspace.Folder // by name
	convs   map[key]workspace.Conversation
	list    []workspace.Conversation // the current origin's, newest first
	origin  string                   // the backend's, as last seen
	synced  uint64                   // the connection attempt last synced in full
	full    kernel.Task
	recent  kernel.Task
	picking kernel.Task
	warned  bool // a failed save was reported

	picker onechat.PickerState
	home   string // shown as ~ in folder paths

	// choose asks the user for a folder, off the UI thread; tests replace it.
	choose func() (string, error)
	// now is the clock, in Unix milliseconds; tests replace it.
	now func() int64
}

var _ workspace.Service = (*Plugin)(nil)

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "workspace", Title: "Folders",
		Description: "The folders you work in and the conversations started in this app.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey},
		Provides:    []kernel.Named{workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.store, p.loaded, p.folders, p.list, p.warned = nil, false, nil, nil, false
	p.convs = map[key]workspace.Conversation{}
	p.origin, p.synced = p.agent.Status().Origin, 0
	if p.choose == nil {
		p.choose = func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Open folder", ButtonLabel: "Open",
				Message: "Choose a folder for the agent to work in.", Directory: true, CreateDirectories: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		}
	}
	if p.now == nil {
		p.now = func() int64 { return time.Now().UnixMilli() }
	}
	p.home = homeDir()
	kernel.On(c, agent.StatusChanged, p.statusChanged)
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, chat.Started, p.started)
	if dir := p.host.DataDir(); dir != "" {
		st := newStore(filepath.Join(dir, "workspace.db"))
		p.store = st
		c.Spawn(func(ctx context.Context) {
			st.run(ctx, func(snap snapshot, err error) {
				c.Post(func() { p.load(snap, err) })
			}, func(err error) {
				c.Post(func() { p.saveFailed(err) })
			})
		})
	} else {
		p.loaded = true
	}
	for _, err := range []error{
		kernel.Provide[workspace.Service](c, workspace.ServiceKey, p),
		kernel.Contribute(c, kernel.Commands, "workspace.open-folder", 1, kernel.Command{
			Title: "Open Folder…", Menu: "File", Keys: []string{"cmd+o"},
			Run: func() { p.ChooseFolder(p.chat.NewChat) }}),
		kernel.Contribute(c, chat.ComposerAccessory, "workspace.folder", -20, shell.View{Render: p.chip}),
	} {
		if err != nil {
			return err
		}
	}
	p.maybeSync()
	return nil
}

// load takes in what the database held. Anything recorded meanwhile wins.
func (p *Plugin) load(snap snapshot, err error) {
	if err != nil {
		p.store = nil
		p.host.Toast(ui.Toast{Title: "Folders and conversations won't be saved", Description: err.Error(), Type: components.ToastError})
	}
	for _, f := range snap.folders {
		if !slices.ContainsFunc(p.folders, func(x workspace.Folder) bool { return x.Path == f.Path }) {
			p.folders = append(p.folders, f)
		}
	}
	for k, c := range snap.convs {
		if _, ok := p.convs[k]; !ok {
			p.convs[k] = c
		}
	}
	p.loaded = true
	p.sortFolders()
	p.changed()
	p.maybeSync()
}

func (p *Plugin) saveFailed(err error) {
	if p.warned {
		return
	}
	p.warned = true
	p.host.Toast(ui.Toast{Title: "Folders and conversations were not saved", Description: err.Error() + ". The app tries again with the next change.", Type: components.ToastError})
}

// changed rebuilds the list and tells the consumers.
func (p *Plugin) changed() {
	p.list = nil // a fresh slice: consumers may still hold the last one
	for k, c := range p.convs {
		if k.origin == p.origin && p.origin != "" {
			p.list = append(p.list, c)
		}
	}
	slices.SortFunc(p.list, func(a, b workspace.Conversation) int {
		return cmp.Or(cmp.Compare(b.Updated, a.Updated), cmp.Compare(b.Created, a.Created), strings.Compare(string(a.ID), string(b.ID)))
	})
	kernel.Emit(p.ctx, workspace.Changed, struct{}{})
}

func (p *Plugin) sortFolders() {
	slices.SortFunc(p.folders, func(a, b workspace.Folder) int {
		return cmp.Or(strings.Compare(strings.ToLower(filepath.Base(a.Path)), strings.ToLower(filepath.Base(b.Path))),
			strings.Compare(a.Path, b.Path))
	})
}

func (p *Plugin) persist(k key) {
	if p.store == nil {
		return
	}
	if c, ok := p.convs[k]; ok {
		p.store.enqueue(putConversation(k, c))
	} else {
		p.store.enqueue(dropConversation(k))
	}
}

// ---- workspace.Service

func (p *Plugin) Loaded() bool                            { return p.loaded }
func (p *Plugin) Folders() []workspace.Folder             { return p.folders }
func (p *Plugin) Conversations() []workspace.Conversation { return p.list }

func (p *Plugin) Lookup(id agent.SessionID) (workspace.Conversation, bool) {
	c, ok := p.convs[key{p.origin, id}]
	return c, ok && p.origin != ""
}

func (p *Plugin) AddFolder(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("a folder needs an absolute path")
	}
	p.addFolder(filepath.Clean(path))
	p.changed()
	return nil
}

func (p *Plugin) addFolder(path string) {
	if slices.ContainsFunc(p.folders, func(f workspace.Folder) bool { return f.Path == path }) {
		return
	}
	f := workspace.Folder{Path: path, Added: p.now()}
	p.folders = append(p.folders, f)
	p.sortFolders()
	if p.store != nil {
		p.store.enqueue(putFolder(f))
	}
}

func (p *Plugin) RemoveFolder(path string) {
	i := slices.IndexFunc(p.folders, func(f workspace.Folder) bool { return f.Path == path })
	if i < 0 {
		return
	}
	p.folders = slices.Delete(p.folders, i, i+1)
	maps.DeleteFunc(p.convs, func(_ key, c workspace.Conversation) bool { return c.Folder == path })
	if p.store != nil {
		p.store.enqueue(dropFolder(path)...)
	}
	p.changed()
}

func (p *Plugin) SetCollapsed(path string, collapsed bool) {
	i := slices.IndexFunc(p.folders, func(f workspace.Folder) bool { return f.Path == path })
	if i < 0 || p.folders[i].Collapsed == collapsed {
		return
	}
	p.folders[i].Collapsed = collapsed
	if p.store != nil {
		p.store.enqueue(putFolder(p.folders[i]))
	}
	p.changed()
}

func (p *Plugin) ChooseFolder(done func(path string)) {
	if p.picking.Running() {
		return
	}
	kernel.Go(p.ctx, &p.picking, func(context.Context) (string, error) {
		return p.choose()
	}, func(path string, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The folder did not open", Description: err.Error(), Type: components.ToastError})
			return
		}
		if path == "" { // cancelled
			return
		}
		if err := p.AddFolder(path); err != nil {
			p.host.Toast(ui.Toast{Title: "The folder did not open", Description: err.Error(), Type: components.ToastError})
			return
		}
		done(filepath.Clean(path))
	})
}

func (p *Plugin) Retitle(id agent.SessionID, title string) {
	p.update(id, func(c *workspace.Conversation) { c.Title = title })
}

func (p *Plugin) Forget(id agent.SessionID) {
	k := key{p.origin, id}
	if _, ok := p.convs[k]; !ok {
		return
	}
	delete(p.convs, k)
	p.persist(k)
	p.changed()
}

// update changes a recorded conversation of the current origin, saving and
// announcing it when fn changed anything.
func (p *Plugin) update(id agent.SessionID, fn func(*workspace.Conversation)) {
	if p.edit(id, fn) {
		p.changed()
	}
}

func (p *Plugin) edit(id agent.SessionID, fn func(*workspace.Conversation)) bool {
	k := key{p.origin, id}
	c, ok := p.convs[k]
	if !ok || p.origin == "" {
		return false
	}
	before := c
	fn(&c)
	if c == before {
		return false
	}
	p.convs[k] = c
	p.persist(k)
	return true
}

// ---- What the app sees happen

// started records a conversation the chat page just created.
func (p *Plugin) started(conv chat.Conversation) {
	if p.origin == "" || conv.Session == "" {
		return
	}
	folder := conv.Directory
	if folder != "" {
		folder = filepath.Clean(folder)
		if filepath.IsAbs(folder) {
			p.addFolder(folder)
		}
	}
	now := p.now()
	k := key{p.origin, conv.Session}
	p.convs[k] = workspace.Conversation{ID: conv.Session, Folder: folder, Title: conv.Title, Model: conv.Model, Created: now, Updated: now}
	p.persist(k)
	p.changed()
}

func (p *Plugin) events(batch []agent.Event) {
	changed, finished := false, false
	for _, ev := range batch {
		var fn func(*workspace.Conversation)
		switch ev.Kind {
		case agent.TurnStarted, agent.TurnInterrupted:
			fn = p.touch
		case agent.TurnSucceeded, agent.TurnFailed:
			fn = p.touch
			_, known := p.Lookup(ev.Session)
			finished = finished || known
		case agent.SessionRenamed:
			fn = func(c *workspace.Conversation) { c.Title = ev.Title }
		case agent.ModelChanged:
			fn = func(c *workspace.Conversation) { c.Model = ev.Model }
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ToolFinished, agent.ApprovalRequested, agent.InteractionRequested:
		}
		if fn != nil && p.edit(ev.Session, fn) {
			changed = true
		}
	}
	if changed {
		p.changed()
	}
	if finished {
		p.syncRecent() // a first turn is when the backend names a conversation
	}
}

func (p *Plugin) touch(c *workspace.Conversation) { c.Updated = max(c.Updated, p.now()) }

func (p *Plugin) statusChanged(st agent.Status) {
	if st.Origin != p.origin {
		p.origin = st.Origin
		p.full.Cancel()
		p.recent.Cancel()
		p.synced = 0
		p.changed()
	}
	p.maybeSync()
}

// ---- Catching up with the backend

// maybeSync catches up once per connection: titles given and activity seen
// while the app wasn't listening.
func (p *Plugin) maybeSync() {
	st := p.agent.Status()
	if !p.loaded || st.State != agent.Ready || st.Attempt == p.synced || p.origin == "" {
		return
	}
	p.synced = st.Attempt
	p.sync(&p.full, syncPages)
}

func (p *Plugin) syncRecent() { p.sync(&p.recent, 1) }

// sync reads up to pages of the backend's conversations, newest first, until
// it has seen every one the app recorded, and takes their titles, models and
// activity. A conversation the backend doesn't list stays recorded: a
// listing can't prove it is gone, and the user can remove it.
func (p *Plugin) sync(task *kernel.Task, pages int) {
	want := map[agent.SessionID]bool{}
	for _, c := range p.list {
		want[c.ID] = true
	}
	if len(want) == 0 {
		return
	}
	origin, backend := p.origin, p.agent
	kernel.Go(p.ctx, task, func(ctx context.Context) ([]agent.Session, error) {
		var seen []agent.Session
		cursor := ""
		for range pages {
			page, err := backend.Sessions(ctx, agent.SessionQuery{Cursor: cursor})
			if err != nil {
				return seen, err
			}
			for _, s := range page.Sessions {
				if want[s.ID] {
					seen = append(seen, s)
					delete(want, s.ID)
				}
			}
			if len(want) == 0 || page.Next == "" {
				break
			}
			cursor = page.Next
		}
		return seen, nil
	}, func(seen []agent.Session, _ error) {
		// A failed page keeps what came before it; the list itself is local.
		if origin != p.origin {
			return
		}
		changed := false
		for _, s := range seen {
			changed = p.edit(s.ID, func(c *workspace.Conversation) {
				c.Title = cmp.Or(s.Title, c.Title)
				if !s.Model.IsZero() {
					c.Model = s.Model
				}
				c.Updated = max(c.Updated, s.Updated)
			}) || changed
		}
		if changed {
			p.changed()
		}
	})
}
