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
	"image"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
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
	maxFileBytes = attachment.MaxImageBytes
	maxBytes     = 20 << 20
)

// Plugin is the attachments plugin.
type Plugin struct {
	ctx     *kernel.Context
	host    shell.Host
	pending []pending
	reading int // batches being read
	pastes  int // numbers pasted images
	made    int // numbers images other plugins attached
	picking kernel.Task
	serial  uint64
	// choose asks the user for files, off the UI thread; tests replace it.
	choose func() ([]string, error)
}

// pending is one file waiting for the next message.
type pending struct {
	id, revision uint64
	path         string // its file, or "paste:<n>" for a pasted image
	file         agent.File
	thumb        *ui.Bitmap // images only
	notes        string     // what the agent should know about the image
	details      []attachment.Detail
}

// bytes is what the file and its details weigh in a message.
func (f pending) bytes() int {
	n := len(f.file.Data)
	for _, d := range f.details {
		n += len(d.File.Data)
	}
	return n
}

// files counts the files the message would send, details included.
func (p *Plugin) files() int {
	n := len(p.pending)
	for _, f := range p.pending {
		n += len(f.details)
	}
	return n
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "attachments", Title: "Attachments",
		Description: "Send files and images with a message.",
		Requires:    []kernel.Named{shell.HostKey},
		Provides:    []kernel.Named{attachment.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	kernel.Provide[attachment.Service](c, attachment.ServiceKey, p)
	if p.choose == nil {
		p.choose = func() ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach files", ButtonLabel: "Attach", Multiple: true})
		}
	}
	// After the transforms that attach files of their own: legends number
	// images among those already in the draft.
	kernel.Transform(c, chat.Outgoing, 1000, p.outgoing)
	kernel.Contribute(c, chat.ComposerAccessory, "attachments.add", -10, shell.View{Render: p.button})
	kernel.Contribute(c, chat.ComposerAttachments, "attachments.list", 0, shell.View{Render: p.list})
	kernel.Contribute(c, chat.FileDrop, "attachments.drop", 0, p.add)
	kernel.Contribute(c, chat.Paste, "attachments.paste", 0, p.paste)
	kernel.Contribute(c, kernel.Commands, "attachments.attach", 20, kernel.Command{
		Title: "Attach Files…", Menu: "Chat", Keys: []string{"cmd+shift+a"}, Run: p.pick})
	return nil
}

func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	if p.reading > 0 {
		return d, nil, errors.New("files are still being read")
	}
	images := 0
	for _, f := range d.Files {
		if strings.HasPrefix(f.MIME, "image/") {
			images++
		}
	}
	for _, f := range p.pending {
		d.Files = append(d.Files, f.file)
		if !strings.HasPrefix(f.file.MIME, "image/") {
			continue
		}
		images++
		if f.notes != "" {
			d.Text = legend(d.Text, images, f.file.Name, f.notes)
		}
		parent := images
		for _, detail := range f.details {
			images++
			d.Files = append(d.Files, detail.File)
			d.Text = legend(d.Text, images, fmt.Sprintf("%s, from image %d", detail.Name, parent), "")
		}
	}
	return d, func() {
		p.pending = nil
		p.changed()
	}, nil
}

// legend adds an image's notes to the message's words, under a line that
// names the image by its place among the message's images, as the agent
// receives them.
func legend(text string, n int, name, notes string) string {
	if text != "" {
		text += "\n\n"
	}
	text += fmt.Sprintf("[Image %d: %s]", n, name)
	if notes != "" {
		text += "\n" + notes
	}
	return text
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
	total, files := 0, p.files()
	for _, f := range p.pending {
		total += f.bytes()
	}
	types, limited := kernel.Use(p.ctx, agent.FileTypesKey)
	var failed []string
	for _, r := range reads {
		switch {
		case r.err != nil:
			failed = append(failed, r.file.Name+": "+r.err.Error())
		case limited && !types.Accepts(r.file.MIME):
			failed = append(failed, r.file.Name+": the agent takes only "+types.Summary())
		case files >= maxFiles:
			failed = append(failed, fmt.Sprintf("%s: a message takes at most %d files", r.file.Name, maxFiles))
		case total+len(r.file.Data) > maxBytes:
			failed = append(failed, fmt.Sprintf("%s: a message takes at most %d MB of files", r.file.Name, maxBytes>>20))
		case p.has(r.path):
		default:
			total += len(r.file.Data)
			files++
			p.serial++
			r.id, r.revision = p.serial, 1
			p.pending = append(p.pending, r.pending)
		}
	}
	p.changed()
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
		config, _, err := image.DecodeConfig(bytes.NewReader(r.file.Data))
		if err == nil && (config.Width > attachment.MaxImageSide || config.Height > attachment.MaxImageSide || int64(config.Width)*int64(config.Height) > attachment.MaxImagePixels) {
			r.err, r.file.Data = errors.New("images must be at most 16 megapixels and 16,384 pixels per side"), nil
			return r
		}
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
			detail := size(len(f.file.Data))
			if f.notes != "" {
				detail += " · with notes"
			}
			switch len(f.details) {
			case 0:
			case 1:
				detail += " · 1 detail"
			default:
				detail += fmt.Sprintf(" · %d details", len(f.details))
			}
			opts := onechat.AttachmentOptions{Label: f.file.Name, Detail: detail, RemoveLabel: "Remove " + f.file.Name}
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
			if f.thumb != nil {
				for _, e := range kernel.Items(p.ctx, attachment.Actions) {
					components.Button(c.Key(fmt.Sprintf("%d/%s", f.id, e.ID)), e.Value.Title, components.ButtonGhost).
						Label(e.Value.Title + " " + f.file.Name).Height(26).OnClick(func() {
						kernel.Invoke(p.ctx, e, func(a attachment.Action) { a.Run(f.id) })
					})
				}
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
			p.changed()
			return
		}
	}
}

