package video

import (
	"fmt"
	"strconv"

	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

const (
	editorGrow     = 2.4 // the editor's share of the page beside the conversation's 1
	editorMinWidth = 620
	sideWidth      = 236
	stagePad       = 10
)

// drawer is the editor beside the conversation: a header, the stage, the
// transport, the timeline and the sidebar.
func (p *Plugin) drawer(c *ui.Context) {
	if !p.open {
		return
	}
	pal := native.PaletteFor(c)
	ui.Row(c.Key("video")).Grow(editorGrow).MinWidth(editorMinWidth).FillHeight().Label("Video editor").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(pal.DrawerBg).Children(func() {
			p.header(c, pal, false)
			p.editor(c, pal)
		})
	})
}

// pageView is the editor in the whole window, for working on the timeline
// without the conversation beside it.
func (p *Plugin) pageView(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).Fill().MinHeight(0).Background(pal.Bg).Children(func() {
		p.header(c, pal, true)
		if !p.open || p.project == nil {
			p.empty(c, pal)
			return
		}
		p.editor(c, pal)
	})
}

// editor is what both the drawer and the page show: the stage, the
// transport, the timeline and the sidebar.
func (p *Plugin) editor(c *ui.Context, pal native.Palette) {
	if p.project == nil {
		p.empty(c, pal)
		return
	}
	ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).FillHeight().Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			p.stageView(c, pal)
			p.transport(c, pal)
			p.timelineView(c, pal)
		})
		ui.Box(c).Width(1).FillHeight().Background(pal.Hairline)
		p.side(c, pal)
	})
}

// header names the project and takes the actions: check, render, reload. A
// page has no drawer around it, so it offers the way back.
func (p *Plugin) header(c *ui.Context, pal native.Palette, page bool) {
	ui.Row(c).FillWidth().Height(48).Shrink(0).Padding(0, 10, 0, 14).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Icon(c, lucide.Clapperboard()).FontSize(14).TextColor(pal.Muted).Shrink(0)
		ui.Text(c, "Video").FontSize(13.5).FontWeight(600).TextColor(pal.Text).SingleLine().Shrink(0)
		if p.project != nil {
			p.projectMenu(c, pal)
		}
		if p.scene != nil {
			ui.Icon(c, lucide.ChevronRight()).FontSize(12).TextColor(pal.Faint).Shrink(0)
			ui.Text(c, p.scene.Comp.ID).FontSize(12).TextColor(pal.Text).SingleLine().Shrink(0)
			b := ui.ButtonBase(c.Key("back")).Label("Back to the main composition").Tooltip("Back to the main composition").
				Size(22, 22).Radius(5).Center().Shrink(0).Cursor(ui.CursorPointer)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.OnClick(func() { p.ctx.Post(p.unfocus) })
			b.Children(func() { ui.Icon(c, lucide.CornerUpLeft()).FontSize(11).TextColor(pal.Muted) })
		}
		ui.Box(c).Grow(1)
		p.iconButton(c, pal, "undo", "Undo the last timing change", lucide.Undo2, false, p.undoEdit, !p.canUndo())
		p.iconButton(c, pal, "redo", "Redo the timing change", lucide.Redo2, false, p.redoEdit, !p.canRedo())
		p.checkChip(c, pal)
		p.renderButton(c, pal)
		if page {
			p.iconButton(c, pal, "back-chat", "Back to the chat", lucide.CornerUpLeft, false, func() { p.host.ShowPage(chat.PageID) })
		} else {
			p.iconButton(c, pal, "open-page", "Open the editor in the window", lucide.Maximize2, false, p.openPage)
		}
		p.iconButton(c, pal, "reload", "Reload the project", lucide.RotateCw, false, p.reload)
	})
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
}

// projectMenu names the open project and chooses another of the folder's,
// so a HyperFrames project the conversation already has opens here too.
func (p *Plugin) projectMenu(c *ui.Context, pal native.Palette) {
	found := p.found
	if p.chat.Current().Session == "" {
		found = nil
	}
	label := p.project.Title
	b := ui.ButtonBase(c.Key("project")).Label(label).Tooltip("Choose the video of this folder").Height(24).
		Padding(0, 6).Radius(5).AlignItems(ui.Center).MinWidth(0).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	if len(found) > 1 {
		b.Menu(func(m *ui.Menu) {
			for _, r := range found {
				if m.Item(r.Title).Checked(r.Dir == p.dir).Chosen() {
					dir := r.Dir
					p.ctx.Post(func() { p.chooseProject(dir) })
				}
			}
		})
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Muted).SingleLine().Ellipsis("…").MinWidth(0)
		if len(found) > 1 {
			ui.Icon(c, lucide.ChevronDown()).FontSize(10).TextColor(pal.Faint).Shrink(0)
		}
	})
}

