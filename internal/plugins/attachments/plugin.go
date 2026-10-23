// Package attachments lets the user add files to the next message: chosen
// with the paperclip or "Attach Files…", dropped on the composer, or pasted. They show
// as chips above the editor and go out with the message through
// chat.Outgoing.
package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

// Limits for one message. Files travel base64-encoded in one request.
const (
	maxFiles     = 10
	maxFileBytes = 10 << 20
	maxBytes     = 20 << 20
)

// Plugin is the attachments plugin.
type Plugin struct {
	ctx     *kernel.Context
	host    shell.Host
	pending []pending
	reading int // batches being read
	pastes  int // numbers pasted images
	picking kernel.Task
	// choose asks the user for files, off the UI thread; tests replace it.
	choose func() ([]string, error)
}

// pending is one file waiting for the next message.
type pending struct {
	path  string // its file, or "paste:<n>" for a pasted image
	file  agent.File
	thumb *ui.Bitmap // images only
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "attachments", Title: "Attachments",
		Description: "Send files and images with a message.",
		Requires:    []kernel.Named{shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	if p.choose == nil {
		p.choose = func() ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach files", ButtonLabel: "Attach", Multiple: true})
		}
	}
	// Last, so a transform that rejects the draft doesn't drop the files.
	kernel.Transform(c, chat.Outgoing, 1000, p.outgoing)
	kernel.Contribute(c, chat.ComposerAccessory, "attachments.add", -10, shell.View{Render: p.button})
	kernel.Contribute(c, chat.ComposerAttachments, "attachments.list", 0, shell.View{Render: p.list})
	kernel.Contribute(c, chat.FileDrop, "attachments.drop", 0, p.add)
	kernel.Contribute(c, chat.Paste, "attachments.paste", 0, p.paste)
	kernel.Contribute(c, kernel.Commands, "attachments.attach", 20, kernel.Command{
		Title: "Attach Files…", Menu: "Chat", Keys: []string{"cmd+shift+a"}, Run: p.pick})
	return nil
}

func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, error) {
	if p.reading > 0 {
		return d, errors.New("files are still being read")
	}
	for _, f := range p.pending {
		d.Files = append(d.Files, f.file)
	}
	p.pending = nil
	return d, nil
}

func (p *Plugin) pick() {
	kernel.Go(p.ctx, &p.picking, func(context.Context) ([]string, error) { return p.choose() },
		func(paths []string, err error) {
			if err != nil {
				p.host.Toast(ui.Toast{Title: "The file chooser did not open", Description: err.Error(), Type: components.ToastError})
				return
			}
			p.add(paths)
		})
}

// paste takes files or an image off the clipboard. Text with an image
// (as some apps copy both) pastes as text.
func (p *Plugin) paste(text string) bool {
	clip := p.host.Clipboard()
	switch {
	case len(clip.Files) > 0:
		p.add(clip.Files)
		return true
	case len(clip.PNG) > 0 && strings.TrimSpace(text) == "":
		p.pastes++
		name := "Pasted image.png"
		if p.pastes > 1 {
			name = fmt.Sprintf("Pasted image %d.png", p.pastes)
		}
		p.read([]read{{pending: pending{path: fmt.Sprintf("paste:%d", p.pastes),
			file: agent.File{Name: name, MIME: "image/png", Data: clip.PNG}}}})
		return true
	}
	return false
}

// read is one file read off the UI thread. A pasted file comes with its data.
type read struct {
	pending
	err error
}

// add reads files for the next message, skipping ones already added.
func (p *Plugin) add(paths []string) {
	var fresh []read
	for _, path := range paths {
		if path != "" && !p.has(path) && !slices.ContainsFunc(fresh, func(r read) bool { return r.path == path }) {
			fresh = append(fresh, read{pending: pending{path: path}})
		}
	}
	p.read(fresh)
}

// read loads files and makes thumbnails off the UI thread, then accepts them.
func (p *Plugin) read(batch []read) {
	if len(batch) == 0 {
		return
	}
	p.reading++
	var task kernel.Task // batches don't replace each other
	kernel.Go(p.ctx, &task, func(context.Context) ([]read, error) {
		for i := range batch {
			batch[i] = load(batch[i])
		}
		return batch, nil
	}, func(reads []read, _ error) {
		p.reading--
		p.accept(reads)
	})
}

