package design

import (
	"path/filepath"
	"strconv"

	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

const (
	canvasGrow     = 2.4 // the canvas's share of the page beside the conversation's 1
	canvasMinWidth = 560
	cardWidth      = 280
	dotStep        = 24 // page pixels between the canvas's dots
)

// suggestions start a design from the empty canvas.
var suggestions = []struct{ title, prompt string }{
	{"Landing page", "Design a landing page for "},
	{"Mobile onboarding", "Design a three-screen mobile onboarding (390x844 artboards) for "},
	{"Three variants", "Design three different directions for the hero section of "},
	{"Slide deck", "Design a five-slide deck (1920x1080 artboards) about "},
}

// drawer is the canvas beside the conversation: a header, and the canvas
// with its artboards, marks and tools.
func (p *Plugin) drawer(c *ui.Context) {
	if !p.open {
		return
	}
	pal := native.PaletteFor(c)
	ui.Row(c.Key("design")).Grow(canvasGrow).MinWidth(canvasMinWidth).FillHeight().Label("Design canvas").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(pal.DrawerBg).Children(func() {
			p.header(c, pal)
			p.canvasView(c, pal)
		})
	})
}

func (p *Plugin) header(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Height(48).Shrink(0).Padding(0, 8, 0, 16).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Icon(c, lucide.Palette()).FontSize(14).TextColor(pal.Muted).Shrink(0)
		ui.Text(c, "Design").FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Shrink(0)
		ui.Text(c, rel(p.project, p.folder)).FontSize(12.5).TextColor(pal.Muted).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Box(c).Grow(1)
		p.systemMenu(c, pal, false)
		if a, ok := p.target(); ok {
			p.previewMenu(c, pal, a)
			b := ui.ButtonBase(c.Key("export")).Label("Export").Height(28).Padding(0, 8).Radius(7).Gap(5).AlignItems(ui.Center).
				Shrink(0).Cursor(ui.CursorPointer)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.Menu(func(m *ui.Menu) {
				if m.Item("Attach Snapshot to Message").Chosen() {
					p.ctx.Post(func() { p.snapshot(a.Path) })
				}
				if m.Item("Open in Browser").Chosen() {
					p.ctx.Post(func() { p.host.OpenURL("file://" + a.Path) })
				}
				if m.Item("Copy Path").Chosen() {
					p.ctx.Post(func() { p.host.Copy(a.Path) })
				}
			})
			b.Children(func() {
				ui.Icon(c, lucide.Share()).FontSize(13).TextColor(pal.Muted)
				ui.Text(c, "Export").FontSize(12.5).TextColor(pal.Text).SingleLine()
			})
		}
		p.iconButton(c, pal, "reload", "Reload the artboards", lucide.RotateCw, false, false, p.reload)
	})
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
}