// chooseProject shows another project of the conversation's folder.
func (p *Plugin) chooseProject(dir string) {
	conv := p.chat.Current()
	if dir == "" || conv.Session == "" {
		return
	}
	p.remember(video{Origin: p.agent.Status().Origin, Session: conv.Session, Dir: dir})
	p.update(conv)
}

// checkChip says what the project's check found; clicking it shows it.
func (p *Plugin) checkChip(c *ui.Context, pal native.Palette) {
	if p.project == nil {
		return
	}
	color := pal.Muted
	label := "No problems"
	switch {
	case p.check.Errors > 0:
		color, label = pal.Danger, p.check.message()
	case p.check.Warnings > 0:
		color, label = pal.Warning, p.check.message()
	}
	b := ui.ButtonBase(c.Key("check")).Label(label).Tooltip("Show what the check found").Height(26).Padding(0, 8).
		Radius(6).Gap(5).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	if p.panel == checkPanel {
		b.Background(pal.Selected)
	}
	b.OnClick(func() { p.ctx.Post(func() { p.panel = checkPanel }) })
	b.Children(func() {
		ui.Icon(c, lucide.CircleAlert()).FontSize(12).TextColor(color)
		ui.Text(c, label).FontSize(11.5).TextColor(color).SingleLine().FontFeatures("tnum")
	})
}

// renderButton renders the project to MP4, or says it is rendering.
func (p *Plugin) renderButton(c *ui.Context, pal native.Palette) {
	if p.project == nil {
		return
	}
	running := p.rendering.Running()
	label := "Render"
	if running {
		label = "Rendering…"
	}
	b := ui.ButtonBase(c.Key("render")).Label(label).Tooltip("Render the project to an MP4 with the HyperFrames CLI").
		Height(26).Padding(0, 9).Radius(6).Gap(5).AlignItems(ui.Center).Shrink(0).
		Disabled(running || p.check.Errors > 0).Cursor(ui.CursorPointer)
	if b.Hovered() && !running && p.check.Errors == 0 {
		b.Background(pal.Hover)
	}
	b.OnClick(p.render)
	b.Children(func() {
		if running {
			ui.Icon(c, lucide.Loader()).FontSize(12).TextColor(pal.Muted)
		} else {
			ui.Icon(c, lucide.Film()).FontSize(12).TextColor(pal.Muted)
		}
		ui.Text(c, label).FontSize(11.5).TextColor(pal.Text).SingleLine()
	})
	p.renderOptionsButton(c, pal)
}

// renderOptionsButton chooses the render's format and quality.
func (p *Plugin) renderOptionsButton(c *ui.Context, pal native.Palette) {
	b := ui.ButtonBase(c.Key("render-options")).Label("Render options").Size(22, 26).Radius(6).Center().Shrink(0).
		Disabled(p.rendering.Running()).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Menu(func(m *ui.Menu) {
		for _, f := range renderFormats {
			if m.Item("Format: " + f.Name).Checked(p.renderOpts.Format == f.Name).Chosen() {
				next := p.renderOpts
				next.Format = f.Name
				p.ctx.Post(func() { p.setRenderOptions(next) })
			}
		}
		m.Separator()
		for _, q := range renderQualities {
			if m.Item("Quality: " + q).Checked(p.renderOpts.Quality == q).Chosen() {
				next := p.renderOpts
				next.Quality = q
				p.ctx.Post(func() { p.setRenderOptions(next) })
			}
		}
	})
	b.Children(func() {
		ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(pal.Muted)
	})
}

