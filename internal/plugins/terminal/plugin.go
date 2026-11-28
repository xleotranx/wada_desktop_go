package terminal

import (
	"cmp"
	"context"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/tabs"
	contractterm "wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/contract/workspace"
)

const (
	defaultCols = 80
	defaultRows = 24
	minCols     = 20
	minRows     = 4
	maxCols     = 400
	maxRows     = 200

	// outboxLen bounds the input and resizes waiting for the writer.
	outboxLen = 256
)

// termOp is one write to an attached terminal, applied in order.
type termOp struct {
	input      []byte
	cols, rows int // a resize when input is nil
}

// Kind is the terminal pane's kind in tabs.Panes; a tab's Ref is the
// terminal's ID.
const Kind = "terminal.shell"

// Plugin shows the daemon's detached PTY terminals as tabs: one tab per
// terminal, in the space of its directory. Only the terminal on show is
// attached; the others keep running.
type Plugin struct {
	ctx   *kernel.Context
	agent agent.Service
	host  shell.Host
	tabs  tabs.Service

	attempt uint64

	shown     bool // a terminal tab is on show
	terminals []contractterm.Info
	epoch     uint64
	activeID  string // the terminal on show

	cols int
	rows int

	screen     *screen
	listState  ui.ListState
	viewport   ui.Handle
	cellWidth  float32
	fitCols    int
	fitRows    int
	input      string
	focusInput bool
	errText    string

	loadTask   kernel.Task
	createTask kernel.Task
	killTask   kernel.Task

	attachScope *kernel.Scope
	streamID    string
	outbox      chan termOp
}

// New constructs a terminal plugin instance.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID:          "terminal",
		Title:       "Terminal",
		Description: "Interactive PTY terminals as tabs in each workspace.",
		Requires:    []kernel.Named{agent.ServiceKey, shell.HostKey, tabs.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.agent = kernel.Require(c, agent.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.tabs = kernel.Require(c, tabs.ServiceKey)
	p.cols = defaultCols
	p.rows = defaultRows
	p.screen = newScreen(defaultCols, defaultRows)
	p.listState.FollowEnd = true

	c.OnDispose(p.detach)
	kernel.On(c, agent.StatusChanged, p.onAgentStatusChanged)
	kernel.On(c, contractterm.Updated, p.onTerminalUpdated)
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if p.shown && p.agent.Status().State == agent.Ready {
			p.reload()
		}
	})

	kernel.Contribute(c, tabs.Panes, Kind, 0, tabs.PaneKind{
		Title:  p.title,
		Icon:   lucide.Terminal,
		Render: p.paneView,
		Show:   p.show,
		Hide:   p.hide,
	})
	kernel.Contribute(c, kernel.Commands, "terminal.show", 0, kernel.Command{
		Title: "Show Terminal",
		Menu:  "View",
		Keys:  []string{"cmd+j"},
		Run:   p.showTerminal,
	})
	kernel.Contribute(c, kernel.Commands, "terminal.new", 0, kernel.Command{
		Title: "New Terminal",
		Menu:  "View",
		Keys:  []string{"cmd+shift+j"},
		Run:   func() { p.createTerminal(p.tabs.Space()) },
	})
	kernel.Contribute(c, tabs.EndActions, "terminal.show", 5, tabs.EndAction{
		Label:   "Terminal",
		Tooltip: "Terminal  ⌘J",
		Icon:    lucide.Terminal,
		Run:     p.showTerminal,
	})

	if st := p.agent.Status(); st.State == agent.Ready {
		p.attempt = st.Attempt
	}
	return nil
}

func (p *Plugin) service() (contractterm.Service, bool) {
	return kernel.Use(p.ctx, contractterm.ServiceKey)
}

func (p *Plugin) onAgentStatusChanged(st agent.Status) {
	switch st.State {
	case agent.Down, agent.Stopped:
		p.detach()
	case agent.Ready:
		if st.Attempt != p.attempt {
			p.attempt = st.Attempt
			if p.shown {
				p.reload()
			}
		}
	case agent.Connecting:
	}
}

