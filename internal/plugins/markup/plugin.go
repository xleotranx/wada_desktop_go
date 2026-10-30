// Package markup lets users draw numbered boxes, arrows, pins, freehand
// lines and highlights on a pending screenshot, write a note for each, and
// black out what the agent should not see. The marked-up image replaces the
// pending attachment, and the notes go with it to the agent as words tied
// to the marks' numbers. Source files remain untouched, and the original
// and the marks are kept, so the image can be marked up again until it is
// sent.
package markup

import (
	"context"
	"image"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// canvasFocus is the canvas's value of Plugin.focus; marks' IDs are their
// notes'.
const canvasFocus = ^uint64(0)

// maxHistory bounds the undo steps kept.
const maxHistory = 100

type Plugin struct {
	ctx    *kernel.Context
	host   shell.Host
	images attachment.Service
	chat   chat.Controller
	// capture takes a screenshot; tests replace it, and it is nil where the
	// platform can't.
	capture      captureFunc
	capturing    kernel.Task
	shots        int // numbers captured screenshots
	scope        *kernel.Scope
	loading      kernel.Task
	saving       kernel.Task
	source       attachment.Image
	conversation chat.Conversation
	open         bool
	applying     bool // Replace is running, and its Changed is ours
	problem      string

	// What is being edited: the image before any marks, and the marks.
	original agent.File
	decoded  image.Image
	preview  *ui.Bitmap
	// The size the backend sends the image at, 0 when it can't say, and the
	// image at that size when it is smaller, for the model's view.
	sentW, sentH int
	sentPreview  *ui.Bitmap
	modelView    bool
	marks        []mark
	notes        map[uint64]*string
	about        string
	serial       uint64 // the last mark ID
	undos        [][]mark
	redos        [][]mark

	tool     tool
	ink      int
	selected uint64 // a mark's ID, or 0
	focus    uint64 // desired focus: canvasFocus or a mark's note
	drag     drag
	active   mark // the mark being drawn
	zoom     float32
	scrollX  float32 // image pixel at the canvas's left edge while zoomed in
	scrollY  float32

	// kept holds the editing state of images replaced with a marked-up copy,
	// by image ID, while they wait to be sent.
	kept map[uint64]kept
}

type kept struct {
	revision uint64
	original agent.File
	marks    []mark
	notes    map[uint64]string
	about    string
	serial   uint64
}

type dragMode uint8

const (
	idle dragMode = iota
	creating
	moving
	resizing
	panning
)

// drag is a press on the canvas being followed until its release.
type drag struct {
	mode             dragMode
	start            point   // where it began, on the image
	x, y             float32 // where it began, on the canvas
	before           []mark  // the marks when it began, for undo
	index, corner    int
	scrollX, scrollY float32
}

type decodedImage struct {
	Image        image.Image
	Preview      *ui.Bitmap
	SentW, SentH int
	SentPreview  *ui.Bitmap
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "markup", Title: "Screenshot markup",
		Description: "Mark up attached screenshots with numbered notes, and hide parts of them, before sending.",
		Requires:    []kernel.Named{shell.HostKey, attachment.ServiceKey, chat.ControllerKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	p.images = kernel.Require(c, attachment.ServiceKey)
	if p.capture == nil {
		p.capture = screencapture()
	}
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.kept = map[uint64]kept{}
	kernel.On(c, chat.Changed, func(conv chat.Conversation) {
		if p.open && (conv.Session != p.conversation.Session || conv.Directory != p.conversation.Directory) {
			p.close()
		}
	})
	kernel.On(c, attachment.Changed, func(struct{}) {
		if p.applying {
			return
		}
		for id, k := range p.kept {
			if current, ok := p.images.Snapshot(id); !ok || current.Revision != k.revision {
				delete(p.kept, id)
			}
		}
		if p.open {
			if current, ok := p.images.Snapshot(p.source.ID); !ok || current.Revision != p.source.Revision {
				p.close()
			}
		}
	})
	kernel.Contribute(c, shell.Overlays, "markup.editor", 60, shell.View{Render: p.editor})
	if p.capture != nil {
		// A capture cut short by the plugin going away still gives the
		// window back.
		c.OnDispose(func() {
			if p.capturing.Running() {
				p.host.SetHidden(false)
			}
		})
		kernel.Contribute(c, kernel.Commands, "markup.capture", 21, kernel.Command{
			Title: "Capture Screenshot…", Menu: "Chat", Keys: []string{"cmd+shift+2"}, Global: "ctrl+cmd+shift+2",
			Run: p.captureScreen})
		kernel.Contribute(c, chat.ComposerAccessory, "markup.capture", -11, shell.View{Render: p.captureButton})
	}
	return kernel.Contribute(c, attachment.Actions, "markup.open", 0, attachment.Action{Title: "Mark up", Run: p.begin})
}

// begin opens the editor on a pending image, with its marks and notes when
// it was marked up before.
func (p *Plugin) begin(id uint64) {
	source, ok := p.images.Snapshot(id)
	if !ok {
		return
	}
	p.close()
	p.scope = p.ctx.NewScope()
	p.source, p.conversation, p.open = source, p.chat.Current(), true
	p.tool, p.focus, p.original = drawBox, canvasFocus, source.File
	p.notes = map[uint64]*string{}
	if k, ok := p.kept[id]; ok && k.revision == source.Revision {
		p.original, p.marks, p.about, p.serial = k.original, slices.Clone(k.marks), k.about, k.serial
		for id, note := range k.notes {
			p.notes[id] = &note
		}
	}
	original, model := p.original, p.conversation.Model
	images, sizes := kernel.Use(p.ctx, agent.ImagesKey)
	kernel.Go(p.scope, &p.loading, func(ctx context.Context) (decodedImage, error) {
		img, err := decode(original.Data)
		if err != nil {
			return decodedImage{}, err
		}
		out := decodedImage{Image: img, Preview: ui.NewBitmap(img)}
		if sizes {
			// Without the size sent, the editor offers what it can without it.
			w, h := img.Bounds().Dx(), img.Bounds().Dy()
			if sw, sh, err := images.Fit(ctx, model, original); err == nil && sw > 0 && sh > 0 && sw <= w && sh <= h {
				out.SentW, out.SentH = sw, sh
				if sw < w {
					out.SentPreview = ui.NewBitmap(downscale(img, sw, sh))
				}
			}
		}
		return out, nil
	}, func(img decodedImage, err error) {
		if !p.open {
			p.close()
			return
		}
		if err != nil {
			p.problem = "Could not open this image: " + err.Error()
			return
		}
		p.decoded, p.preview = img.Image, img.Preview
		p.sentW, p.sentH, p.sentPreview = img.SentW, img.SentH, img.SentPreview
	})
}

func (p *Plugin) close() {
	p.loading.Cancel()
	p.saving.Cancel()
	if p.scope != nil {
		p.scope.Dispose()
		p.scope = nil
	}
	p.open = false
	p.source, p.original = attachment.Image{}, agent.File{}
	p.decoded, p.preview = nil, nil
	p.sentW, p.sentH, p.sentPreview, p.modelView = 0, 0, nil, false
	p.marks, p.notes, p.about, p.serial, p.undos, p.redos = nil, nil, "", 0, nil, nil
	p.selected, p.focus, p.drag, p.active = 0, 0, drag{}, mark{}
	p.zoom, p.scrollX, p.scrollY = 0, 0, 0
	p.problem = ""
}

func (p *Plugin) busy() bool { return !p.open || p.decoded == nil || p.saving.Running() }

// record saves the marks before a change, for undo.
func (p *Plugin) record(before []mark) {
	p.undos = append(p.undos, before)
	if len(p.undos) > maxHistory {
		p.undos = p.undos[1:]
	}
	p.redos = nil
}

func (p *Plugin) undo() {
	if p.busy() || p.drag.mode != idle || len(p.undos) == 0 {
		return
	}
	p.redos = append(p.redos, p.marks)
	p.marks, p.undos = p.undos[len(p.undos)-1], p.undos[:len(p.undos)-1]
	p.keepSelection()
}

func (p *Plugin) redo() {
	if p.busy() || p.drag.mode != idle || len(p.redos) == 0 {
		return
	}
	p.undos = append(p.undos, p.marks)
	p.marks, p.redos = p.redos[len(p.redos)-1], p.redos[:len(p.redos)-1]
	p.keepSelection()
}

func (p *Plugin) keepSelection() {
	if p.index(p.selected) < 0 {
		p.selected = 0
	}
}

func (p *Plugin) index(id uint64) int {
	return slices.IndexFunc(p.marks, func(m mark) bool { return m.ID == id })
}

// remove deletes a mark; its note stays for undo.
func (p *Plugin) remove(id uint64) {
	i := p.index(id)
	if p.busy() || p.drag.mode != idle || i < 0 {
		return
	}
	p.record(p.marks)
	p.marks = slices.Delete(slices.Clone(p.marks), i, i+1)
	if p.selected == id {
		p.selected = 0
	}
	if p.focus == id {
		p.focus = canvasFocus
	}
}

// setTool chooses what a press on the canvas does. A drawing tool lets go
// of the selection, so an ink chosen next is for the new mark.
func (p *Plugin) setTool(t tool) {
	p.tool = t
	if t != pick {
		p.selected = 0
	}
}

// setInk chooses the ink of new marks and recolors the selected one.
func (p *Plugin) setInk(i int) {
	p.ink = i
	if j := p.index(p.selected); j >= 0 && !p.busy() && p.marks[j].Ink != i {
		p.record(p.marks)
		p.marks = slices.Clone(p.marks)
		p.marks[j].Ink = i
	}
}

// details counts the marks sending a full-resolution part.
func (p *Plugin) details() int {
	n := 0
	for _, m := range p.marks {
		if m.Detail && m.detailable() {
			n++
		}
	}
	return n
}

// setDetail turns a mark's full-resolution part on or off, up to
// maxDetails.
func (p *Plugin) setDetail(id uint64, on bool) {
	i := p.index(id)
	if i < 0 || p.busy() || p.drag.mode != idle || !p.marks[i].detailable() || p.marks[i].Detail == on {
		return
	}
	if on && p.details() >= maxDetails {
		p.problem = "An image takes at most 4 details."
		return
	}
	p.record(p.marks)
	p.marks = slices.Clone(p.marks)
	p.marks[i].Detail = on
}

func (p *Plugin) note(id uint64) *string {
	n := p.notes[id]
	if n == nil {
		n = new(string)
		p.notes[id] = n
	}
	return n
}

// restoring reports whether the image was marked up before, so applying
// with no marks and no words puts the original back.
func (p *Plugin) restoring() bool {
	k, ok := p.kept[p.source.ID]
	return ok && k.revision == p.source.Revision
}

func (p *Plugin) canApply() bool {
	return !p.busy() && p.drag.mode == idle && (len(p.marks) > 0 || strings.TrimSpace(p.about) != "" || p.restoring())
}

// apply replaces the pending image with the marked-up one, and its notes
// with the legend. With no marks it keeps the original pixels. Only the
// palette's theme-independent inks and redaction are used.
func (p *Plugin) apply(pal native.Palette) {
	if !p.canApply() {
		return
	}
	img, marks, source, original, about := p.decoded, slices.Clone(p.marks), p.source, p.original, p.about
	notes := map[uint64]string{}
	for _, m := range marks {
		notes[m.ID] = *p.note(m.ID)
	}
	serial, words := p.serial, legend(marks, notes, about, pal.Inks)
	name := strings.TrimSuffix(original.Name, filepath.Ext(original.Name)) + ".png"
	p.problem = ""
	kernel.Go(p.scope, &p.saving, func(ctx context.Context) (exported, error) {
		if len(marks) == 0 {
			return exported{File: original, Preview: ui.NewBitmap(img)}, nil
		}
		return exportPNG(ctx, img, marks, pal, name)
	}, func(result exported, err error) {
		if !p.open {
			p.close()
			return
		}
		if err == nil {
			p.applying = true
			err = p.images.Replace(source, result.File, result.Preview, words, result.Details)
			p.applying = false
		}
		if err != nil {
			p.problem = "Could not use the marked-up image: " + err.Error()
			return
		}
		delete(p.kept, source.ID)
		if current, ok := p.images.Snapshot(source.ID); ok && (len(marks) > 0 || strings.TrimSpace(about) != "") {
			p.kept[source.ID] = kept{revision: current.Revision, original: original, marks: marks,
				notes: maps.Clone(notes), about: about, serial: serial}
		}
		p.close()
	})
}
