// Package video is a video editor beside the conversation: the HyperFrames
// compositions the agent writes in the conversation's folder, shown on a
// stage and a timeline. The user scrubs, drags clips, checks the project and
// renders it to MP4; the agent edits the same HTML files with the brief the
// composer sends.
package video

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// maxUndo is how many timing edits the editor can take back.
const maxUndo = 50

// PageID is the editor's full-window page.
const PageID = "video.page"

// videosKey is the setting that remembers the conversations that are videos.
const videosKey = "videos-v1"

// valuesKey is the setting that remembers the values the user gave a
// project's variables, by project folder.
const valuesKey = "values-v1"

// renderKey is the setting that remembers the render's format and quality.
const renderKey = "render-v1"

// panelKind is the sidebar's tab.
type panelKind uint8

const (
	scenesPanel panelKind = iota
	clipPanel
	varsPanel
	checkPanel
)

// video remembers a conversation in the video editor: the project folder the
// agent writes in, under the backend origin it was made in.
type video struct {
	Origin  string
	Session agent.SessionID
	Dir     string
}

// Plugin owns the editor, its preview page and which conversations are videos.
type Plugin struct {
	ctx        *kernel.Context
	navigation *kernel.Scope
	agent      agent.Service
	ws         workspace.Service
	chat       chat.Controller
	host       shell.Host

	videos   []video
	draft    bool   // the new chat on show was made with New Video
	draftDir string // and this is its project's folder
	making   kernel.Task
	opening  string
	starts   map[uint64]video // videos awaiting a session, by the source chat's DraftID

	open       bool   // the editor shows beside the conversation
	folder     string // the conversation's folder
	dir        string // the project's folder
	project    *Project
	check      check // what the Check panel shows: the editor's, or the CLI's
	quick      check // the editor's own check, kept while the CLI's shows
	cliChecked bool
	loadErr    string
	scene      *Scene // the scene the timeline edits; nil for the root

	web      shell.WebView
	webDir   string    // the folder the page shows
	webMTime time.Time // the root's time when it loaded
	info     previewInfo
	webErr   string

	playhead float64
	playing  bool
	playAt   time.Time // when playback started
	playBase float64   // the playhead playback started from
	ticking  kernel.Task

	selected string
	panel    panelKind
	hover    string
	drag     dragState

	stage     ui.Handle
	timeline  ui.Handle
	tlWidth   float32 // the timeline's width as last drawn
	viewStart float64 // the time at the timeline's left edge
	viewZoom  float64 // 1 fits the whole composition; more magnifies
	zoom      float64
	keys      bool  // the timeline has the keyboard
	found     []ref // the conversation folder's projects, read with the project

	loading    kernel.Task
	mounting   kernel.Task
	checking   kernel.Task
	shooting   kernel.Task
	seeking    kernel.Task
	editing    kernel.Task
	adding     kernel.Task
	rendering  kernel.Task
	renderLog  []string
	renderOut  string
	renderErr  string
	renderPct  int
	renderOpts renderOptions

	undo    []clipEdit // edits the timeline made, oldest first
	redo    []clipEdit // edits undone, newest last
	text    string     // the selected clip's words, as the sidebar edits them
	textFor string     // the clip those words belong to
	shots   []shot     // the storyboard's frames
	story   bool       // the frames show over the window

	values  map[string]any            // the user's variable values for the open project
	saved   map[string]map[string]any // and by project folder, kept in the settings
	editors []*variableEditor
	apply   kernel.Task
}

// New makes the plugin.
func New() kernel.Plugin { return &Plugin{} }

// Manifest declares the editor and what it needs.
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "video", Title: "Video editor",
		Description: "Edit the HyperFrames videos the agent writes: preview, timeline, checks and render.",
		Requires:    []kernel.Named{agent.ServiceKey, workspace.ServiceKey, chat.ControllerKey, shell.HostKey}}
}