// stageView shows the composition's current frame at the size of the room.
func (p *Plugin) stageView(c *ui.Context, pal native.Palette) {
	ui.Box(c.Key("stage")).FillWidth().Grow(1).MinHeight(0).Clip().Background(pal.Bg).Bind(&p.stage).Label("Stage").
		Children(func() {
			if p.webErr != "" {
				ui.Text(c, p.webErr).FontSize(12).TextColor(pal.Danger).Padding(12).MaxLines(3)
				return
			}
			if p.web == nil || p.project == nil {
				return
			}
			r := p.stage.Bounds(c)
			if r.W <= 2*stagePad || r.H <= 2*stagePad {
				return
			}
			w, h := p.project.Size()
			k := float64(min((r.W-2*stagePad)/float32(w), (r.H-2*stagePad)/float32(h)))
			if k <= 0 {
				return
			}
			if k != p.zoom {
				p.zoom = k
				p.web.SetZoom(k)
			}
			sw, sh := float32(w)*float32(k), float32(h)*float32(k)
			p.web.Show(c.Key("preview")).Absolute().Left((r.W - sw) / 2).Top((r.H - sh) / 2).Width(sw).Height(sh)
		})
	if len(p.info.Errors) > 0 {
		ui.Text(c, p.info.Errors[0]).FontSize(11).TextColor(pal.Warning).Absolute().Left(12).Bottom(6).
			MaxLines(2).PassThrough()
	}
}

// transport is the play button, the timecode and the video's shape.
func (p *Plugin) transport(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Height(40).Shrink(0).Padding(0, 12).AlignItems(ui.Center).Gap(8).Children(func() {
		p.playButton(c, pal)
		ui.Text(c, timecode(p.displayTime())).FontSize(12).TextColor(pal.Text).FontFeatures("tnum").SingleLine().Shrink(0)
		ui.Text(c, "/ "+timecode(p.displayDuration())).FontSize(12).TextColor(pal.Muted).FontFeatures("tnum").SingleLine().Shrink(0)
		ui.Text(c, "f "+strconv.Itoa(int(p.displayTime()*p.fps()+0.5))).FontSize(11).TextColor(pal.Faint).FontFeatures("tnum").
			SingleLine().Shrink(0)
		ui.Box(c).Grow(1)
		if p.viewZoom > 1 {
			ui.Text(c, fmt.Sprintf("%.1f×", p.viewZoom)).FontSize(11).TextColor(pal.Faint).FontFeatures("tnum").
				SingleLine().Shrink(0)
		}
		p.iconButton(c, pal, "zoom-out", "Zoom out (⌘−)", lucide.ZoomOut, false,
			func() { p.zoomAt(1/1.5, timelinePad) })
		p.iconButton(c, pal, "zoom-fit", "Fit the timeline (⌘0)", lucide.Maximize2, false,
			func() { p.viewStart, p.viewZoom = 0, 1 })
		p.iconButton(c, pal, "zoom-in", "Zoom in (⌘=)", lucide.ZoomIn, false,
			func() { p.zoomAt(1.5, timelinePad) })
		if p.project != nil {
			w, h := p.project.Size()
			ui.Text(c, fmt.Sprintf("%g×%g · %g fps", w, h, p.fps())).FontSize(11).TextColor(pal.Faint).
				FontFeatures("tnum").SingleLine().Shrink(0)
		}
	})
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
}

// playButton plays or pauses the preview.
func (p *Plugin) playButton(c *ui.Context, pal native.Palette) {
	b := ui.ButtonBase(c.Key("play")).Label("Play").Tooltip("Play the preview (space)").Size(26, 26).Radius(13).Center().
		Shrink(0).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(p.togglePlay)
	b.Children(func() {
		if p.playing {
			ui.Icon(c, lucide.Pause()).FontSize(12).TextColor(pal.Text)
			return
		}
		ui.Icon(c, lucide.Play()).FontSize(12).TextColor(pal.Text)
	})
}

// side is the sidebar: the scenes, the selected clip and the check.
func (p *Plugin) side(c *ui.Context, pal native.Palette) {
	ui.Column(c).Width(sideWidth).FillHeight().Background(pal.DrawerBg).Children(func() {
		ui.Row(c).FillWidth().Height(38).Shrink(0).Padding(0, 6, 0, 10).AlignItems(ui.Center).Gap(2).Children(func() {
			p.panelTab(c, pal, "Scenes", scenesPanel)
			p.panelTab(c, pal, "Clip", clipPanel)
			p.panelTab(c, pal, "Vars", varsPanel)
			p.panelTab(c, pal, "Check", checkPanel)
		})
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
		switch p.panel {
		case scenesPanel:
			p.scenesView(c, pal)
		case clipPanel:
			p.clipPanel(c, pal)
		case varsPanel:
			p.variablesView(c, pal)
		case checkPanel:
			p.checkPanel(c, pal)
		}
		if p.rendering.Running() || p.renderErr != "" || p.renderOut != "" {
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(pal.Hairline)
			p.renderStatus(c, pal)
		}
	})
}