func (p *Plugin) onTerminalUpdated(info contractterm.Info) {
	p.epoch++
	p.upsertTerminal(info)
	if p.activeID == info.ID && !info.Running {
		p.errText = ""
	}
	p.ctx.Invalidate()
}

func (p *Plugin) upsertTerminal(info contractterm.Info) {
	if info.ID == "" {
		return
	}
	idx := slices.IndexFunc(p.terminals, func(t contractterm.Info) bool { return t.ID == info.ID })
	if idx >= 0 {
		p.terminals[idx] = info
		return
	}
	p.terminals = append(p.terminals, info)
}

// ---- The pane

// title is a terminal's name; "" until it is listed keeps the tab's own.
func (p *Plugin) title(ref string) string {
	for _, t := range p.terminals {
		if t.ID == ref {
			return cmp.Or(t.Name, "Terminal")
		}
	}
	return ""
}

// show attaches the terminal of the tab coming on show.
func (p *Plugin) show(t tabs.Tab) {
	p.shown = true
	p.activeID = t.Ref
	p.errText = ""
	p.focusInput = true
	if p.streamID != t.Ref {
		p.screen.Reset()
		p.attach(t.Ref)
	}
	p.reload()
	p.ctx.Invalidate()
}

// hide detaches; the terminal keeps running.
func (p *Plugin) hide() {
	p.shown = false
	p.detach()
}

// showTerminal shows a terminal of the space on show: the tab of one, else
// a running terminal in its directory without a tab, else a new one.
func (p *Plugin) showTerminal() {
	folder := p.tabs.Space()
	if active, ok := p.tabs.Active(); ok && active.Kind == tabs.Pane && active.Pane == Kind {
		return
	}
	var open []tabs.Tab
	for _, t := range p.tabs.Tabs() {
		if t.Kind == tabs.Pane && t.Pane == Kind && t.Folder == folder {
			open = append(open, t)
		}
	}
	if len(open) > 0 {
		p.tabs.Select(open[len(open)-1].ID)
		return
	}
	svc, ok := p.service()
	if !ok {
		p.unavailable()
		return
	}
	dir := p.directoryOf(folder)
	kernel.Go(p.ctx, &p.loadTask, func(ctx context.Context) ([]contractterm.Info, error) {
		return svc.List(ctx)
	}, func(list []contractterm.Info, err error) {
		if err == nil {
			p.terminals = list
			for _, t := range list {
				if t.Running && t.Directory == dir && dir != "" {
					p.tabs.Open(Kind, t.ID, folder)
					return
				}
			}
		}
		p.createTerminal(folder)
	})
}

// directoryOf is where a space's terminals start: its folder, or the
// inbox's for the agent's default space.
func (p *Plugin) directoryOf(folder string) string {
	if folder != "" {
		return folder
	}
	if ws, ok := kernel.Use(p.ctx, workspace.ServiceKey); ok {
		if box, ok := ws.At(""); ok {
			return box.Directory
		}
	}
	return ""
}

func (p *Plugin) unavailable() {
	p.host.Toast(ui.Toast{Title: "Terminals are unavailable", Description: "The agent's backend has no terminals.", Type: components.ToastError})
}

// reload reads the terminals again and closes the tabs of those gone.
func (p *Plugin) reload() {
	svc, ok := p.service()
	if !ok {
		p.errText = "Terminal service unavailable"
		p.ctx.Invalidate()
		return
	}
	epoch := p.epoch
	kernel.Go(p.ctx, &p.loadTask, func(ctx context.Context) ([]contractterm.Info, error) {
		return svc.List(ctx)
	}, func(list []contractterm.Info, err error) {
		if err != nil {
			p.errText = err.Error()
			p.ctx.Invalidate()
			return
		}
		if p.epoch == epoch {
			p.terminals = list
			p.closeGone()
		} else {
			for _, t := range list {
				if !slices.ContainsFunc(p.terminals, func(cur contractterm.Info) bool { return cur.ID == t.ID }) {
					p.terminals = append(p.terminals, t)
				}
			}
		}
		// After a reconnect nothing is attached; an attachment under way
		// is left to finish.
		if p.shown && p.activeID != "" && p.attachScope == nil && p.has(p.activeID) {
			p.attach(p.activeID)
		}
		p.ctx.Invalidate()
	})
}

