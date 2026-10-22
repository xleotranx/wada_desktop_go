// Package tabs keeps the window's tabs: the conversations, new chats and
// pages the user has open. The title strip shows one folder at a time, its
// space: that folder's tabs, then the pages, with a switcher for the other
// folders. Tabs follow navigation: whatever the chat or the shell shows
// becomes the active tab, and its folder the space on show, so nothing has
// to open things through them. A background tab shows what its conversation
// is doing, read from the agent's events.
package tabs

import (
	"slices"
	"strconv"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// Plugin is the tabs plugin. The zero value is ready to install.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	chat  chat.Controller
	host  shell.Host
	ws    workspace.Service

	saved       *kernel.Setting[[]saved]
	savedActive *kernel.Setting[int]
	// colors holds each folder's index into the palette's project colors.
	colors *kernel.Setting[map[string]int]
	reopen *kernel.Setting[bool] // the tabs of the last run come back

	tabs   []tabs.Tab // without State; states holds it
	active string
	states map[agent.SessionID]tabs.State
	// recorded holds the conversations the workspace record has listed, so
	// one it stops listing is closed but one it has not recorded yet is not.
	recorded map[agent.SessionID]bool
	restored bool
	// selecting is set while Select navigates, so the navigation it causes
	// is not followed as if someone else had caused it.
	selecting bool
	drafts    int // numbers the draft tabs' IDs
	// unsent holds the text typed and not sent in tabs not on show, by tab ID,
	// for this run only.
	unsent map[string]string
	// chatTab is the tab the chat shows or showed last, which names the new
	// chat whose text the chat leaves behind.
	chatTab string
	// space is the folder whose tabs the strip shows; lastIn holds each
	// folder's tab shown last, to come back to.
	space     string
	lastIn    map[string]string
	switching bool // the space switcher is open

	// The dialogs the menus open.
	renaming tabs.Tab
	title    string
	renameOn bool
	deleting tabs.Tab
	deleteOn bool
	removing string // the folder whose removal is being confirmed
	removeOn bool
}

// saved is a tab as it is kept between runs.
type saved struct {
	Kind    tabs.Kind       `json:"kind"`
	Session agent.SessionID `json:"session,omitempty"`
	Folder  string          `json:"folder,omitempty"`
	Page    string          `json:"page,omitempty"`
	Title   string          `json:"title,omitempty"`
}