// panelTab is one sidebar tab.
func (p *Plugin) panelTab(c *ui.Context, pal native.Palette, label string, kind panelKind) {
	b := ui.ButtonBase(c.Key("tab-"+label)).Label(label).Height(26).Padding(0, 8).Radius(6).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if p.panel == kind {
		b.Background(pal.Selected)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() { p.ctx.Post(func() { p.panel = kind }) })
	b.Children(func() {
		ui.Text(c, label).FontSize(11.5).TextColor(pal.Text).SingleLine()
	})
}

// scenesView lists the scenes the root mounts, in timeline order.
func (p *Plugin) scenesView(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Height(32).Shrink(0).Padding(0, 10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Scenes").FontSize(11).TextColor(pal.Faint).SingleLine().Shrink(0)
		ui.Box(c).Grow(1)
		if p.scene == nil {
			b := ui.ButtonBase(c.Key("add-scene")).Label("Add scene").Height(22).Padding(0, 6).Radius(5).
				AlignItems(ui.Center).Shrink(0).Disabled(p.adding.Running()).Cursor(ui.CursorPointer)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.OnClick(p.newScene)
			b.Children(func() {
				ui.Icon(c, lucide.Plus()).FontSize(11).TextColor(pal.Muted)
				ui.Text(c, "Add scene").FontSize(11).TextColor(pal.Text).SingleLine()
			})
		}
	})
	ui.Scroll(c.Key("scenes")).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(6, 8).Gap(2).Children(func() {
			if p.project == nil || len(p.project.Scenes) == 0 {
				ui.Text(c, "This video has one composition.").FontSize(11.5).TextColor(pal.Faint).Padding(4, 6)
				return
			}
			for _, s := range p.project.Scenes {
				p.sceneRow(c, pal, s)
			}
		})
	})
}

// sceneRow is one scene of the sidebar.
func (p *Plugin) sceneRow(c *ui.Context, pal native.Palette, s *Scene) {
	host := s.Host
	focused := p.scene != nil && p.scene.Host != nil && p.scene.Host.ID == host.ID
	b := ui.ButtonBase(c.Key("scene-"+host.ID)).Label("Edit "+host.Label).Tooltip("Edit the clips of "+host.Label).
		Height(40).FillWidth().Radius(6).Padding(6, 8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if focused {
		b.Background(pal.Selected)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() {
		p.ctx.Post(func() {
			p.panel = clipPanel
			p.focusOn(s)
			p.selectClip(host.ID)
		})
	})
	b.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, host.Label).FontSize(12).TextColor(pal.Text).SingleLine().Ellipsis("…")
			ui.Text(c, fmt.Sprintf("%s – %s", timecode(host.Start), timecode(host.End()))).FontSize(10.5).
				TextColor(pal.Faint).FontFeatures("tnum").SingleLine()
		})
		if focused {
			ui.Icon(c, lucide.CornerDownRight()).FontSize(11).TextColor(pal.Link).Shrink(0)
		} else {
			ui.Icon(c, lucide.ChevronRight()).FontSize(11).TextColor(pal.Faint).Shrink(0)
		}
	})
}