// closeGone closes the tabs of terminals the backend no longer lists.
func (p *Plugin) closeGone() {
	for _, t := range p.tabs.Tabs() {
		if t.Kind == tabs.Pane && t.Pane == Kind && !p.has(t.Ref) {
			p.tabs.Close(t.ID)
		}
	}
}

func (p *Plugin) has(id string) bool {
	return slices.ContainsFunc(p.terminals, func(t contractterm.Info) bool { return t.ID == id })
}

// createTerminal starts a terminal in a space and opens its tab.
func (p *Plugin) createTerminal(folder string) {
	svc, ok := p.service()
	if !ok {
		p.unavailable()
		return
	}
	req := contractterm.CreateRequest{
		Directory: p.directoryOf(folder),
		Cols:      p.cols,
		Rows:      p.rows,
	}
	kernel.Go(p.ctx, &p.createTask, func(ctx context.Context) (contractterm.Info, error) {
		return svc.Create(ctx, req)
	}, func(info contractterm.Info, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The terminal didn’t start", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.epoch++
		p.upsertTerminal(info)
		p.tabs.Open(Kind, info.ID, folder)
	})
}

// killTerminal ends the terminal and closes its tab.
func (p *Plugin) killTerminal(id string) {
	if id == "" {
		return
	}
	svc, ok := p.service()
	if !ok {
		return
	}
	if p.activeID == id {
		p.detach()
	}
	kernel.Go(p.ctx, &p.killTask, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, svc.Kill(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.errText = err.Error()
			if p.shown && p.activeID == id {
				p.attach(id)
			}
			p.ctx.Invalidate()
			return
		}
		p.epoch++
		p.terminals = slices.DeleteFunc(p.terminals, func(t contractterm.Info) bool { return t.ID == id })
		p.closeGone()
		p.ctx.Invalidate()
	})
}

func (p *Plugin) attach(id string) {
	p.detach()
	svc, ok := p.service()
	if !ok || id == "" {
		return
	}
	scope := p.ctx.NewScope()
	p.attachScope = scope
	cols, rows := p.cols, p.rows
	outbox := make(chan termOp, outboxLen)
	p.outbox = outbox

	scope.Spawn(func(ctx context.Context) {
		st, err := svc.Attach(ctx, contractterm.AttachRequest{
			ID:   id,
			Cols: cols,
			Rows: rows,
		})
		if err != nil {
			if ctx.Err() == nil {
				scope.Post(func() {
					if p.attachScope == scope {
						p.errText = err.Error()
						p.ctx.Invalidate()
					}
				})
			}
			return
		}
		defer func() { _ = st.Close() }()
		scope.Spawn(func(ctx context.Context) { p.writeLoop(ctx, scope, st, outbox) })

		info := st.Info()
		snap := st.Snapshot()
		ch := st.Output()
		scope.Post(func() {
			if p.attachScope != scope {
				return
			}
			p.streamID = id
			p.errText = ""
			p.upsertTerminal(info)
			p.screen.Resize(cmp.Or(info.Cols, cols), cmp.Or(info.Rows, rows))
			p.screen.Reset()
			p.screen.Write(snap)
			if p.cols != cols || p.rows != rows {
				// The drawer was fitted while this attachment was starting.
				p.screen.Resize(p.cols, p.rows)
				p.enqueue(termOp{cols: p.cols, rows: p.rows})
			}
			p.listState.ScrollToEnd()
			p.ctx.Invalidate()
		})

		for {
			var chunk []byte
			select {
			case <-ctx.Done():
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				chunk = append(chunk, data...)
			}
		drain:
			for len(chunk) < 64<<10 {
				select {
				case more, ok := <-ch:
					if !ok {
						break drain
					}
					chunk = append(chunk, more...)
				default:
					break drain
				}
			}
			out := chunk
			scope.Post(func() {
				if p.attachScope != scope {
					return
				}
				p.screen.Write(out)
				p.listState.ScrollToEnd()
				p.ctx.Invalidate()
			})
		}
	})
}