// accept adds what was read within the limits and reports the rest.
func (p *Plugin) accept(reads []read) {
	total := 0
	for _, f := range p.pending {
		total += len(f.file.Data)
	}
	types, limited := kernel.Use(p.ctx, agent.FileTypesKey)
	var failed []string
	for _, r := range reads {
		switch {
		case r.err != nil:
			failed = append(failed, r.file.Name+": "+r.err.Error())
		case limited && !types.Accepts(r.file.MIME):
			failed = append(failed, r.file.Name+": the agent takes only "+types.Summary())
		case len(p.pending) >= maxFiles:
			failed = append(failed, fmt.Sprintf("%s: a message takes at most %d files", r.file.Name, maxFiles))
		case total+len(r.file.Data) > maxBytes:
			failed = append(failed, fmt.Sprintf("%s: a message takes at most %d MB of files", r.file.Name, maxBytes>>20))
		case p.has(r.path):
		default:
			total += len(r.file.Data)
			p.pending = append(p.pending, r.pending)
		}
	}
	if len(failed) > 0 {
		desc := failed[0]
		if len(failed) > 1 {
			desc = fmt.Sprintf("%s, and %d more", desc, len(failed)-1)
		}
		p.host.Toast(ui.Toast{Title: "Some files were not attached", Description: desc, Type: components.ToastError})
	}
}

func (p *Plugin) has(path string) bool {
	for _, f := range p.pending {
		if f.path == path {
			return true
		}
	}
	return false
}

// load reads a file unless its data came with it, types it and makes its
// thumbnail.
func load(r read) read {
	tooBig := fmt.Errorf("larger than %d MB", maxFileBytes>>20)
	if r.file.Data == nil {
		r.file.Name = filepath.Base(r.path)
		info, err := os.Stat(r.path)
		switch {
		case err != nil:
			r.err = err
		case info.IsDir():
			r.err = errors.New("folders can't be attached")
		case info.Size() > maxFileBytes:
			r.err = tooBig
		default:
			r.file.Data, r.err = os.ReadFile(r.path)
		}
		if r.err != nil {
			r.file.Data = nil
			return r
		}
		r.file.MIME = mimeOf(r.path, r.file.Data)
	}
	if len(r.file.Data) > maxFileBytes {
		r.err, r.file.Data = tooBig, nil
		return r
	}
	if strings.HasPrefix(r.file.MIME, "image/") {
		r.thumb, _ = ui.DecodeBitmap(r.file.Data) // no thumbnail for formats it can't decode
	}
	return r
}

// mimeOf types a file by its extension, then by its contents. Text stays
// text whatever the extension claims (".ts" is not a video here).
func mimeOf(path string, data []byte) string {
	typ := mime.TypeByExtension(filepath.Ext(path))
	if typ == "" {
		typ = http.DetectContentType(data)
	}
	if base, _, err := mime.ParseMediaType(typ); err == nil {
		typ = base
	}
	text := utf8.Valid(data) && !bytes.ContainsRune(data, 0)
	switch {
	case strings.HasPrefix(typ, "image/"), strings.HasPrefix(typ, "text/") && text:
		return typ
	case text:
		return "text/plain"
	}
	return typ
}

func (p *Plugin) button(c *ui.Context) {
	pal := native.PaletteFor(c)
	label := "Attach files"
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(0, 6, 0, 0).Children(func() {
		b := ui.ButtonBase(c).Label(label).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer).Tooltip(label)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
		b.OnClick(p.pick)
		b.Children(func() { ui.Icon(c, lucide.Paperclip()).FontSize(15).TextColor(pal.Muted) })
		ui.Box(c).Width(1).Height(16).Background(pal.Hairline)
	})
}

func (p *Plugin) list(c *ui.Context) {
	if len(p.pending) == 0 && p.reading == 0 {
		return
	}
	pal := native.PaletteFor(c)
	ui.Row(c).FillWidth().Wrap().Gap(6).Padding(0, 0, 6, 0).Children(func() {
		for _, f := range p.pending {
			c.Key(f.path)
			opts := onechat.AttachmentOptions{Label: f.file.Name, Detail: size(len(f.file.Data)), RemoveLabel: "Remove " + f.file.Name}
			if f.thumb != nil {
				opts.Image = f.thumb
			} else if strings.HasPrefix(f.file.MIME, "image/") {
				opts.Icon = lucide.Image
			}
			if onechat.Attachment(c, opts).Remove {
				path := f.path
				// Removal changes the list being drawn, so it waits for the frame to end.
				p.ctx.Post(func() { p.remove(path) })
			}
		}
		if p.reading > 0 {
			c.Key("reading")
			ui.Text(c, "Reading files…").FontSize(12).TextColor(pal.Muted).AlignSelf(ui.Center)
		}
	})
}

func (p *Plugin) remove(path string) {
	for i, f := range p.pending {
		if f.path == path {
			p.pending = append(p.pending[:i:i], p.pending[i+1:]...)
			return
		}
	}
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