// clipPanel shows the selected clip and what can be done to it.
func (p *Plugin) clipPanel(c *ui.Context, pal native.Palette) {
	ui.Scroll(c.Key("clip")).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(10, 10).Gap(6).Children(func() {
			clip, ok := p.clip(p.selected)
			if !ok {
				ui.Text(c, "Select a clip on the timeline.").FontSize(11.5).TextColor(pal.Faint)
				ui.Text(c, "Drag it to move it, drag its edges to trim it.").FontSize(11).TextColor(pal.Faint).MaxLines(3)
				return
			}
			ui.Text(c, clip.Label).FontSize(12.5).TextColor(pal.Text).MaxLines(3)
			p.detail(c, pal, "File", clip.File)
			p.detail(c, pal, "Element", "<"+clip.Tag+">")
			p.detail(c, pal, "Start", timecode(clip.Start))
			if clip.HasDuration {
				p.detail(c, pal, "Duration", timecode(clip.Duration))
				p.detail(c, pal, "End", timecode(clip.End()))
			} else {
				p.detail(c, pal, "Duration", "from its media")
			}
			p.detail(c, pal, "Track", trackName(clip.TrackKind)+" "+strconv.Itoa(clip.Track))
			if clip.StartExpr != "" && clip.StartExpr != formatSeconds(clip.Start) {
				p.detail(c, pal, "Written as", clip.StartExpr)
			}
			if clip.TextEditable {
				ui.Text(c, "Words").FontSize(10.5).TextColor(pal.Faint).SingleLine()
				ui.TextInput(c.Key("clip-text-"+clip.ID), &p.text).FillWidth().Label("The clip's words").
					OnSubmit(func() { p.saveText(clip) })
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
					ui.Text(c, "Enter saves").FontSize(9.5).TextColor(pal.Faint).SingleLine()
					ui.Box(c).Grow(1)
					b := ui.ButtonBase(c.Key("save-text-"+clip.ID)).Label("Save the words").Height(20).Padding(0, 8).
						Radius(4).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
					if b.Hovered() {
						b.Background(pal.Hover)
					}
					b.OnClick(func() { p.saveText(clip) })
					b.Children(func() { ui.Text(c, "Save").FontSize(10.5).TextColor(pal.Text).SingleLine() })
				})
			} else if clip.Text != "" {
				ui.Text(c, clip.Text).FontSize(10.5).TextColor(pal.Muted).MaxLines(3)
				ui.Text(c, "This element holds other elements, so its words are changed in its file.").
					FontSize(9.5).TextColor(pal.Faint).MaxLines(3)
			}
			ui.Text(c, "Drag the clip to move it; drag its edges to trim it. Changes are saved to the HTML file.").
				FontSize(10.5).TextColor(pal.Faint).MaxLines(4)
		})
	})
}

// detail is one labelled line of the clip panel.
func (p *Plugin) detail(c *ui.Context, pal native.Palette, name, value string) {
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, name).FontSize(11).TextColor(pal.Faint).Width(62).Shrink(0).SingleLine()
		ui.Text(c, value).FontSize(11).TextColor(pal.Text).MinWidth(0).SingleLine().Ellipsis("…").FontFeatures("tnum")
	})
}

// variablesView edits the values the focused composition declares.
func (p *Plugin) variablesView(c *ui.Context, pal native.Palette) {
	ui.Scroll(c.Key("variables")).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(10, 8).Gap(10).Children(func() {
			if comp := p.comp(); comp != nil {
				ui.Text(c, "Declared in "+comp.File).FontSize(10).TextColor(pal.Faint).SingleLine().Ellipsis("…")
			}
			if len(p.editors) == 0 {
				ui.Text(c, "This composition declares no variables.").FontSize(11.5).TextColor(pal.Faint).MaxLines(3)
				ui.Text(c, "A template declares them with data-composition-variables on its root.").FontSize(10.5).
					TextColor(pal.Faint).MaxLines(4)
				return
			}
			for _, e := range p.editors {
				p.variableRow(c, pal, e)
			}
		})
	})
}

// variableRow is one variable: its name, its type, its control and a way back
// to the declared default.
func (p *Plugin) variableRow(c *ui.Context, pal native.Palette, e *variableEditor) {
	ui.Column(c).FillWidth().Gap(3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, e.label()).FontSize(11.5).TextColor(pal.Text).SingleLine().Ellipsis("…").MinWidth(0)
			ui.Box(c).Grow(1)
			if p.overridden(e.ID) {
				b := ui.ButtonBase(c.Key("reset-"+e.ID)).Label("Reset "+e.label()).Tooltip("Back to the declared default").
					Size(18, 18).Radius(4).Center().Shrink(0).Cursor(ui.CursorPointer)
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.OnClick(func() { p.ctx.Post(func() { p.resetVariable(e) }) })
				b.Children(func() { ui.Icon(c, lucide.RotateCcw()).FontSize(10).TextColor(pal.Faint) })
			}
		})
		ui.Text(c, variableSummary(e.variable)).FontSize(9.5).TextColor(pal.Faint).SingleLine()
		key := c.Key("var-" + e.ID)
		switch e.Type {
		case "color":
			ui.ColorWell(key, &e.color).Label(e.label()).OnChange(func() { p.setVariable(e.ID, hexString(e.color)) })
		case "boolean":
			ui.Switch(key, &e.on).Label(e.label()).OnChange(func() { p.setVariable(e.ID, e.changed()) })
		case "enum":
			ui.Select(key, &e.text, e.Options).FillWidth().Label(e.label()).OnChange(func() { p.setVariable(e.ID, e.changed()) })
		default:
			ui.TextInput(key, &e.text).FillWidth().Label(e.label()).OnChange(func() {
				if v := e.changed(); v != nil {
					p.setVariable(e.ID, v)
				}
			})
		}
		if p.pinned(e.ID) {
			ui.Text(c, "The scene pins this value.").FontSize(9.5).TextColor(pal.Warning).SingleLine()
		}
	})
}