// Setup registers the editor.
func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.navigation = c.NewScope()
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	c.Settings().Get(videosKey, &p.videos)
	c.Settings().Get(valuesKey, &p.saved)
	c.Settings().Get(renderKey, &p.renderOpts)
	if p.renderOpts.Format == "" {
		p.renderOpts.Format = "mp4"
	}
	if p.renderOpts.Quality == "" {
		p.renderOpts.Quality = "standard"
	}
	p.starts = map[uint64]video{}
	c.OnDispose(func() {
		// Nothing may use the page once it is gone: stop the work that does,
		// then close it.
		p.ticking.Cancel()
		p.mounting.Cancel()
		p.shooting.Cancel()
		p.seeking.Cancel()
		p.apply.Cancel()
		p.editing.Cancel()
		p.rendering.Cancel()
		if p.web != nil {
			p.web.Close()
			p.web = nil
		}
	})
	kernel.On(c, chat.Leaving, p.leaving)
	kernel.On(c, chat.Changed, p.changed)
	kernel.On(c, chat.Started, p.started)
	kernel.On(c, agent.StatusChanged, func(agent.Status) { p.update(p.chat.Current()) })
	kernel.On(c, agent.Events, p.events)
	kernel.On(c, workspace.Changed, func(struct{}) { p.forgetGone() })
	kernel.Transform(c, chat.Outgoing, 900, p.outgoing)
	kernel.Contribute(c, chat.Drawers, "video.editor", 20, shell.View{Render: p.drawer})
	kernel.Contribute(c, chat.HideWork, "video.editor", 0, func() bool { return p.open })
	kernel.Contribute(c, shell.TitleBar, "video.new", 0, shell.View{Render: p.titleButton})
	kernel.Contribute(c, shell.Overlays, "video.storyboard", 0, shell.View{Render: p.storyView})
	if err := kernel.Contribute(c, shell.Pages, PageID, 0, shell.Page{
		Title: func() string { return "Video" }, Render: p.pageView}); err != nil {
		return err
	}
	kernel.Contribute(c, tabs.Looks, "video.tabs", 0, tabs.Look{Applies: p.isVideoTab, Icon: lucide.Clapperboard, Name: "Video",
		Draft: "New video"})
	p.update(p.chat.Current())
	if err := kernel.Contribute(c, kernel.Commands, "video.new", 0, kernel.Command{
		Title: "New Video", Menu: "File", Keys: []string{"cmd+shift+v"}, Enabled: p.canVideo, Run: p.newVideo}); err != nil {
		return err
	}
	return kernel.Contribute(c, kernel.Commands, "video.open", 0, kernel.Command{
		Title: "Open Video Editor", Menu: "File", Enabled: p.canOpenPage, Run: p.openPage})
}

// close puts the editor away until another video conversation shows, as when
// the window has no web pages.
func (p *Plugin) close() {
	p.stopPlaying()
	p.open = false
	p.selected = ""
	p.drag = dragState{}
	if p.web != nil {
		p.web.Close()
		p.web = nil
	}
	p.webDir, p.webMTime = "", time.Time{}
	p.info, p.webErr = previewInfo{}, ""
	p.project, p.check, p.quick, p.cliChecked, p.loadErr = nil, check{}, check{}, false, ""
	p.scene = nil
	p.found = nil
	p.values = nil
	p.editors = nil
	p.dir, p.playhead = "", 0
}

// follow opens another project, letting the old page go.
func (p *Plugin) follow(folder, dir string) {
	if folder == p.folder && dir == p.dir {
		return
	}
	p.navigation.Dispose()
	p.navigation = p.ctx.NewScope()
	p.loading.Cancel()
	p.mounting.Cancel()
	p.seeking.Cancel()
	p.stopPlaying()
	p.selected, p.drag = "", dragState{}
	p.undo, p.redo = nil, nil
	p.shots, p.story = nil, false
	p.scene = nil
	p.viewStart, p.viewZoom = 0, 1
	p.folder, p.dir = folder, dir
	p.project, p.check, p.loadErr = nil, check{}, ""
	p.playhead = 0
	if p.web != nil {
		p.web.Close()
		p.web = nil
	}
	p.webDir, p.webMTime = "", time.Time{}
	p.info, p.webErr = previewInfo{}, ""
	if dir != "" {
		p.load()
	}
}

// loaded is a project read off the UI thread, with what its check found and
// the other projects the conversation's folder holds.
type loaded struct {
	project *Project
	check   check
	found   []ref
}

