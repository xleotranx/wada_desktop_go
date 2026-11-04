package video

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// editKind is what a remembered edit changed.
type editKind uint8

const (
	timingEditKind editKind = iota
	textEditKind
)

// clipEdit is one clip as it was, or as it is to be, to put back.
type clipEdit struct {
	Kind         editKind
	ID           string
	File         string
	Start        float64
	Duration     float64
	Track        int
	Text         string
	RootDuration float64
}

// editOf is a clip's timing now.
func editOf(c *Clip) clipEdit {
	return clipEdit{Kind: timingEditKind, ID: c.ID, File: c.File, Start: c.Start, Duration: c.Duration, Track: c.Track}
}

// timingEditOf also remembers the film length a root edit can extend.
func (p *Plugin) timingEditOf(c *Clip) clipEdit {
	e := editOf(c)
	if p.project != nil && p.project.Root.HasDuration && c.File == p.project.File {
		e.RootDuration = p.project.Root.DeclaredSeconds
	}
	return e
}

// textEditOf is a clip's words now.
func textEditOf(c *Clip) clipEdit {
	return clipEdit{Kind: textEditKind, ID: c.ID, File: c.File, Text: c.Text}
}

// pushEdit adds a step to the undo stack.
func (p *Plugin) pushEdit(e clipEdit) {
	p.undo = append(p.undo, e)
	if len(p.undo) > maxUndo {
		p.undo = p.undo[len(p.undo)-maxUndo:]
	}
	p.redo = nil
}

// canUndo and canRedo say what the header's buttons do.
func (p *Plugin) canUndo() bool {
	return len(p.undo) > 0 && !p.editing.Running() && !p.adding.Running() && !p.loading.Running()
}
func (p *Plugin) canRedo() bool {
	return len(p.redo) > 0 && !p.editing.Running() && !p.adding.Running() && !p.loading.Running()
}

// undoEdit puts the last timing edit back.
func (p *Plugin) undoEdit() { p.stepEdit(&p.undo, &p.redo) }

// redoEdit applies the edit undone last.
func (p *Plugin) redoEdit() { p.stepEdit(&p.redo, &p.undo) }

// stepEdit moves one edit from a stack to the other, writing the file.
func (p *Plugin) stepEdit(from, to *[]clipEdit) {
	if len(*from) == 0 || p.project == nil || p.dir == "" || p.editing.Running() || p.adding.Running() || p.loading.Running() {
		return
	}
	e := (*from)[len(*from)-1]
	clip, ok := p.clip(e.ID)
	if !ok {
		*from = (*from)[:len(*from)-1]
		return
	}
	inverse := p.timingEditOf(clip)
	if e.Kind == textEditKind {
		inverse = textEditOf(clip)
	}
	dir, file, project := p.dir, e.File, p.project
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		if e.Kind == textEditKind {
			return struct{}{}, writeClipText(dir, file, e.ID, e.Text)
		}
		return struct{}{}, writeClipTiming(dir, file, project, e.ID, e.Start, e.Duration, e.Track, e.RootDuration)
	}, func(_ struct{}, err error) {
		defer p.flushReload()
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The clip did not change back", Description: err.Error(), Type: components.ToastError})
			return
		}
		*from = (*from)[:len(*from)-1]
		*to = append(*to, inverse)
		if e.Kind == textEditKind {
			p.text, p.textBase, p.textFor = e.Text, e.Text, e.ID
		}
		p.reload()
	})
}