// checkPanel lists what the check found.
func (p *Plugin) checkPanel(c *ui.Context, pal native.Palette) {
	ui.Row(c).FillWidth().Height(32).Shrink(0).Padding(0, 10).AlignItems(ui.Center).Gap(4).Children(func() {
		label := "The editor's check"
		if p.cliChecked {
			label = "The HyperFrames CLI"
		}
		if p.checking.Running() {
			label = "Checking with the CLI…"
		}
		ui.Text(c, label).FontSize(10.5).TextColor(pal.Faint).SingleLine().MinWidth(0).Ellipsis("…")
		ui.Box(c).Grow(1)
		name := "Full check"
		if p.cliChecked {
			name = "Quick check"
		}
		b := ui.ButtonBase(c.Key("full-check")).Label(name).Height(22).Padding(0, 6).Radius(5).
			AlignItems(ui.Center).Shrink(0).Disabled(p.checking.Running()).Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
		b.OnClick(func() {
			p.ctx.Post(func() {
				if p.cliChecked {
					p.check, p.cliChecked = p.quick, false
					return
				}
				p.fullCheck()
			})
		})
		b.Children(func() { ui.Text(c, name).FontSize(10.5).TextColor(pal.Text).SingleLine() })
	})
	ui.Scroll(c.Key("check-list")).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(6, 8).Gap(2).Children(func() {
			if len(p.check.Findings) == 0 {
				ui.Text(c, "No problems.").FontSize(11.5).TextColor(pal.Link).Padding(4, 6)
				return
			}
			for i, f := range p.check.Findings {
				p.findingRow(c, pal, i, f)
			}
		})
	})
}

// findingRow is one finding of the check.
func (p *Plugin) findingRow(c *ui.Context, pal native.Palette, i int, f finding) {
	color := pal.Muted
	switch f.Severity {
	case sevError:
		color = pal.Danger
	case sevWarning:
		color = pal.Warning
	case sevInfo:
	}
	b := ui.ButtonBase(c.Key("finding-"+strconv.Itoa(i))).Label(f.Code).FillWidth().Radius(5).Padding(6, 6).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.OnClick(func() {
		p.ctx.Post(func() {
			p.panel = clipPanel
			if f.Clip != "" {
				p.focusClip(f.Clip)
			}
		})
	})
	b.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(6).Height(6).Radius(3).Background(color).Shrink(0)
				ui.Text(c, f.Code).FontSize(10.5).TextColor(color).SingleLine().MinWidth(0)
				ui.Box(c).Grow(1)
				if f.Source == "cli" {
					ui.Text(c, "CLI").FontSize(9).TextColor(pal.Link).SingleLine().Shrink(0)
				}
				if f.Line > 0 {
					ui.Text(c, strconv.Itoa(f.Line)).FontSize(10).TextColor(pal.Faint).FontFeatures("tnum").Shrink(0)
				}
			})
			ui.Text(c, f.Message).FontSize(10.5).TextColor(pal.Muted).MaxLines(2)
			if f.Fix != "" {
				ui.Text(c, f.Fix).FontSize(10).TextColor(pal.Faint).MaxLines(2)
			}
		})
	})
}