const newChatTitle = "New chat"

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "tabs", Title: "Tabs", Locked: true,
		Description: "Open chats as tabs across the window, grouped by folder.",
		Provides:    []kernel.Named{tabs.ServiceKey},
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey, shell.HostKey, workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.saved = kernel.NewSetting[[]saved](c, "open", nil)
	p.savedActive = kernel.NewSetting(c, "active", 0)
	p.colors = kernel.NewSetting(c, "colors", map[string]int{})
	p.reopen = kernel.NewSetting(c, "reopen", true)
	p.tabs, p.active, p.restored, p.selecting = nil, "", false, false
	p.states, p.recorded = map[agent.SessionID]tabs.State{}, map[agent.SessionID]bool{}
	p.unsent, p.chatTab = map[string]string{}, ""
	p.space, p.lastIn, p.switching = "", map[string]string{}, false
	p.renaming, p.renameOn, p.deleting, p.deleteOn, p.removing, p.removeOn = tabs.Tab{}, false, tabs.Tab{}, false, "", false
	if err := kernel.Provide[tabs.Service](c, tabs.ServiceKey, p); err != nil {
		return err
	}
	kernel.On(c, chat.Changed, p.followChat)
	kernel.On(c, chat.Started, p.started)
	kernel.On(c, chat.Leaving, p.leaving)
	kernel.On(c, shell.PageChanged, p.followPage)
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, workspace.Changed, func(struct{}) { p.recordChanged() })
	kernel.On(c, kernel.PointChanged, func(point string) {
		if point == shell.Pages.Name() {
			p.pagesChanged()
		}
	})
	// Restoring navigates, which is not Setup's job: do it once set up.
	c.Post(p.restore)
	for _, err := range []error{
		kernel.Contribute(c, shell.Strip, "tabs.strip", 0, shell.View{Render: p.strip, Grow: true}),
		kernel.Contribute(c, shell.Overlays, "tabs.dialogs", 0, shell.View{Render: p.dialogs}),
		kernel.Contribute(c, settings.Sections, "tabs.settings", 10, settings.Section{
			Title: "Tabs", Description: "How tabs open and how projects look.", Render: p.section}),
		kernel.Contribute(c, kernel.Commands, "tabs.new", 0, kernel.Command{
			Title: "New Chat", Menu: "File", Keys: []string{"cmd+n"}, Run: p.newChat}),
		kernel.Contribute(c, kernel.Commands, "tabs.close", 0, kernel.Command{
			Title: "Close Tab", Menu: "File", Keys: []string{"cmd+w"},
			Enabled: func() bool { return p.active != "" }, Run: func() { p.Close(p.active) }}),
		kernel.Contribute(c, kernel.Commands, "tabs.next", 0, kernel.Command{
			Title: "Show Next Tab", Menu: "View", Keys: []string{"ctrl+tab"},
			Enabled: func() bool { return len(p.tabs) > 1 }, Run: func() { p.step(1) }}),
		kernel.Contribute(c, kernel.Commands, "tabs.previous", 0, kernel.Command{
			Title: "Show Previous Tab", Menu: "View", Keys: []string{"ctrl+shift+tab"},
			Enabled: func() bool { return len(p.tabs) > 1 }, Run: func() { p.step(-1) }}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// ---- tabs.Service

func (p *Plugin) Tabs() []tabs.Tab {
	out := make([]tabs.Tab, len(p.tabs))
	for i, t := range p.tabs {
		t.State = p.state(t)
		out[i] = t
	}
	return out
}

func (p *Plugin) Active() (tabs.Tab, bool) {
	i := p.find(p.active)
	if i < 0 {
		return tabs.Tab{}, false
	}
	t := p.tabs[i]
	t.State = p.state(t)
	return t, true
}

func (p *Plugin) Select(id string) {
	i := p.find(id)
	if i < 0 || id == p.active {
		return
	}
	t := p.tabs[i]
	p.selecting = true
	switch t.Kind {
	case tabs.Conversation:
		s := agent.Session{ID: t.Session, Title: t.Title, Directory: t.Folder}
		if conv, ok := p.ws.Lookup(t.Session); ok {
			s = conv.Session()
		}
		p.chat.Open(s)
		p.host.ShowPage(chat.PageID)
	case tabs.Draft:
		p.chat.NewChat(t.Folder)
		p.host.ShowPage(chat.PageID)
	case tabs.Page:
		p.host.ShowPage(t.Page)
	}
	p.selecting = false
	p.activate(t.ID)
}

func (p *Plugin) Close(id string) {
	i := p.find(id)
	if i < 0 {
		return
	}
	closed := p.tabs[i]
	shown := p.visible()
	at := slices.IndexFunc(shown, func(t tabs.Tab) bool { return t.ID == id })
	p.tabs = slices.Delete(p.tabs, i, i+1)
	delete(p.unsent, id)
	if p.lastIn[closed.Folder] == id {
		delete(p.lastIn, closed.Folder)
	}
	if id != p.active {
		p.changed()
		return
	}
	p.active = ""
	shown = slices.Delete(shown, at, at+1)
	if len(shown) == 0 || (closed.Kind != tabs.Page && !slices.ContainsFunc(shown, func(t tabs.Tab) bool { return t.Kind != tabs.Page })) {
		// The space's last tab gives way to a new chat in its folder;
		// following the chat makes that the new tab.
		folder := closed.Folder
		if closed.Kind == tabs.Page {
			folder = p.space
		}
		p.chat.NewChat(folder)
		p.host.ShowPage(chat.PageID)
		if p.active == "" {
			p.showConversation(p.chat.Current())
		}
		return
	}
	p.Select(shown[min(at, len(shown)-1)].ID)
}

func (p *Plugin) Move(id string, index int) {
	i := p.find(id)
	if i < 0 {
		return
	}
	t := p.tabs[i]
	p.tabs = slices.Delete(p.tabs, i, i+1)
	var group []int
	for j, o := range p.tabs {
		if o.Folder == t.Folder {
			group = append(group, j)
		}
	}
	var at int
	switch {
	case len(group) == 0:
		at = min(i, len(p.tabs))
	case index < len(group):
		at = group[max(index, 0)]
	default:
		at = group[len(group)-1] + 1
	}
	p.tabs = slices.Insert(p.tabs, at, t)
	p.changed()
}

func (p *Plugin) Space() string { return p.space }

// step selects the tab of the space by off from the active one, wrapping
// around.
func (p *Plugin) step(off int) {
	shown := p.visible()
	if len(shown) == 0 {
		return
	}
	i := max(slices.IndexFunc(shown, func(t tabs.Tab) bool { return t.ID == p.active }), 0)
	p.Select(shown[(i+off+len(shown))%len(shown)].ID)
}

// visible is what the strip shows: the space's tabs, then the pages.
func (p *Plugin) visible() []tabs.Tab {
	var mine, pages []tabs.Tab
	for _, t := range p.tabs {
		switch {
		case t.Kind == tabs.Page:
			pages = append(pages, t)
		case t.Folder == p.space:
			mine = append(mine, t)
		}
	}
	return append(mine, pages...)
}

// showSpace shows a folder's tabs: the one shown there last, else its first,
// else a new chat in it.
func (p *Plugin) showSpace(folder string) {
	if id := p.lastIn[folder]; p.find(id) >= 0 {
		p.Select(id)
		return
	}
	for _, t := range p.tabs {
		if t.Kind != tabs.Page && t.Folder == folder {
			p.Select(t.ID)
			return
		}
	}
	p.openDraft(folder)
}

// ---- Following navigation

func (p *Plugin) followChat(conv chat.Conversation) {
	if !p.restored || p.selecting {
		return
	}
	if page := p.host.Page(); page != "" && page != chat.PageID {
		return
	}
	p.showConversation(conv)
}

func (p *Plugin) followPage(id string) {
	if !p.restored || p.selecting {
		return
	}
	if id == "" || id == chat.PageID {
		p.showConversation(p.chat.Current())
		return
	}
	tid := pageID(id)
	if p.find(tid) >= 0 {
		p.activate(tid)
		return
	}
	p.insert(tabs.Tab{ID: tid, Kind: tabs.Page, Page: id, Title: p.pageTitle(id)})
}

// showConversation makes the conversation the chat shows the active tab.
// A new chat takes over the active draft tab, as does the first stored
// conversation shown from one: sending a draft turns its tab into the
// conversation, and so does opening one from an empty tab. A draft with
// unsent text keeps its tab, and the conversation opens in a new one.
func (p *Plugin) showConversation(conv chat.Conversation) {
	draft := p.find(p.active)
	if draft >= 0 && p.tabs[draft].Kind != tabs.Draft {
		draft = -1
	}
	if conv.Session == "" {
		if draft < 0 {
			p.insert(tabs.Tab{ID: p.draftID(), Kind: tabs.Draft, Folder: conv.Directory, Title: newChatTitle})
			return
		}
		if p.tabs[draft].Folder != conv.Directory {
			p.tabs[draft].Folder = conv.Directory
			p.regroup(draft)
			p.activate(p.active) // the space follows the draft's folder
			return
		}
		p.changed()
		return
	}
	if _, ok := p.ws.Lookup(conv.Session); ok {
		p.recorded[conv.Session] = true
	}
	id := conversationID(conv.Session)
	if i := p.find(id); i >= 0 {
		if conv.Title != "" {
			p.tabs[i].Title = conv.Title
		}
		p.activate(id)
		return
	}
	t := tabs.Tab{ID: id, Kind: tabs.Conversation, Session: conv.Session, Folder: conv.Directory, Title: p.titleOf(conv)}
	// A draft the user left text in keeps its tab.
	if draft < 0 || p.unsent[p.tabs[draft].ID] != "" {
		p.insert(t)
		return
	}
	p.ensureColor(t.Folder)
	delete(p.unsent, p.tabs[draft].ID)
	p.tabs[draft] = t
	p.regroup(draft)
	p.activate(id)
}

// started gives a conversation created after the user moved on its own tab,
// in the background.
func (p *Plugin) started(conv chat.Conversation) {
	if !p.restored || p.find(conversationID(conv.Session)) >= 0 {
		return
	}
	active := p.active
	p.insert(tabs.Tab{ID: conversationID(conv.Session), Kind: tabs.Conversation, Session: conv.Session,
		Folder: conv.Directory, Title: p.titleOf(conv)})
	p.active = active
	p.changed()
}

// ---- Restoring and the workspace record

// restore reopens the tabs of the last run once the workspace record is
// read, dropping conversations it no longer lists and pages no plugin
// contributes. What the chat shows by then wins over the saved active tab.
func (p *Plugin) restore() {
	if p.restored || !p.ws.Loaded() {
		return
	}
	p.restored = true
	saved, active := p.saved.Get(), p.savedActive.Get()
	if !p.reopen.Get() {
		saved = nil
	}
	activeID := ""
	for i, s := range saved {
		var t tabs.Tab
		switch s.Kind {
		case tabs.Conversation:
			conv, ok := p.ws.Lookup(s.Session)
			if !ok || p.find(conversationID(s.Session)) >= 0 {
				continue
			}
			p.recorded[s.Session] = true
			t = tabs.Tab{ID: conversationID(s.Session), Kind: tabs.Conversation, Session: s.Session,
				Folder: conv.Folder, Title: titleOr(conv.Title, s.Title)}
		case tabs.Draft:
			t = tabs.Tab{ID: p.draftID(), Kind: tabs.Draft, Folder: s.Folder, Title: newChatTitle}
		case tabs.Page:
			if !p.hasPage(s.Page) || p.find(pageID(s.Page)) >= 0 {
				continue
			}
			t = tabs.Tab{ID: pageID(s.Page), Kind: tabs.Page, Page: s.Page, Title: p.pageTitle(s.Page)}
		default:
			continue
		}
		p.ensureColor(t.Folder)
		p.tabs = append(p.tabs, t)
		if i == active {
			activeID = t.ID
		}
	}
	if activeID == "" && len(p.tabs) > 0 {
		activeID = p.tabs[0].ID
	}
	if conv := p.chat.Current(); conv.Session != "" || activeID == "" {
		p.showConversation(conv)
		return
	}
	p.Select(activeID)
	p.changed()
}

// recordChanged restores once the record is read, then keeps titles in step
// and closes the tabs of conversations the record stopped listing.
func (p *Plugin) recordChanged() {
	if !p.restored {
		p.restore()
		return
	}
	var gone []string
	for i, t := range p.tabs {
		if t.Kind != tabs.Conversation {
			continue
		}
		conv, ok := p.ws.Lookup(t.Session)
		switch {
		case ok:
			p.recorded[t.Session] = true
			if conv.Title != "" {
				p.tabs[i].Title = conv.Title
			}
		case p.recorded[t.Session]:
			gone = append(gone, t.ID)
		}
	}
	for _, id := range gone {
		p.Close(id)
	}
	p.changed()
}

// pagesChanged closes the tabs of pages no plugin contributes any more, and
// keeps the others' titles current.
func (p *Plugin) pagesChanged() {
	if !p.restored {
		return
	}
	var gone []string
	retitled := false
	for i, t := range p.tabs {
		switch {
		case t.Kind != tabs.Page:
		case !p.hasPage(t.Page):
			gone = append(gone, t.ID)
		case p.pageTitle(t.Page) != t.Title:
			p.tabs[i].Title, retitled = p.pageTitle(t.Page), true
		}
	}
	for _, id := range gone {
		p.Close(id)
	}
	if retitled {
		p.changed()
	}
}

// ---- What conversations are doing

func (p *Plugin) events(batch []agent.Event) {
	moved := false
	set := func(s agent.SessionID, st tabs.State) {
		if p.states[s] != st {
			p.states[s] = st
			moved = true
		}
	}
	for _, e := range batch {
		if e.Session == "" {
			continue
		}
		switch e.Kind {
		case agent.TurnStarted, agent.TextStarted, agent.TextDelta, agent.ToolStarted, agent.ToolCalled, agent.ToolFinished:
			set(e.Session, tabs.Working)
		case agent.ApprovalRequested, agent.InteractionRequested:
			set(e.Session, tabs.Waiting)
		case agent.TurnSucceeded, agent.TurnFailed:
			if p.onShow(e.Session) {
				set(e.Session, tabs.Idle)
			} else {
				set(e.Session, tabs.Unread)
			}
		case agent.TurnInterrupted:
			set(e.Session, tabs.Idle)
		case agent.SessionRenamed:
			if i := p.find(conversationID(e.Session)); i >= 0 && e.Title != "" && p.tabs[i].Title != e.Title {
				p.tabs[i].Title = e.Title
				moved = true
			}
		case agent.TextEnded, agent.ModelChanged:
		}
	}
	if moved {
		p.changed()
	}
}

// onShow reports that the user is looking at the conversation's tab.
func (p *Plugin) onShow(s agent.SessionID) bool {
	page := p.host.Page()
	return (page == "" || page == chat.PageID) && p.chat.Current().Session == s
}

func (p *Plugin) state(t tabs.Tab) tabs.State {
	if t.Kind != tabs.Conversation {
		return tabs.Idle
	}
	return p.states[t.Session]
}

// ---- The list

func (p *Plugin) find(id string) int {
	if id == "" {
		return -1
	}
	return slices.IndexFunc(p.tabs, func(t tabs.Tab) bool { return t.ID == id })
}

// activate makes the tab the active one; a finished turn is seen once its
// tab is, and the text left in its composer comes back.
func (p *Plugin) activate(id string) {
	p.active = id
	if i := p.find(id); i >= 0 {
		t := p.tabs[i]
		if p.states[t.Session] == tabs.Unread {
			p.states[t.Session] = tabs.Idle
		}
		if t.Kind != tabs.Page {
			p.space, p.lastIn[t.Folder] = t.Folder, t.ID
			p.chatTab = t.ID
			if text, ok := p.unsent[t.ID]; ok {
				delete(p.unsent, t.ID)
				p.chat.SetDraft(text)
			}
		}
	}
	p.changed()
}

// leaving keeps the text the chat leaves in a tab's composer.
func (p *Plugin) leaving(u chat.Unsent) {
	id := p.chatTab
	if u.Conversation.Session != "" {
		id = conversationID(u.Conversation.Session)
	}
	if p.find(id) < 0 {
		return
	}
	if u.Text == "" {
		delete(p.unsent, id)
		return
	}
	p.unsent[id] = u.Text
}

// insert adds t and makes it active: beside the active tab when that is in
// t's group, else after the group's last tab, else at the end.
func (p *Plugin) insert(t tabs.Tab) {
	p.ensureColor(t.Folder)
	p.tabs = slices.Insert(p.tabs, p.slot(t.Folder, -1), t)
	p.activate(t.ID)
}

// regroup moves the tab at i to its group, after the folder changed.
func (p *Plugin) regroup(i int) {
	t := p.tabs[i]
	p.tabs = slices.Delete(p.tabs, i, i+1)
	p.tabs = slices.Insert(p.tabs, p.slot(t.Folder, i), t)
}

// slot is where a tab of the folder goes; fallback, when not negative, is
// used for a folder with no tabs yet.
func (p *Plugin) slot(folder string, fallback int) int {
	if i := p.find(p.active); i >= 0 && p.tabs[i].Folder == folder {
		return i + 1
	}
	last := -1
	for i, t := range p.tabs {
		if t.Folder == folder {
			last = i
		}
	}
	switch {
	case last >= 0:
		return last + 1
	case fallback >= 0:
		return min(fallback, len(p.tabs))
	}
	return len(p.tabs)
}

// changed saves the tabs and says so.
func (p *Plugin) changed() {
	keep := make([]saved, 0, len(p.tabs))
	active := 0
	for i, t := range p.tabs {
		if t.ID == p.active {
			active = i
		}
		keep = append(keep, saved{Kind: t.Kind, Session: t.Session, Folder: t.Folder, Page: t.Page, Title: t.Title})
	}
	p.saved.Set(keep)
	p.savedActive.Set(active)
	kernel.Emit(p.ctx, tabs.Changed, struct{}{})
}

// ensureColor gives a folder the first project color no other folder has.
func (p *Plugin) ensureColor(folder string) {
	colors := p.colors.Get()
	if _, ok := colors[folder]; ok || folder == "" {
		return
	}
	var used [len(native.Palette{}.Projects)]bool
	for _, i := range colors {
		if i >= 0 && i < len(used) {
			used[i] = true
		}
	}
	pick := len(colors) % len(used)
	if i := slices.Index(used[:], false); i >= 0 {
		pick = i
	}
	next := make(map[string]int, len(colors)+1)
	for k, v := range colors {
		next[k] = v
	}
	next[folder] = pick
	p.colors.Set(next)
}

func (p *Plugin) draftID() string {
	p.drafts++
	return "draft:" + strconv.Itoa(p.drafts)
}

func (p *Plugin) titleOf(conv chat.Conversation) string {
	if c, ok := p.ws.Lookup(conv.Session); ok && c.Title != "" {
		return c.Title
	}
	return titleOr(conv.Title, "")
}

func (p *Plugin) hasPage(id string) bool {
	return slices.ContainsFunc(kernel.Items(p.ctx, shell.Pages), func(e kernel.Entry[shell.Page]) bool { return e.ID == id })
}

func (p *Plugin) pageTitle(id string) string {
	title := ""
	for _, e := range kernel.Items(p.ctx, shell.Pages) {
		if e.ID == id && e.Value.Title != nil {
			kernel.Invoke(p.ctx, e, func(pg shell.Page) { title = pg.Title() })
		}
	}
	return titleOr(title, "Page")
}

func titleOr(title, fallback string) string {
	switch {
	case title != "":
		return title
	case fallback != "":
		return fallback
	}
	return newChatTitle
}

func conversationID(s agent.SessionID) string { return "chat:" + string(s) }
func pageID(id string) string                 { return "page:" + id }
