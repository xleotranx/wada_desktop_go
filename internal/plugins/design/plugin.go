// Package design is a canvas beside the conversation for the web pages the
// agent writes in its folder. Each HTML file is an artboard and each folder
// of them a page; the artboards reload as the agent changes them. The user
// pans and zooms the canvas, comments on elements, leaves notes, adjusts the
// tweaks a page declares, presents an artboard and attaches snapshots, and
// what they say goes to the composer for the next message.
package design

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
	"slices"
	"strconv"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// maxPages is how many artboards a page shows at once, each a web page.
const maxPages = 12

// tool is what the pointer does on the canvas.
type tool uint8

const (
	selectTool  tool = iota // the pages take the pointer
	handTool                // a drag pans the canvas
	commentTool             // a click comments on the element under it
	noteTool                // a click puts a note on the canvas
	inspectTool             // highlights and selects an HTML element without activating it
)

// board is the web page of an artboard on show.
type board struct {
	web        shell.WebView
	loaded     time.Time // the file's time when it loaded
	tweaks     []tweak
	reading    kernel.Task
	applying   kernel.Task
	anchoring  kernel.Task
	rastering  kernel.Task
	raster     *ui.Bitmap
	rasterSize previewSize
}

// designsKey is the setting that remembers the conversations in design mode.
const designsKey = "conversations-v1"