// empty is the editor without a project: the first video can start here.
func (p *Plugin) empty(c *ui.Context, pal native.Palette) {
	ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
		ui.Icon(c, lucide.Clapperboard()).FontSize(26).TextColor(pal.Faint)
		if p.loadErr != "" {
			ui.Text(c, "The video did not open").FontSize(13).TextColor(pal.Text)
			ui.Text(c, p.loadErr).FontSize(11.5).TextColor(pal.Danger).MaxLines(4)
			return
		}
		b := ui.ButtonBase(c.Key("back-chat")).Label("Back to the chat").Height(28).Padding(0, 12).Radius(7).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() { p.host.ShowPage(chat.PageID) })
		b.Children(func() {
			ui.Text(c, "Back to the chat").FontSize(12).TextColor(pal.Text)
		})
		ui.Text(c, "No video here yet").FontSize(13).TextColor(pal.Text)
		ui.Text(c, "Ask for a video, or start one with its own composition and timeline.").FontSize(11.5).
			TextColor(pal.Muted).MaxLines(3)
		if p.canVideo() {
			ui.ButtonBase(c.Key("new-video")).Label("New Video").Height(28).Padding(0, 12).Radius(7).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(p.newVideo).Children(func() {
				ui.Text(c, "New Video").FontSize(12).TextColor(pal.Text)
			})
		}
	})
}

// renderStatus is the sidebar's line for the render job.
func (p *Plugin) renderStatus(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().Padding(8, 10).Gap(3).Children(func() {
		switch {
		case p.rendering.Running():
			label := "Rendering the video…"
			if p.renderPct > 0 {
				label = fmt.Sprintf("Rendering the video… %d%%", p.renderPct)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, label).FontSize(11.5).TextColor(pal.Text).SingleLine().MinWidth(0)
				ui.Box(c).Grow(1)
				b := ui.ButtonBase(c.Key("stop-render")).Label("Stop the render").Height(20).Padding(0, 6).Radius(4).
					AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.OnClick(func() {
					p.ctx.Post(func() {
						p.rendering.Cancel()
						p.renderPct = 0
						p.renderLog = nil
					})
				})
				b.Children(func() { ui.Text(c, "Stop").FontSize(10.5).TextColor(pal.Danger).SingleLine() })
			})
			fill := float32(sideWidth-20) * float32(p.renderPct) / 100
			ui.Box(c).FillWidth().Height(4).Radius(2).Background(pal.Hairline).Children(func() {
				if fill > 0 {
					ui.Box(c).Absolute().Left(0).Top(0).Width(fill).Height(4).Radius(2).Background(pal.Link)
				}
			})
		case p.renderErr != "":
			ui.Text(c, "The render failed").FontSize(11.5).TextColor(pal.Danger).SingleLine()
			ui.Text(c, p.renderErr).FontSize(10).TextColor(pal.Muted).MaxLines(4)
		case p.renderOut != "":
			ui.Text(c, "Rendered").FontSize(11.5).TextColor(pal.Link).SingleLine()
			ui.Text(c, rel(p.dir, p.renderOut)).FontSize(10).TextColor(pal.Faint).MaxLines(2)
		}
	})
}

// titleButton starts a video from the title strip.
func (p *Plugin) titleButton(c *ui.Context) {
	if page := p.host.Page(); page != chat.PageID && page != "" {
		return
	}
	pal := native.PaletteFor(c)
	label := "New video"
	b := ui.ButtonBase(c.Key("video-new")).Label(label).Tooltip(label+"  ⇧⌘V").Size(28, 28).Radius(7).Center().
		Disabled(!p.canVideo()).Cursor(ui.CursorPointer)
	switch {
	case p.open:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(p.newVideo)
	b.Children(func() {
		color := pal.Muted
		if p.open {
			color = pal.Text
		}
		ui.Icon(c, lucide.Clapperboard()).FontSize(15).TextColor(color)
	})
}

// iconButton is a small icon button of the header.
func (p *Plugin) iconButton(c *ui.Context, pal native.Palette, key, label string, icon func() *ui.SVG, active bool, run func(), disabled ...bool) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Size(26, 26).Radius(6).Center().Shrink(0).
		Cursor(ui.CursorPointer)
	if len(disabled) > 0 && disabled[0] {
		b.Disabled(true)
	}
	switch {
	case active:
		b.Background(pal.Selected)
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.OnClick(run)
	b.Children(func() {
		color := pal.Muted
		if active {
			color = pal.Text
		}
		ui.Icon(c, icon()).FontSize(13).TextColor(color)
	})
}

// timecode writes a time as m:ss.cc.
func timecode(t float64) string {
	if t < 0 {
		t = 0
	}
	mins := int(t) / 60
	secs := int(t) % 60
	cs := int((t - float64(int(t))) * 100)
	return fmt.Sprintf("%d:%02d.%02d", mins, secs, cs)
}