// systemMenu chooses the design system the agent follows. The empty
// canvas shows it as a larger button.
func (p *Plugin) systemMenu(c *ui.Context, pal native.Palette, large bool) {
	if len(p.found.Systems) == 0 && !large {
		return
	}
	name := "Choose design system…"
	if s := p.system(); s != "" {
		name = s
	} else if len(p.found.Systems) == 0 {
		name = "No design system in the folder"
	}
	b := ui.ButtonBase(c.Key("system")).Label("Design system").Tooltip("The design system the agent follows").
		Height(28).Padding(0, 9).Radius(7).Gap(6).AlignItems(ui.Center).MinWidth(0).Shrink(1).Cursor(ui.CursorPointer)
	if large {
		b.Background(pal.Hover).Height(32).Padding(0, 12)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Menu(func(m *ui.Menu) {
		if m.Item("None").Checked(p.system() == "").Chosen() {
			p.ctx.Post(func() { p.chooseSystem("") })
		}
		if len(p.found.Systems) > 0 {
			m.Separator()
		}
		for _, s := range p.found.Systems {
			if m.Item(s).Checked(p.system() == s).Chosen() {
				p.ctx.Post(func() { p.chooseSystem(s) })
			}
		}
	})
	b.Children(func() {
		if !large {
			ui.Icon(c, lucide.Layers()).FontSize(13).TextColor(pal.Muted).Shrink(0)
		}
		ui.Text(c, name).FontSize(12.5).TextColor(pal.Text).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Icon(c, lucide.ChevronDown()).FontSize(12).TextColor(pal.Muted).Shrink(0)
	})
}

// canvasView is the canvas: dots, artboards, marks, the tool layer, the
// floating tools, the open card and the tweaks.
func (p *Plugin) canvasView(c *ui.Context, pal native.Palette) {
	cursor := ui.CursorDefault
	switch {
	case p.drag.on:
		cursor = ui.CursorGrabbing
	case p.tool == handTool:
		cursor = ui.CursorGrab
	}
	k := p.vp.zoom()
	vp := p.vp
	ui.Box(c.Key("canvas")).FillWidth().Grow(1).MinHeight(0).Clip().Background(pal.Bg).Bind(&p.canvas).Label("Canvas").Children(func() {
		p.measure(c)
		// The background takes the pointer, scrolling and keys where nothing
		// on the canvas does.
		ui.Box(c.Key("background")).Absolute().Top(0).Left(0).Right(0).Bottom(0).FocusBind(&p.focus, true).Cursor(cursor).
			Label("Canvas background").HandleInput(p.input).Draw(func(painter *ui.Painter, r ui.Rect) {
			step := dotStep * k
			for step < 12 {
				step *= 2
			}
			x0 := r.X + mod(vp.X, step)
			for y := r.Y + mod(vp.Y, step); y < r.Y+r.H; y += step {
				for x := x0; x < r.X+r.W; x += step {
					painter.Fill(ui.Rect{X: x - .75, Y: y - .75, W: 1.5, H: 1.5}, pal.Hairline, .75)
				}
			}
		})
		boards := p.shown()
		if len(boards) == 0 {
			p.empty(c, pal)
			return
		}
		// A layer over the canvas per artboard keeps its parts' keys apart.
		for _, a := range boards {
			ui.Box(c.Key(a.Path)).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Children(func() {
				p.artboardView(c, pal, a)
			})
		}
		showHover := false
		if p.tool != selectTool {
			// The tool takes the pointer over the pages too.
			cur := ui.CursorCrosshair
			if p.tool == handTool {
				cur = cursor
			}
			layer := ui.Box(c.Key("tool-layer")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Cursor(cur).HandleInput(p.input)
			showHover = p.tool == inspectTool && layer.Hovered()
		}
		// The names stay clickable whatever the tool.
		for i, a := range boards {
			ui.Box(c.Key("label " + a.Path)).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Children(func() {
				p.labelView(c, pal, a, i, len(p.current().Boards))
			})
		}
		p.inspectionView(c, pal, showHover)
		p.marksView(c, pal)
		p.toolbar(c, pal)
		p.controls(c, pal)
		p.tweaksPanel(c, pal)
		p.card(c, pal)
		p.sendBar(c, pal)
		if more := len(p.current().Boards) - len(boards); more > 0 {
			ui.Text(c, strconv.Itoa(more)+" more artboards on this page aren't shown").FontSize(11.5).TextColor(pal.Muted).
				Absolute().Left(16).Bottom(14)
		}
	})
}

// measure keeps the canvas's size for fitting, and fits once it is known
// or changes while the canvas fits the page.
func (p *Plugin) measure(c *ui.Context) {
	r := p.canvas.Bounds(c)
	if r.W <= 0 || r.H <= 0 || r.W == p.size[0] && r.H == p.size[1] {
		return
	}
	p.size = [2]float32{r.W, r.H}
	if p.autoFit && !p.fitting {
		p.fitting = true
		p.ctx.Post(func() {
			p.fitting = false
			if p.autoFit {
				p.fit()
			}
		})
	}
}

func mod(a, b float32) float32 {
	m := a - b*float32(int(a/b))
	if m < 0 {
		m += b
	}
	return m
}

func (p *Plugin) labelView(c *ui.Context, pal native.Palette, a artboard, i, n int) {
	if p.boards[a.Path] == nil {
		return
	}
	x, y, w, _ := p.vp.screen(a)
	selected := a.Path == p.selected
	ui.Row(c.Key("label")).Absolute().Left(x).Top(y - labelHeight).Width(max(w, 160)).Height(labelHeight).
		AlignItems(ui.Center).Gap(2).Children(func() {
		name := ui.ButtonBase(c.Key("name")).Label("Select "+a.name()).Tooltip(strconv.Itoa(i+1)+" of "+strconv.Itoa(n)).
			Height(22).Padding(0, 4).Radius(5).AlignItems(ui.Center).MinWidth(0).Shrink(1).Cursor(ui.CursorPointer)
		name.OnClick(func() { p.clearInspection(); p.selectBoard(a.Path) })
		name.Children(func() {
			color := pal.Muted
			if selected {
				color = pal.Link
			}
			ui.Text(c, a.name()).FontSize(12).FontWeight(500).TextColor(color).SingleLine().Ellipsis("…").MinWidth(0)
		})
		ui.Box(c).Grow(1)
		focused := p.focusView != nil && p.focusView.path == a.Path
		if w >= 160 || focused {
			label, icon := "Fit screen "+a.name(), lucide.Maximize2
			if focused {
				label, icon = "Restore view for "+a.name(), lucide.Minimize2
			}
			p.smallButton(c, pal, "zoom", label, icon, func() { p.focusBoard(a.Path) })
		}
		if w >= 160 {
			p.smallButton(c, pal, "snapshot", "Attach a snapshot of "+a.name(), lucide.Camera, func() { p.snapshot(a.Path) })
			p.smallButton(c, pal, "present", "Present "+a.name(), lucide.Play, func() { p.present(a.Path) })
		}
	})
}

// artboardView is the artboard's page, with its outline while selected.
func (p *Plugin) artboardView(c *ui.Context, pal native.Palette, a artboard) {
	b := p.boards[a.Path]
	if b == nil {
		return
	}
	x, y, w, h := p.vp.screen(a)
	selected := a.Path == p.selected
	accent := pal.Link
	ui.Box(c.Key("shadow")).Absolute().Left(x).Top(y).Width(w).Height(h).Background(pal.Raised).PassThrough().
		Shadow(0, 1, 6, 0, pal.Hairline)
	if a.Path != p.presenting {
		if p.vp.zoom() >= 1 {
			b.web.Show(c.Key("page")).Absolute().Left(x).Top(y).Width(w).Height(h)
		} else {
			ui.Box(c.Key("preview")).Absolute().Left(x).Top(y).Width(w).Height(h).Clip().Children(func() {
				// Keep the source page laid out at its declared size behind the
				// bitmap, including while only part of the artboard is visible.
				b.web.Show(c.Key("page")).Absolute().Left(0).Top(0).Width(a.W).Height(a.H)
				ui.ButtonBase(c.Key("open")).Label("Open " + a.name() + " at 100%").Tooltip("Open at 100% to interact").
					Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(pal.Raised).Cursor(ui.CursorPointer).
					OnClick(func() { p.actualBoard(a) }).Children(func() {
					if b.raster != nil {
						ui.Image(c, b.raster).Fill()
					} else {
						ui.Text(c, "Open at 100%").FontSize(12).TextColor(pal.Muted).Margin(8)
					}
				})
			})
		}
	}
	if selected {
		ui.Box(c.Key("outline")).Absolute().Left(x-2).Top(y-2).Width(w+4).Height(h+4).Border(2, accent).PassThrough()
		for _, at := range [][2]float32{{x, y}, {x + w, y}, {x, y + h}, {x + w, y + h}} {
			ui.Box(c).Absolute().Left(at[0]-4).Top(at[1]-4).Width(8).Height(8).Background(pal.Bg).Border(1.5, accent).PassThrough()
		}
	}
}

// marksView draws the comments' pins on their artboards and the notes.
func (p *Plugin) marksView(c *ui.Context, pal native.Palette) {
	k := p.vp.zoom()
	for _, m := range p.marks {
		n := strconv.Itoa(p.number(m.ID))
		pinColor := pal.Link
		if m.Resolved {
			pinColor = pal.Muted
		}
		switch m.Kind {
		case commentMark:
			if m.Unlocated {
				continue
			}
			a, ok := p.current().board(m.Path)
			if !ok {
				continue
			}
			x, y, _, _ := p.vp.screen(a)
			pin := ui.ButtonBase(c.Key(m.ID)).Label("Comment "+n).Tooltip(m.status()+": "+m.Words).Absolute().Left(x+m.X*k-11).Top(y+m.Y*k-11).
				Size(22, 22).Radius(11).Center().Background(pinColor).Border(2, pal.Bg).Cursor(ui.CursorPointer)
			pin.OnClick(func() { p.edit(m.ID) })
			pin.Children(func() { ui.Text(c, n).FontSize(11).FontWeight(700).TextColor(pal.Bg) })
		case noteMark:
			note := ui.ButtonBase(c.Key(m.ID)).Label("Note "+n).Absolute().Left(p.vp.X+m.X*k).Top(p.vp.Y+m.Y*k).
				Width(180).Padding(8, 10).Radius(8).Background(pal.Raised).Border(1, pal.Orange).Gap(4).Cursor(ui.CursorPointer)
			note.OnClick(func() { p.edit(m.ID) })
			note.Children(func() {
				ui.Text(c, "Note "+n+" · "+m.status()).FontSize(11).FontWeight(600).TextColor(pal.Orange)
				words := m.Words
				if words == "" {
					words = "New note"
				}
				ui.Text(c, words).FontSize(12).LineHeight(1.35).TextColor(pal.Text).MaxLines(4)
			})
		}
	}
}

// floating is the look of the tools that float over the canvas.
func floating(e ui.Element, pal native.Palette) ui.Element {
	return e.Background(pal.Raised).Border(1, pal.Hairline).Radius(10).Padding(4).Gap(2).AlignItems(ui.Center).
		Shadow(0, 2, 8, 0, pal.Hairline)
}

func (p *Plugin) toolbar(c *ui.Context, pal native.Palette) {
	floating(ui.Row(c.Key("toolbar")).Absolute().Left(12).Top(12), pal).Children(func() {
		p.pageMenu(c, pal)
		divider(c, pal)
		p.toolButton(c, pal, selectTool, "Select  V", lucide.MousePointer2)
		p.toolButton(c, pal, inspectTool, "Inspect  I", lucide.Scan)
		p.toolButton(c, pal, handTool, "Hand  H", lucide.Hand)
		p.toolButton(c, pal, commentTool, "Comment  C", lucide.MessageCircle)
		p.toolButton(c, pal, noteTool, "Note  N", lucide.StickyNote)
		divider(c, pal)
		p.iconButton(c, pal, "new-artboard", "New artboard", lucide.SquarePlus, false, p.creating.Running(), p.newArtboard)
	})
}

func divider(c *ui.Context, pal native.Palette) {
	ui.Box(c).Width(1).Height(18).Margin(0, 4).Background(pal.Hairline)
}

func pageName(dir string) string {
	if dir == "." || dir == "" {
		return "Page 1"
	}
	return filepath.Base(dir)
}

// pageMenu names the page on show; its menu shows another.
func (p *Plugin) pageMenu(c *ui.Context, pal native.Palette) {
	b := ui.ButtonBase(c.Key("pages")).Label("Choose the page").Tooltip(p.page).Height(28).Padding(0, 8).Radius(7).Gap(5).
		AlignItems(ui.Center).MaxWidth(180).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Menu(func(m *ui.Menu) {
		for _, pg := range p.found.Pages {
			label := pageName(pg.Dir) + " — " + strconv.Itoa(len(pg.Boards))
			if m.Item(label).Checked(pg.Dir == p.page).Chosen() {
				p.ctx.Post(func() { p.choosePage(pg.Dir) })
			}
		}
	})
	b.Children(func() {
		ui.Text(c, pageName(p.page)).FontSize(12.5).FontWeight(500).TextColor(pal.Text).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(pal.Muted).Shrink(0)
	})
}

func (p *Plugin) toolButton(c *ui.Context, pal native.Palette, t tool, label string, icon func() *ui.SVG) {
	p.iconButton(c, pal, "tool-"+strconv.Itoa(int(t)), label, icon, p.tool == t, false, func() { p.setTool(t) })
}

// controls put presentation and tweaks at the top right, and zoom below.
func (p *Plugin) controls(c *ui.Context, pal native.Palette) {
	a, ok := p.target()
	floating(ui.Row(c.Key("controls")).Absolute().Right(12).Top(12), pal).Children(func() {
		p.iconButton(c, pal, "present", "Present", lucide.Play, false, !ok, func() { p.present(a.Path) })
		label := "Tweaks"
		if !ok || len(p.boards[a.Path].tweaks) == 0 {
			label = "Tweaks: this artboard declares none"
		}
		p.iconButton(c, pal, "tweaks", label, lucide.SlidersHorizontal, p.tweaksOn, label != "Tweaks",
			func() { p.tweaksOn = !p.tweaksOn })
	})
	floating(ui.Row(c.Key("zoom-controls")).Absolute().Right(12).Bottom(12), pal).Children(func() {
		if p.focusView != nil {
			p.iconButton(c, pal, "fit", "Restore previous view", lucide.Undo2, true, false, p.restoreView)
		} else {
			p.iconButton(c, pal, "fit", "Zoom to fit  ⇧1", lucide.Scan, false, false, p.fit)
		}
		p.iconButton(c, pal, "zoom-out", "Zoom out", lucide.Minus, false, p.vp.zoom() <= minZoom, func() { p.zoomStep(false) })
		z := ui.ButtonBase(c.Key("zoom")).Label("Zoom").Height(28).Padding(0, 6, 0, 8).Radius(7).Gap(3).AlignItems(ui.Center).
			Cursor(ui.CursorPointer)
		if z.Hovered() {
			z.Background(pal.Hover)
		}
		z.Menu(func(m *ui.Menu) {
			if m.Item("Zoom In").Chosen() {
				p.ctx.Post(func() { p.zoomStep(true) })
			}
			if m.Item("Zoom Out").Chosen() {
				p.ctx.Post(func() { p.zoomStep(false) })
			}
			if m.Item("Zoom to Fit").Chosen() {
				p.ctx.Post(p.fit)
			}
			m.Separator()
			for _, k := range []float32{.5, 1, 2} {
				if m.Item(strconv.Itoa(int(k*100)) + "%").Chosen() {
					p.ctx.Post(func() { p.zoomTo(k) })
				}
			}
		})
		z.Children(func() {
			ui.Text(c, strconv.Itoa(int(p.vp.zoom()*100+.5))+"%").FontSize(12.5).TextColor(pal.Text).FontFeatures("tnum")
			ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(pal.Muted)
		})
		p.iconButton(c, pal, "zoom-in", "Zoom in", lucide.Plus, false, p.vp.zoom() >= maxZoom, func() { p.zoomStep(true) })
	})
}

// target is the artboard the canvas's actions apply to: the selected one,
// or else the first.
func (p *Plugin) target() (artboard, bool) {
	if a, ok := p.current().board(p.selected); ok && p.boards[a.Path] != nil {
		return a, true
	}
	if boards := p.shown(); len(boards) > 0 && p.boards[boards[0].Path] != nil {
		return boards[0], true
	}
	return artboard{}, false
}

func (p *Plugin) tweaksPanel(c *ui.Context, pal native.Palette) {
	a, ok := p.target()
	if !p.tweaksOn || !ok || len(p.boards[a.Path].tweaks) == 0 {
		return
	}
	b := p.boards[a.Path]
	ui.Column(c.Key("tweaks")).Absolute().Right(12).Top(56).Width(260).Background(pal.Raised).Border(1, pal.Hairline).
		Radius(10).Padding(12).Gap(10).Shadow(0, 2, 8, 0, pal.Hairline).Label("Tweaks").Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Tweaks").FontSize(13).FontWeight(600).TextColor(pal.Text)
			ui.Text(c, " · "+a.name()).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…").MinWidth(0)
		})
		for i := range b.tweaks {
			t := &b.tweaks[i]
			c.Key(t.Var)
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Children(func() {
					ui.Text(c, t.Label).FontSize(12).TextColor(pal.Text).Grow(1).SingleLine().Ellipsis("…")
					if t.Type != "color" {
						ui.Text(c, t.Value).FontSize(11.5).TextColor(pal.Muted).FontFeatures("tnum").SingleLine()
					}
				})
				apply := func() { t.changed(); p.applyTweaks(a.Path) }
				switch t.Type {
				case "range":
					hi := t.Max
					if hi <= t.Min {
						hi = t.Min + 100
					}
					ui.Slider(c.Key("control"), &t.num, t.Min, hi).FillWidth().Label(t.Label).OnChange(func() {
						if t.Step > 0 {
							t.num = t.Min + float64(int((t.num-t.Min)/t.Step+.5))*t.Step
						}
						apply()
					})
				case "color":
					ui.ColorWell(c.Key("control"), &t.color).Label(t.Label).OnChange(apply)
				case "toggle":
					ui.Switch(c.Key("control"), &t.on).Label(t.Label).OnChange(apply)
				case "select":
					ui.Select(c.Key("control"), &t.Value, t.Options).FillWidth().Label(t.Label).OnChange(func() { p.applyTweaks(a.Path) })
				}
			})
		}
		ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
			p.textButton(c, pal, "reset", "Reset", !b.tweaked(), false, func() { p.resetTweaks(a.Path) })
			p.textButton(c, pal, "keep", "Ask to keep", !b.tweaked(), true, func() { p.keepTweaks(a.Path) })
		})
	})
}