// writeLoop applies one attachment's input and resizes in the order the user
// made them; it is the attachment's only writer.
func (p *Plugin) writeLoop(ctx context.Context, scope *kernel.Scope, st contractterm.Stream, outbox <-chan termOp) {
	for {
		var op termOp
		select {
		case <-ctx.Done():
			return
		case op = <-outbox:
		}
		var err error
		if op.input != nil {
			err = st.WriteInput(op.input)
		} else {
			err = st.Resize(op.cols, op.rows)
		}
		if err != nil && ctx.Err() == nil {
			scope.Post(func() {
				if p.attachScope == scope {
					p.errText = err.Error()
					p.ctx.Invalidate()
				}
			})
		}
	}
}

func (p *Plugin) detach() {
	if p.attachScope != nil {
		p.attachScope.Dispose()
		p.attachScope = nil
	}
	p.streamID = ""
	p.outbox = nil
}

// enqueue hands op to the attachment's writer, or reports that it is
// backed up rather than blocking the UI thread. Input made while the
// attachment is still starting waits for its writer.
func (p *Plugin) enqueue(op termOp) {
	if p.outbox == nil {
		if op.input != nil {
			p.errText = "No terminal is attached; input was not sent"
			p.ctx.Invalidate()
		}
		return
	}
	select {
	case p.outbox <- op:
	default:
		p.errText = "Terminal is not keeping up; input was not sent"
		p.ctx.Invalidate()
	}
}

func (p *Plugin) sendBytes(data []byte) {
	if len(data) == 0 {
		return
	}
	p.enqueue(termOp{input: append([]byte(nil), data...)})
}

// fitTo sizes the PTY to the pane's viewport, as it was laid out last frame.
func (p *Plugin) fitTo(c *ui.Context) {
	b := p.viewport.Bounds(c)
	if b.W <= 0 || b.H <= 0 {
		return
	}
	if p.cellWidth <= 0 {
		const sample = "MMMMMMMMMM"
		w, _ := c.MeasureText(0, ui.Span{Text: sample, Font: "monospace", Size: lineFontSize})
		p.cellWidth = w / float32(len(sample))
	}
	if p.cellWidth <= 0 {
		return
	}
	cols, rows := fitSize(b.W-2*viewportPadX, b.H-2*viewportPadY, p.cellWidth)
	if (cols == p.cols && rows == p.rows) || (cols == p.fitCols && rows == p.fitRows) {
		return
	}
	p.fitCols, p.fitRows = cols, rows
	p.ctx.Post(func() { p.resize(cols, rows) })
}

func fitSize(width, height, cellWidth float32) (int, int) {
	cols := clampInt(int(width/cellWidth), minCols, maxCols)
	rows := clampInt(int(height/lineHeight), minRows, maxRows)
	return cols, rows
}

func (p *Plugin) resize(cols, rows int) {
	if cols == p.cols && rows == p.rows {
		return
	}
	p.cols, p.rows = cols, rows
	p.screen.Resize(cols, rows)
	p.enqueue(termOp{cols: cols, rows: rows})
	p.ctx.Invalidate()
}

func (p *Plugin) submitInput() {
	cmd := p.input
	p.input = ""
	p.focusInput = true
	p.sendBytes([]byte(cmd + "\r"))
	p.ctx.Invalidate()
}

func (p *Plugin) sendControl(b byte) {
	p.focusInput = true
	p.sendBytes([]byte{b})
}

func (p *Plugin) clearScreen() {
	p.screen.Reset()
	p.focusInput = true
	if info, ok := p.activeTerminal(); ok && info.Running {
		p.sendBytes([]byte{0x0c})
	}
	p.ctx.Invalidate()
}

func (p *Plugin) copyOutput() {
	text := p.screen.PlainText()
	if err := p.host.Copy(text); err != nil {
		p.host.Toast(ui.Toast{Title: "Copy failed", Description: err.Error(), Type: "error"})
		return
	}
	p.host.Toast(ui.Toast{Title: "Copied terminal output", Type: "success"})
}

func (p *Plugin) activeTerminal() (contractterm.Info, bool) {
	for _, t := range p.terminals {
		if t.ID == p.activeID {
			return t, true
		}
	}
	return contractterm.Info{}, false
}
