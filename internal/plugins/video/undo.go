package video

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// timingEdit is one clip's timing as it was, to put back.
type timingEdit struct {
	ID       string
	File     string
	Start    float64
	Duration float64
	Track    int
}

// editOf is a clip's timing now.
func editOf(c *Clip) timingEdit {
	return timingEdit{ID: c.ID, File: c.File, Start: c.Start, Duration: c.Duration, Track: c.Track}
}

// rememberTiming keeps the timing a drag is about to change, and drops the
// redo steps a new edit makes dead.
func (p *Plugin) rememberTiming(e timingEdit) {
	p.undo = append(p.undo, e)
	if len(p.undo) > maxUndo {
		p.undo = p.undo[len(p.undo)-maxUndo:]
	}
	p.redo = nil
}

// canUndo and canRedo say what the header's buttons do.
func (p *Plugin) canUndo() bool { return len(p.undo) > 0 && !p.editing.Running() }
func (p *Plugin) canRedo() bool { return len(p.redo) > 0 && !p.editing.Running() }

// undoEdit puts the last timing edit back.
func (p *Plugin) undoEdit() { p.stepEdit(&p.undo, &p.redo) }

// redoEdit applies the edit undone last.
func (p *Plugin) redoEdit() { p.stepEdit(&p.redo, &p.undo) }

// stepEdit moves one timing from a stack to the other, writing the file.
func (p *Plugin) stepEdit(from, to *[]timingEdit) {
	if len(*from) == 0 || p.project == nil || p.dir == "" || p.editing.Running() {
		return
	}
	e := (*from)[len(*from)-1]
	clip, ok := p.clip(e.ID)
	if !ok {
		*from = (*from)[:len(*from)-1]
		return
	}
	*from = (*from)[:len(*from)-1]
	*to = append(*to, editOf(clip))
	dir, file, project := p.dir, e.File, p.project
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writeClipTiming(dir, file, project, e.ID, e.Start, e.Duration, e.Track)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The clip did not change back", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.reload()
	})
}