// Plugin owns the canvas and its web pages, and which conversations are
// designs.
type Plugin struct {
	ctx        *kernel.Context
	navigation *kernel.Scope // work tied to the open design
	agent      agent.Service
	ws         workspace.Service
	chat       chat.Controller
	host       shell.Host
	images     attachments.Service
	systems    *kernel.Setting[map[string]string] // the design system chosen, by the conversation's folder

	designs  []design
	draft    bool              // the new chat on show was made with New Design
	draftDir string            // and this is its design's folder
	making   kernel.Task       // New Design makes the design's folder
	opening  string            // New Design is opening the chat of this design's folder
	starts   map[uint64]design // designs awaiting a session, by the source chat's DraftID
	feedback []savedFeedback

	origin   string  // backend origin of the design on show
	open     bool    // the canvas shows: the conversation on show is a design
	project  string  // the conversation's folder
	folder   string  // the design's own folder, whose artboards are listed
	found    listing // what it holds
	page     string  // the Dir of the page on show
	chosen   bool    // the user chose the page, so a newer one does not replace it
	boards   map[string]*board
	selected string // the path of the selected artboard

	previews  map[string]previewSize
	focusView *focusedView
	vp        viewport
	autoFit   bool // the canvas fits the page until the user pans or zooms
	canvas    ui.Handle
	size      [2]float32 // the canvas's size as last drawn
	fitting   bool       // a fit is posted
	tool      tool
	drag      drag
	focus     bool

	marks   []*mark // comments and notes, in order
	nextID  int
	editing int    // the mark whose card is open; 0 for none
	text    string // the card's words
	cardOn  bool

	hover, inspected     *inspectedElement
	hovering, inspecting kernel.Task

	tweaksOn   bool
	presenting string // the artboard in the present overlay

	scanning, creating, probing, snapping kernel.Task
	shots                                 int
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "design", Title: "Design canvas",
		Description: "Design with the agent on a canvas of the web pages it writes: comment, take notes, tweak and present.",
		Requires: []kernel.Named{agent.ServiceKey, workspace.ServiceKey, chat.ControllerKey, attachments.ServiceKey,
			shell.HostKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.navigation = c.NewScope()
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.images = kernel.Require(c, attachments.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.systems = kernel.NewSetting[map[string]string](c, "systems-v1", nil)
	c.Settings().Get(designsKey, &p.designs)
	p.boards = map[string]*board{}
	c.Settings().Get(feedbackKey, &p.feedback)
	p.starts = map[uint64]design{}
	p.autoFit = true
	c.OnDispose(func() {
		for _, b := range p.boards {
			b.web.Close()
		}
	})
	kernel.On(c, chat.Leaving, p.leaving)
	kernel.On(c, chat.Changed, p.changed)
	kernel.On(c, chat.Started, p.started)
	kernel.On(c, workspace.Changed, func(struct{}) { p.forgetGone() })
	kernel.On(c, agent.StatusChanged, func(agent.Status) { p.update(p.chat.Current()) })
	kernel.On(c, agent.Events, p.events)
	kernel.Transform(c, chat.Outgoing, 900, p.outgoing)
	kernel.Contribute(c, chat.Drawers, "design.canvas", 10, shell.View{Render: p.drawer})
	kernel.Contribute(c, chat.ComposerAttachments, "design.selection", 20, shell.View{Render: p.selectionChip})
	// The canvas takes the work panel's room; usage moves to the composer.
	kernel.Contribute(c, chat.HideWork, "design.canvas", 0, func() bool { return p.open })
	kernel.Contribute(c, shell.TitleBar, "design.new", 0, shell.View{Render: p.titleButton})
	kernel.Contribute(c, shell.Overlays, "design.present", 0, shell.View{Render: p.presentView})
	kernel.Contribute(c, tabs.Looks, "design.tabs", 0, tabs.Look{Applies: p.isDesignTab, Icon: lucide.Palette, Name: "Design",
		Draft: "New design"})
	p.update(p.chat.Current())
	return kernel.Contribute(c, kernel.Commands, "design.new", 0, kernel.Command{
		Title: "New Design", Menu: "File", Keys: []string{"cmd+shift+d"}, Enabled: p.canDesign, Run: p.newDesign})
}

// close puts the canvas away until another conversation shows, as when the
// window has no web pages.
func (p *Plugin) close() {
	p.clearInspection()
	p.stopPresenting()
	p.closeCard()
	p.open = false
}

// follow lists the artboards of another design's folder, and lets the
// pages go without one.
func (p *Plugin) follow(project, folder string) {
	origin := p.agent.Status().Origin
	if folder == p.folder && project == p.project && origin == p.origin {
		return
	}
	p.navigation.Dispose()
	p.navigation = p.ctx.NewScope()
	p.scanning.Cancel()
	p.probing.Cancel()
	p.snapping.Cancel()
	p.clearInspection()
	p.creating.Cancel()
	p.closeCard()
	p.stopPresenting()
	p.focusView = nil
	p.previews = nil
	p.project, p.folder, p.found, p.chosen, p.selected = project, folder, listing{}, false, ""
	p.origin = origin
	p.loadMarks()
	p.showPage("")
	if p.open && folder != "" {
		p.scan()
	}
}

// events looks again once the agent may have written an artboard: after a
// tool that changes files or runs commands, and when a turn ends.
func (p *Plugin) events(batch []agent.Event) {
	if !p.open {
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
				p.scan()
				return
			}
		case agent.TurnSucceeded, agent.TurnFailed, agent.TurnInterrupted:
			p.scan()
			return
		case agent.TextStarted, agent.TextDelta, agent.TextEnded, agent.ToolStarted, agent.ToolCalled,
			agent.ApprovalRequested, agent.InteractionRequested, agent.TurnStarted, agent.ModelChanged, agent.SessionRenamed:
		}
	}
}

// scan lists the folder off the UI thread and shows what changed.
func (p *Plugin) scan() {
	folder, project := p.folder, p.project
	if folder == "" {
		return
	}
	kernel.Go(p.navigation, &p.scanning, func(ctx context.Context) (listing, error) {
		return list(ctx, folder, project)
	}, func(l listing, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The design's artboards were not listed", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.found = l
		p.prunePreviews(l)
		next := ""
		if len(l.Pages) > 0 {
			next = l.Pages[0].Dir
		}
		if _, ok := l.page(p.page); ok && (p.chosen || p.page == next) {
			next = p.page
		} else {
			p.chosen = false
		}
		p.showPage(next)
	})
}

// current is the page on show.
func (p *Plugin) current() page {
	pg, _ := p.found.page(p.page)
	return p.previewPage(pg)
}

// shown is the artboards of the page on show that have web pages.
func (p *Plugin) shown() []artboard {
	boards := p.current().Boards
	return boards[:min(len(boards), maxPages)]
}