// card edits the open comment or note.
func (p *Plugin) card(c *ui.Context, pal native.Palette) {
	m := p.mark(p.editing)
	if m == nil {
		return
	}
	k := p.vp.zoom()
	x, y := p.vp.X+m.X*k, p.vp.Y+m.Y*k
	title := "Note"
	if m.Kind == commentMark {
		a, ok := p.current().board(m.Path)
		if !ok {
			return
		}
		ax, ay, _, _ := p.vp.screen(a)
		x, y = ax+m.X*k, ay+m.Y*k
		title = "Comment"
	}
	left := max(8, min(x+18, p.size[0]-cardWidth-8))
	top := max(8, min(y-12, p.size[1]-210))
	ui.Column(c.Key("card")).Absolute().Left(left).Top(top).Width(cardWidth).Background(pal.Raised).Border(1, pal.Hairline).
		Radius(10).Padding(12).Gap(8).Shadow(0, 4, 16, 0, pal.Hairline).Label(title).Children(func() {
		c.OnShortcut(0, ui.KeyEscape, p.closeCard)
		ui.Text(c, title+" "+strconv.Itoa(p.number(m.ID))+" · "+m.status()).FontSize(12.5).FontWeight(600).TextColor(pal.Text)
		if m.Kind == commentMark {
			ui.Text(c, m.Selector).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		}
		if m.Added && !m.Resolved {
			p.textButton(c, pal, "add-again", "Add to message again", false, false, func() { p.addMarkAgain(m.ID) })
		}
		in := ui.TextArea(c.Key("words"), &p.text).Placeholder("What should change?").FillWidth().Height(72).Label(title + " words")
		if p.cardOn {
			in.Focus()
			p.cardOn = false
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if m.Words != "" {
				p.iconButton(c, pal, "delete", "Delete", lucide.Trash, false, false, func() { p.deleteMark(m.ID) })
				label := "Resolve"
				if m.Resolved {
					label = "Reopen"
				}
				p.textButton(c, pal, "resolve", label, false, false, func() { p.resolveMark(m.ID) })
			}
			ui.Box(c).Grow(1)
			p.textButton(c, pal, "cancel", "Cancel", false, false, p.closeCard)
			p.textButton(c, pal, "save", "Save", false, true, p.saveCard)
		})
	})
}