// load reads the project and its check off the UI thread.
func (p *Plugin) load() {
	dir, folder := p.dir, p.folder
	if dir == "" {
		return
	}
	kernel.Go(p.navigation, &p.loading, func(ctx context.Context) (loaded, error) {
		project, err := openProject(dir)
		if err != nil {
			return loaded{}, err
		}
		return loaded{project: project, check: checkProject(project), found: discover(folder)}, nil
	}, func(l loaded, err error) {
		if err != nil {
			p.project, p.loadErr = nil, err.Error()
			p.check = check{}
			p.host.Toast(ui.Toast{Title: "The video did not open", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.loadErr = ""
		p.project, p.check, p.quick, p.found = l.project, l.check, l.check, l.found
		p.cliChecked = false
		p.values = p.saved[p.dir]
		p.pruneFocus()
		p.pruneSelection()
		if c, ok := p.clip(p.selected); ok && p.textFor != p.selected {
			p.text, p.textFor = c.Text, p.selected
		}
		p.clampView()
		p.buildEditors()
		p.mount()
	})
}

// mount shows the project in the preview page, creating the page or loading
// the root again when the project's files changed.
func (p *Plugin) mount() {
	project := p.project
	if project == nil {
		return
	}
	if p.web == nil {
		web, err := p.host.NewWebView()
		if err != nil {
			p.webErr = err.Error()
			p.host.Toast(ui.Toast{Title: "The editor can't show pages", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.web = web
	}
	web, dir := p.web, p.dir
	root := projectFile(p.dir, project.File)
	mtime := projectTime(project)
	if p.webDir != dir || !mtime.Equal(p.webMTime) {
		p.webDir, p.webMTime = dir, mtime
		if err := web.LoadFile(root); err != nil {
			p.webErr = err.Error()
			return
		}
	}
	p.webErr = ""
	script, err := mountScript(project, p.values)
	if err != nil {
		p.webErr = err.Error()
		return
	}
	kernel.Go(p.navigation, &p.mounting, func(ctx context.Context) (previewInfo, error) {
		if err := waitReady(ctx, web, root, 10*time.Second); err != nil {
			return previewInfo{}, err
		}
		if err := ctx.Err(); err != nil {
			return previewInfo{}, err
		}
		result, err := web.Eval(ctx, script)
		if err != nil {
			return previewInfo{}, err
		}
		return decodeInfo(result)
	}, func(info previewInfo, err error) {
		if err != nil {
			p.webErr = err.Error()
			return
		}
		p.info = info
		p.seek(p.playhead)
	})
}

// newScene appends a scene of four seconds to the project and shows it.
func (p *Plugin) newScene() {
	if p.project == nil || p.dir == "" || p.adding.Running() {
		return
	}
	dir, project := p.dir, p.project
	kernel.Go(p.navigation, &p.adding, func(ctx context.Context) (string, error) {
		return addScene(dir, project, 4)
	}, func(id string, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The scene was not added", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.selectClip(id)
		p.reload()
	})
}

// reload reads the project again and shows its first frame back at the
// playhead.
func (p *Plugin) reload() {
	p.loading.Cancel()
	p.mounting.Cancel()
	p.load()
}

// pruneFocus puts the timeline back on the root when its scene is gone.
func (p *Plugin) pruneFocus() {
	if p.scene == nil || p.project == nil {
		p.scene = nil
		return
	}
	for _, s := range p.project.Scenes {
		if s.Host != nil && p.scene.Host != nil && s.Host.ID == p.scene.Host.ID {
			p.scene = s
			return
		}
	}
	p.scene = nil
}

// focusOn edits one scene's clips on the timeline instead of the root's.
func (p *Plugin) focusOn(s *Scene) {
	if s == nil || s.Comp == nil {
		return
	}
	p.scene = s
	p.selected = ""
	p.drag = dragState{}
	p.viewStart, p.viewZoom = 0, 1
	p.buildEditors()
	if s.Host != nil {
		p.seek(s.Host.Start)
	}
}

// unfocus puts the timeline back on the root composition.
func (p *Plugin) unfocus() {
	p.scene = nil
	p.selected = ""
	p.drag = dragState{}
	p.viewStart, p.viewZoom = 0, 1
	p.buildEditors()
}

// pruneSelection forgets a clip the project no longer has.
func (p *Plugin) pruneSelection() {
	if p.selected == "" || p.project == nil {
		return
	}
	if _, ok := p.clip(p.selected); !ok {
		p.selected, p.text, p.textFor = "", "", ""
	}
}

// selectClip selects a clip and takes its words into the sidebar.
func (p *Plugin) selectClip(id string) {
	p.selected = id
	if c, ok := p.clip(id); ok {
		p.text, p.textFor = c.Text, id
		return
	}
	p.text, p.textFor = "", id
}

// saveText writes the selected clip's words into its file and takes an undo
// step for them.
func (p *Plugin) saveText(c *Clip) {
	if c == nil || !c.TextEditable || p.dir == "" || c.ID != p.selected {
		return
	}
	if p.text == c.Text {
		return
	}
	p.rememberText(c)
	dir, file, id, text := p.dir, c.File, c.ID, p.text
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writeClipText(dir, file, id, text)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The words did not change", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.textFor = id
		p.reload()
	})
}

// clip finds a clip of the project by id.
func (p *Plugin) clip(id string) (*Clip, bool) {
	if p.project == nil {
		return nil, false
	}
	if c := clipIn(p.project.Root, id); c != nil {
		return c, true
	}
	for _, s := range p.project.Scenes {
		if c := clipIn(s.Comp, id); c != nil {
			return c, true
		}
	}
	return nil, false
}

// focusClip shows the composition a clip belongs to, selects it and moves
// the playhead to its start.
func (p *Plugin) focusClip(id string) {
	if p.project == nil || id == "" {
		return
	}
	if c := clipIn(p.project.Root, id); c != nil {
		p.unfocus()
		p.selectClip(id)
		p.seek(c.Start)
		return
	}
	for _, s := range p.project.Scenes {
		if c := clipIn(s.Comp, id); c != nil {
			p.focusOn(s)
			p.selectClip(id)
			p.seek(c.Start)
			return
		}
	}
}

// clipIn finds a clip of a composition.
func clipIn(comp *Composition, id string) *Clip {
	if comp == nil {
		return nil
	}
	for _, c := range comp.Clips {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// seek shows the frame at t, and drives playback when it is on.
func (p *Plugin) seek(t float64) {
	if p.project == nil {
		return
	}
	if t < 0 {
		t = 0
	}
	if d := p.duration(); d > 0 && t > d {
		t = d
	}
	p.playhead = t
	if p.playing {
		p.playBase, p.playAt = t, time.Now()
	}
	if p.web == nil || p.webErr != "" {
		return
	}
	web := p.web
	kernel.Go(p.navigation, &p.seeking, func(ctx context.Context) (float64, error) {
		result, err := web.Eval(ctx, seekScript(t))
		if err != nil {
			return 0, err
		}
		if v, ok := result.(float64); ok {
			return v, nil
		}
		return t, nil
	}, func(at float64, err error) {
		if err != nil {
			p.webErr = err.Error()
			return
		}
		if !p.playing {
			p.playhead = at
		}
	})
}

// duration is the video's length.
func (p *Plugin) duration() float64 {
	if p.project == nil {
		return 0
	}
	return p.project.Duration()
}

// fps is the video's frame rate.
func (p *Plugin) fps() float64 {
	if p.project == nil || p.project.FPS <= 0 {
		return 30
	}
	return p.project.FPS
}

// togglePlay starts or stops preview playback, which the editor drives by
// seeking: the page never plays itself.
func (p *Plugin) togglePlay() {
	if p.playing {
		p.stopPlaying()
		return
	}
	if p.project == nil || p.web == nil || p.webErr != "" || p.duration() <= 0 {
		return
	}
	if p.playhead >= p.playLimit() || p.playhead < p.startOfSpan() {
		p.playhead = p.startOfSpan()
	}
	p.playing = true
	p.playBase = p.playhead
	p.playAt = time.Now()
	p.tick()
}

// stopPlaying stops the preview where it is.
func (p *Plugin) stopPlaying() {
	if !p.playing {
		return
	}
	p.playing = false
	p.ticking.Cancel()
}

// tick shows the frame for the time since the last one, one frame per turn:
// the page is asked from a worker, and the frame it answered is drawn on the
// UI thread before the next is asked.
func (p *Plugin) tick() {
	if !p.playing || p.web == nil {
		return
	}
	at := p.playBase + time.Since(p.playAt).Seconds()
	if limit := p.playLimit(); at >= limit {
		p.playing = false
		p.playhead = limit
		p.seek(p.playhead)
		p.ctx.Post(func() {})
		return
	}
	p.playhead = at
	web := p.web
	kernel.Go(p.ctx, &p.ticking, func(ctx context.Context) (float64, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(33 * time.Millisecond):
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, err := web.Eval(ctx, seekScript(at)); err != nil {
			return 0, err
		}
		return at, nil
	}, func(_ float64, err error) {
		if err != nil {
			p.playing = false
			p.webErr = err.Error()
			return
		}
		p.ctx.Post(func() {})
		p.tick()
	})
}

// errNoPreview is what a page that does not answer with its state reports.
var errNoPreview = errors.New("the preview page did not answer")

// decodeInfo reads what the page answered about the composition.
func decodeInfo(result any) (previewInfo, error) {
	m, ok := result.(map[string]any)
	if !ok {
		return previewInfo{}, errNoPreview
	}
	data, err := json.Marshal(m)
	if err != nil {
		return previewInfo{}, err
	}
	var info previewInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return previewInfo{}, err
	}
	return info, nil
}

// events looks again once the agent may have written the project: after a
// tool that changes files or runs commands, and when a turn ends.
func (p *Plugin) events(batch []agent.Event) {
	if !p.open || p.dir == "" {
		return
	}
	session := p.chat.Current().Session
	for _, e := range batch {
		if e.Session != session {
			continue
		}
		switch e.Kind {
		case agent.ToolFinished:
			if e.Tool != nil && e.Tool.Kind != agent.ToolRead && e.Tool.Kind != agent.ToolSearch && e.Tool.Kind != agent.ToolFetch {
				p.reload()
				return
			}
		case agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			p.reload()
			return
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ApprovalRequested, agent.InteractionRequested, agent.TurnStarted, agent.ModelChanged, agent.SessionRenamed:
		}
	}
}

// changed follows the conversation on show.
func (p *Plugin) changed(conv chat.Conversation) {
	if p.opening != "" {
		p.draft, p.draftDir = true, p.opening
	}
	p.complete(conv)
	if conv.Session != "" {
		p.draft, p.draftDir = false, ""
	}
	p.update(conv)
}

// started records the video a new video's first message created after the
// user left it.
func (p *Plugin) started(conv chat.Conversation) {
	p.complete(conv)
	p.update(p.chat.Current())
}

// complete matches a creation to the video whose first message started it.
func (p *Plugin) complete(conv chat.Conversation) {
	if v, ok := p.starts[conv.DraftID]; ok && conv.Session != "" {
		delete(p.starts, conv.DraftID)
		v.Session = conv.Session
		p.remember(v)
	}
}

// leaving ends the new video draft the chat leaves.
func (p *Plugin) leaving(left chat.Unsent) {
	conv := left.Conversation
	if conv.Session == "" && p.draft {
		p.draft, p.draftDir = false, ""
		if !conv.Starting {
			delete(p.starts, conv.DraftID)
		}
	}
	p.stopPlaying()
}

// update shows the editor beside a video and puts it away elsewhere.
func (p *Plugin) update(conv chat.Conversation) {
	dir := p.videoDir(conv)
	if conv.Directory == "" || dir == "" {
		if p.open {
			p.close()
		}
		p.follow("", "")
		return
	}
	p.open = true
	p.follow(conv.Directory, dir)
}

// forgetGone forgets the videos whose conversations the workspace record no
// longer has.
func (p *Plugin) forgetGone() {
	origin := p.agent.Status().Origin
	if origin == "" || !p.ws.Loaded() {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(p.videos), func(v video) bool {
		if v.Origin != origin {
			return false
		}
		_, ok := p.ws.Lookup(v.Session)
		return !ok
	})
	if len(kept) != len(p.videos) {
		p.videos = kept
		p.saveVideos()
	}
}

// rel names a path under the conversation's folder.
func rel(folder, path string) string {
	if folder == "" || path == "" {
		return path
	}
	if len(path) > len(folder) && path[:len(folder)] == folder {
		path = path[len(folder):]
		for len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}
		return path
	}
	return native.Tilde(path)
}