// choosePage shows a page the user picked.
func (p *Plugin) choosePage(dir string) {
	p.chosen = true
	p.showPage(dir)
}

// showPage shows the page in dir: its artboards get web pages, loaded or
// reloaded as their files changed, and the pages of others close.
func (p *Plugin) showPage(dir string) {
	changed := dir != p.page
	p.page = dir
	if changed {
		p.focusView = nil
		p.probing.Cancel()
		p.clearInspection()
		p.stopPresenting()
		p.closeCard()
		p.selected = ""
		p.autoFit = true
	}
	shown := p.shown()
	for path, b := range p.boards {
		if !slices.ContainsFunc(shown, func(a artboard) bool { return a.Path == path }) {
			if p.inspected != nil && p.inspected.Path == path || p.hover != nil && p.hover.Path == path {
				p.clearInspection()
			}
			if p.focusView != nil && p.focusView.path == path {
				p.focusView = nil
			}
			b.anchoring.Cancel()
			b.rastering.Cancel()
			b.web.Close()
			delete(p.boards, path)
			if p.selected == path {
				p.selected = ""
			}
		}
	}
	for _, a := range shown {
		b := p.boards[a.Path]
		if b == nil {
			web, err := p.host.NewWebView()
			if err != nil {
				p.host.Toast(ui.Toast{Title: "The canvas can't show pages", Description: err.Error(), Type: components.ToastError})
				p.close()
				return
			}
			b = &board{web: web}
			p.boards[a.Path] = b
			web.SetZoom(float64(max(1, p.vp.zoom())))
			if err := web.LoadFile(a.Path); err != nil {
				p.host.Toast(ui.Toast{Title: "The artboard did not load", Description: err.Error(), Type: components.ToastError})
			}
			b.loaded = a.Modified
			p.readTweaks(a.Path, b)
			p.refreshAnchors(a.Path)
			continue
		}
		if a.Modified.After(b.loaded) {
			b.loaded = a.Modified
			b.raster = nil
			b.rastering.Cancel()
			p.clearInspection()
			b.web.Reload()
			p.readTweaks(a.Path, b)
			p.refreshAnchors(a.Path)
		}
	}
	if p.autoFit {
		p.fit()
	}
}

func (p *Plugin) reload() {
	p.clearInspection()
	for path, b := range p.boards {
		b.raster = nil
		b.rastering.Cancel()
		b.web.Reload()
		p.readTweaks(path, b)
		p.refreshAnchors(path)
	}
	p.scan()
}

// Reduced previews scale a full-size snapshot, avoiding WebKit's minimum font
// size when page zoom is below 100%. Larger previews remain live web pages.
func (p *Plugin) setZoom() {
	for path, b := range p.boards {
		if path != p.presenting {
			k := p.vp.zoom()
			b.web.SetZoom(float64(max(1, k)))
			if k >= 1 {
				b.raster = nil // live interaction invalidates the cached preview
				b.rastering.Cancel()
			} else {
				p.renderRaster(path, false)
			}
		}
	}
}

// snapshot attaches a picture of the artboard to the next message, where
// the markup editor can draw on it.
func (p *Plugin) snapshot(path string) {
	b := p.boards[path]
	if b == nil {
		return
	}
	web := b.web
	type shot struct {
		data    []byte
		preview *ui.Bitmap
	}
	kernel.Go(p.navigation, &p.snapping, func(ctx context.Context) (shot, error) {
		data, err := web.Capture()
		if err != nil {
			return shot{}, err
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return shot{}, err
		}
		return shot{data, ui.NewBitmap(img)}, nil
	}, func(s shot, err error) {
		if err == nil {
			p.shots++
			_, err = p.images.Attach(agent.File{Name: snapshotName(p.shots), MIME: "image/png", Data: s.data}, s.preview)
		}
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The snapshot was not attached", Description: err.Error(), Type: components.ToastError})
		}
	})
}

func snapshotName(n int) string {
	if n == 1 {
		return "Design.png"
	}
	return "Design " + strconv.Itoa(n) + ".png"
}