// sendBar offers to put the comments and notes in the composer.
func (p *Plugin) sendBar(c *ui.Context, pal native.Palette) {
	n := len(p.pending())
	if n == 0 || p.editing != 0 {
		return
	}
	label := "Add 1 comment to the message"
	if n > 1 {
		label = "Add " + strconv.Itoa(n) + " comments to the message"
	}
	ui.Row(c.Key("send-bar")).Absolute().Left(0).Right(0).Bottom(56).Justify(ui.Center).PassThrough().Children(func() {
		b := ui.ButtonBase(c.Key("send")).Label(label).Height(34).Padding(0, 14).Radius(17).Gap(7).AlignItems(ui.Center).
			Background(pal.Text).Cursor(ui.CursorPointer).Shadow(0, 2, 8, 0, pal.Hairline)
		b.OnClick(p.sendMarks)
		b.Children(func() {
			ui.Icon(c, lucide.MessageCircle()).FontSize(14).TextColor(pal.Bg)
			ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(pal.Bg)
		})
	})
}

// empty is the canvas before the folder has an artboard.
func (p *Plugin) empty(c *ui.Context, pal native.Palette) {
	ui.Column(c.Key("empty")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(14).Padding(32).Children(func() {
		if p.scanning.Running() && len(p.found.Pages) == 0 {
			ui.Text(c, "Looking for designs…").FontSize(13).TextColor(pal.Muted)
			return
		}
		ui.Box(c).Size(120, 96).Draw(func(painter *ui.Painter, r ui.Rect) { shapes(painter, r, pal) })
		ui.Text(c, "Describe an idea and turn it into a design").FontSize(22).FontWeight(600).TextColor(pal.Text).
			TextAlign(ui.Center).MaxWidth(360).LineHeight(1.25)
		ui.Text(c, "Ask in the chat. Each page the agent writes shows here as an artboard and updates as it works.").
			FontSize(12.5).LineHeight(1.45).TextColor(pal.Muted).TextAlign(ui.Center).MaxWidth(360)
		p.systemMenu(c, pal, true)
		p.textButton(c, pal, "new-artboard", "New artboard", p.creating.Running(), false, p.newArtboard)
		ui.Row(c).Gap(6).Wrap().Justify(ui.Center).MaxWidth(440).Margin(6, 0, 0, 0).Children(func() {
			for _, s := range suggestions {
				p.textButton(c, pal, s.title, s.title, false, false, func() {
					p.chat.SetDraft(appendDraft(p.chat.DraftText(), s.prompt))
				})
			}
		})
	})
}

// shapes draws a triangle, a circle and a square, the empty canvas's picture.
func shapes(painter *ui.Painter, r ui.Rect, pal native.Palette) {
	ink, w := pal.Text, float32(2)
	bx, by := r.X+20, r.Y+r.H-4
	painter.StrokePolyline([][2]float32{{bx, by}, {bx + 24, r.Y + 4}, {bx + 48, by}, {bx, by}}, w, ink)
	painter.Fill(ui.Rect{X: r.X + 62, Y: r.Y + 24, W: 34, H: 34}, pal.Selected, 17)
	painter.Stroke(ui.Rect{X: r.X + 62, Y: r.Y + 24, W: 34, H: 34}, ink, 17, w)
	painter.Fill(ui.Rect{X: r.X + 58, Y: r.Y + 58, W: 42, H: 34}, pal.Bg, 0)
	painter.Stroke(ui.Rect{X: r.X + 58, Y: r.Y + 58, W: 42, H: 34}, ink, 0, w)
}

func (p *Plugin) iconButton(c *ui.Context, pal native.Palette, key, label string, icon func() *ui.SVG, on, disabled bool, run func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Size(28, 28).Radius(7).Center().Shrink(0).
		Disabled(disabled).Cursor(ui.CursorPointer)
	switch {
	case on:
		b.Background(pal.Selected)
	case b.Hovered() && !disabled:
		b.Background(pal.Hover)
	}
	b.OnClick(run)
	b.Children(func() {
		color := pal.Muted
		switch {
		case disabled:
			color = pal.Faint
		case on:
			color = pal.Text
		}
		ui.Icon(c, icon()).FontSize(14).TextColor(color)
	})
}

func (p *Plugin) smallButton(c *ui.Context, pal native.Palette, key, label string, icon func() *ui.SVG, run func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Size(22, 22).Radius(5).Center().Shrink(0).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(run)
	b.Children(func() { ui.Icon(c, icon()).FontSize(12).TextColor(pal.Muted) })
}

func (p *Plugin) textButton(c *ui.Context, pal native.Palette, key, label string, disabled, primary bool, run func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Height(28).Padding(0, 11).Radius(7).Center().Shrink(0).
		Disabled(disabled).Cursor(ui.CursorPointer)
	color := pal.Text
	switch {
	case primary && !disabled:
		b.Background(pal.Text)
		color = pal.Bg
	case b.Hovered() && !disabled:
		b.Background(pal.Hover).Border(1, pal.Hairline)
	default:
		b.Border(1, pal.Hairline)
	}
	if disabled {
		color = pal.Faint
	}
	b.OnClick(run)
	b.Children(func() { ui.Text(c, label).FontSize(12.5).TextColor(color).SingleLine() })
}

// titleButton starts a new design from the title strip, while the
// conversation page, the default, is on show. It shows pressed beside a
// design.
func (p *Plugin) titleButton(c *ui.Context) {
	if page := p.host.Page(); page != chat.PageID && page != "" {
		return
	}
	pal := native.PaletteFor(c)
	label := "New design"
	b := ui.ButtonBase(c.Key("design-new")).Label(label).Tooltip(label+"  ⇧⌘D").Size(28, 28).Radius(7).Center().
		Disabled(!p.canDesign()).Cursor(ui.CursorPointer)
	switch {
	case p.open:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(p.newDesign)
	b.Children(func() {
		color := pal.Muted
		if p.open {
			color = pal.Text
		}
		ui.Icon(c, lucide.Palette()).FontSize(15).TextColor(color)
	})
}