func (p *Plugin) changed() { kernel.Emit(p.ctx, attachment.Changed, struct{}{}) }

func (p *Plugin) Snapshot(id uint64) (attachment.Image, bool) {
	for _, f := range p.pending {
		if f.id == id && strings.HasPrefix(f.file.MIME, "image/") {
			file := f.file
			file.Data = slices.Clone(file.Data)
			return attachment.Image{ID: id, Revision: f.revision, File: file, Notes: f.notes}, true
		}
	}
	return attachment.Image{}, false
}

func (p *Plugin) Attach(file agent.File, preview *ui.Bitmap) (uint64, error) {
	if !strings.HasPrefix(file.MIME, "image/") || len(file.Data) == 0 || preview == nil {
		return 0, errors.New("only an image with its preview can be attached")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(file.Data))
	switch {
	case err != nil:
		return 0, err
	case config.Width > attachment.MaxImageSide || config.Height > attachment.MaxImageSide || int64(config.Width)*int64(config.Height) > attachment.MaxImagePixels:
		return 0, errors.New("images must be at most 16 megapixels and 16,384 pixels per side")
	case len(file.Data) > maxFileBytes:
		return 0, fmt.Errorf("the image is larger than %d MB", maxFileBytes>>20)
	}
	if types, limited := kernel.Use(p.ctx, agent.FileTypesKey); limited && !types.Accepts(file.MIME) {
		return 0, errors.New("the agent takes only " + types.Summary())
	}
	total := len(file.Data)
	for _, f := range p.pending {
		total += f.bytes()
	}
	switch {
	case p.files() >= maxFiles:
		return 0, fmt.Errorf("a message takes at most %d files", maxFiles)
	case total > maxBytes:
		return 0, fmt.Errorf("a message takes at most %d MB of files", maxBytes>>20)
	}
	p.made++
	p.serial++
	file.Data = slices.Clone(file.Data)
	p.pending = append(p.pending, pending{id: p.serial, revision: 1, path: fmt.Sprintf("made:%d", p.made), file: file, thumb: preview})
	p.changed()
	return p.serial, nil
}

func (p *Plugin) Replace(source attachment.Image, file agent.File, preview *ui.Bitmap, notes string, details []attachment.Detail) error {
	i := slices.IndexFunc(p.pending, func(f pending) bool { return f.id == source.ID && f.revision == source.Revision })
	if i < 0 {
		return errors.New("the image was removed, sent or changed; open it again")
	}
	if !strings.HasPrefix(file.MIME, "image/") || len(file.Data) == 0 || preview == nil {
		return errors.New("the replacement is not an image")
	}
	types, limited := kernel.Use(p.ctx, agent.FileTypesKey)
	total := 0
	for _, f := range append([]agent.File{file}, detailFiles(details)...) {
		switch {
		case !strings.HasPrefix(f.MIME, "image/") || len(f.Data) == 0:
			return errors.New("the replacement and its details must be images")
		case len(f.Data) > maxFileBytes:
			return fmt.Errorf("the image is larger than %d MB", maxFileBytes>>20)
		case limited && !types.Accepts(f.MIME):
			return errors.New("the agent takes only " + types.Summary())
		}
		total += len(f.Data)
	}
	files := p.files() - len(p.pending[i].details) + len(details)
	for j, f := range p.pending {
		if j != i {
			total += f.bytes()
		}
	}
	switch {
	case total > maxBytes:
		return fmt.Errorf("the image would exceed the message's %d MB of files", maxBytes>>20)
	case files > maxFiles:
		return fmt.Errorf("a message takes at most %d files, details included", maxFiles)
	}
	f := &p.pending[i]
	file.Data = slices.Clone(file.Data)
	f.file, f.thumb, f.notes = file, preview, notes
	f.details = make([]attachment.Detail, len(details))
	for j, d := range details {
		d.File.Data = slices.Clone(d.File.Data)
		f.details[j] = d
	}
	f.revision++
	p.changed()
	return nil
}

func detailFiles(details []attachment.Detail) []agent.File {
	out := make([]agent.File, len(details))
	for i, d := range details {
		out[i] = d.File
	}
	return out
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
