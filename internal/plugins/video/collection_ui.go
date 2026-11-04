package video

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	kit "wada-desktop-go/internal/contract/videokit"
)

func (p *Plugin) chooseVideoStarter(folder string) {
	if len(kernel.Items(p.ctx, kit.Collections)) == 0 {
		p.newVideoIn(folder)
		return
	}
	p.pickerFolder = folder
}

func (p *Plugin) templatePicker(c *ui.Context) {
	if p.pickerFolder == "" {
		return
	}
	pal := native.PaletteFor(c)
	c.OnShortcut(0, ui.KeyEscape, func() { p.ctx.Post(func() { p.pickerFolder = "" }) })
	ui.Overlay(c, func() {
		ui.Column(c.Key("video-starters")).Absolute().Top(60).Left(80).Right(80).Bottom(60).Padding(24).Gap(16).
			Background(pal.Bg).Border(1, pal.Hairline).Radius(12).Modal().Role(ui.RoleDialog).Label("Choose a video starter").Children(func() {
			ui.Text(c, "Choose a video starter").FontSize(22).FontWeight(600).TextColor(pal.Text)
			ui.Text(c, "Ready-made scenes. Edit their content, combine components, then export.").FontSize(13).TextColor(pal.Muted)
			ui.Scroll(c.Key("starter-list")).Fill().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Children(func() {
					for _, entry := range kernel.Items(p.ctx, kit.Collections) {
						collection := entry.Value
						if len(collection.Templates) == 0 {
							continue
						}
						ui.Text(c, collection.Title).FontSize(12).TextColor(pal.Faint)
						for _, t := range collection.Templates {
							ui.ButtonBase(c.Key(t.ID)).FillWidth().Padding(18).Radius(8).Border(1, pal.Hairline).Background(pal.Raised).
								Label(t.Title).Cursor(ui.CursorPointer).OnClick(func() {
								folder := p.pickerFolder
								snapshot, err := p.collectionSnapshot()
								if err != nil {
									p.host.Toast(ui.Toast{Title: "The component collection is invalid", Description: err.Error(), Type: components.ToastError})
									return
								}
								p.ctx.Post(func() {
									p.pickerFolder = ""
									p.makeVideo(folder, func(dir string) error { return newStructuredProject(dir, snapshot, t) })
								})
							}).Children(func() {
								ui.Column(c).FillWidth().Gap(6).Children(func() {
									ui.Text(c, t.Title).FontSize(17).FontWeight(600).TextColor(pal.Text)
									ui.Text(c, t.Description).FontSize(12).TextColor(pal.Muted).MaxLines(3)
									ui.Text(c, fmt.Sprintf("%d scenes · editable components · no AI generation needed", len(t.Scenes))).FontSize(11).TextColor(pal.Faint)
								})
							})
						}
					}
				})
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Button(c, "Start blank").OnClick(func() { folder := p.pickerFolder; p.ctx.Post(func() { p.pickerFolder = ""; p.newVideoIn(folder) }) })
				ui.Button(c, "Cancel").OnClick(func() { p.ctx.Post(func() { p.pickerFolder = "" }) })
			})
		})
	})
}

func (p *Plugin) saveStructuredField(id string, value any) {
	if p.pendingFields == nil {
		p.pendingFields = map[string]any{}
	}
	p.pendingFields[id] = value
	p.flushStructuredFields()
}

// Field controls can change faster than a disk save. Keep the latest value
// of every field and serialize their writes with generation and other edits.
func (p *Plugin) flushStructuredFields() {
	if len(p.pendingFields) == 0 || p.project == nil || p.project.Document == nil || p.loading.Running() || p.editing.Running() || p.adding.Running() {
		return
	}
	dir, fields := p.dir, p.pendingFields
	p.pendingFields = nil
	kernel.Go(p.navigation, &p.editing, func(ctx context.Context) (struct{}, error) {
		if err := ctx.Err(); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, changeDocument(dir, func(d *kit.Document) error {
			if d.Overrides == nil {
				d.Overrides = map[string]any{}
			}
			for id, value := range fields {
				if value == nil {
					delete(d.Overrides, id)
				} else {
					d.Overrides[id] = value
				}
			}
			return nil
		})
	}, func(_ struct{}, err error) {
		defer p.flushReload()
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The value was not saved", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.reload()
	})
}

// componentMenu uses pinned definitions by default. Importing an installed
// component is an explicit action and never upgrades an existing reference.
func (p *Plugin) componentMenu(m *ui.Menu) {
	if p.project == nil || p.project.Document == nil {
		return
	}
	groups := map[string][]kit.Component{}
	order := []string{}
	for _, c := range p.project.Document.Snapshot.Components {
		group := c.Group
		if group == "" {
			group = "Essentials"
		}
		if _, ok := groups[group]; !ok {
			order = append(order, group)
		}
		groups[group] = append(groups[group], c)
	}
	for _, group := range order {
		m.Submenu(group, func(m *ui.Menu) {
			for _, c := range groups[group] {
				if m.Item(c.Title).Chosen() {
					p.ctx.Post(func() { p.addComponent(c.Ref) })
				}
			}
		})
	}
	installed, err := p.collectionSnapshot()
	if err != nil {
		return
	}
	missing := []kit.Component{}
	for _, c := range installed.Components {
		if _, err := componentFor(p.project.Document, c.Ref); err != nil {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		m.Separator()
		m.Submenu("Add from installed library", func(m *ui.Menu) {
			for _, c := range missing {
				if m.Item(c.Title).Chosen() {
					p.ctx.Post(func() { p.addComponentFrom(c.Ref, &installed) })
				}
			}
		})
	}
}

func (p *Plugin) addComponent(ref kit.Ref) { p.addComponentFrom(ref, nil) }
func (p *Plugin) addComponentFrom(ref kit.Ref, installed *kit.Collection) {
	if p.project == nil || p.project.Document == nil || p.adding.Running() || p.editing.Running() || p.loading.Running() {
		return
	}
	dir := p.dir
	kernel.Go(p.navigation, &p.adding, func(context.Context) (string, error) {
		var added string
		err := changeDocument(dir, func(d *kit.Document) error {
			if installed != nil {
				if err := importComponent(d, *installed, ref); err != nil {
					return err
				}
			}
			c, err := componentFor(d, ref)
			if err != nil {
				return err
			}
			parts := []kit.Part{{ID: "content", Component: ref}}
			layout, err := layoutForParts(d, parts)
			if err != nil {
				return err
			}
			end := 0.0
			for _, s := range d.Scenes {
				end = math.Max(end, s.Start+s.Duration)
			}
			added = nextSceneID(d)
			duration := c.DefaultDuration
			if duration == 0 {
				duration = 6
			}
			d.Scenes = append(d.Scenes, kit.Scene{ID: added, Title: c.Title, Layout: layout, Start: end, Duration: duration, Parts: parts})
			return nil
		})
		return added, err
	}, func(added string, err error) {
		defer p.flushReload()
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The component was not added", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.unfocus()
		p.selected = added
		p.reload()
	})
}

// importComponent adds definitions and assets without replacing anything in an
// existing video. Importable components carry their visual styles themselves.
func importComponent(d *kit.Document, source kit.Collection, ref kit.Ref) error {
	if _, err := componentFor(d, ref); err == nil {
		return nil
	}
	c, err := componentFor(&kit.Document{Snapshot: source}, ref)
	if err != nil {
		return err
	}
	if d.Snapshot.RuntimeURL != source.RuntimeURL {
		return fmt.Errorf("the installed component uses a different animation runtime")
	}
	for name, data := range source.Assets {
		if old, ok := d.Snapshot.Assets[name]; ok && old != data {
			return fmt.Errorf("asset %s conflicts with this video's snapshot", name)
		}
	}
	if d.Snapshot.Assets == nil {
		d.Snapshot.Assets = map[string]string{}
	}
	for name, data := range source.Assets {
		if _, exists := d.Snapshot.Assets[name]; !exists {
			d.Snapshot.Assets[name] = data
		}
	}
	for _, ref := range c.Layouts {
		if _, err := layoutFor(d, ref); err == nil {
			continue
		}
		l, err := layoutFor(&kit.Document{Snapshot: source}, ref)
		if err != nil {
			return err
		}
		d.Snapshot.Layouts = append(d.Snapshot.Layouts, l)
	}
	d.Snapshot.Components = append(d.Snapshot.Components, c)
	return nil
}

// Collections share one namespace. Exact duplicate references are rejected by
// compilation rather than silently changing meaning with registration order.
func (p *Plugin) collectionSnapshot() (kit.Collection, error) {
	result := kit.Collection{Ref: kit.Ref{ID: "wada.snapshot", Version: 1}, Title: "Project components", Assets: map[string]string{}}
	for _, entry := range kernel.Items(p.ctx, kit.Collections) {
		c := entry.Value
		result.Components = append(result.Components, c.Components...)
		result.Themes = append(result.Themes, c.Themes...)
		result.Layouts = append(result.Layouts, c.Layouts...)
		result.Templates = append(result.Templates, c.Templates...)
		result.CSS += c.CSS
		for name, data := range c.Assets {
			if previous, exists := result.Assets[name]; exists && previous != data {
				return kit.Collection{}, fmt.Errorf("conflicting collection asset %s", name)
			} else {
				result.Assets[name] = data
			}
		}
		if c.RuntimeURL != "" {
			if result.RuntimeURL != "" && result.RuntimeURL != c.RuntimeURL {
				return kit.Collection{}, fmt.Errorf("collections declare conflicting animation runtimes")
			}
			result.RuntimeURL = c.RuntimeURL
		}
	}
	return result, nil
}

func (p *Plugin) editStructure(change func(*kit.Document) error) {
	if p.project == nil || p.project.Document == nil || p.editing.Running() || p.adding.Running() || p.loading.Running() {
		return
	}
	dir := p.dir
	kernel.Go(p.navigation, &p.editing, func(context.Context) (struct{}, error) { return struct{}{}, changeDocument(dir, change) }, func(_ struct{}, err error) {
		defer p.flushReload()
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The video did not change", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.undo = nil
		p.redo = nil
		p.editors = nil
		p.reload()
	})
}

func sceneIndex(d *kit.Document, id string) int {
	for i, s := range d.Scenes {
		if s.ID == id {
			return i
		}
	}
	return -1
}
func resequence(d *kit.Document) {
	start := 0.0
	for i := range d.Scenes {
		d.Scenes[i].Start = start
		start += d.Scenes[i].Duration
	}
}

func duplicateScene(d *kit.Document, id string) error {
	i := sceneIndex(d, id)
	if i < 0 {
		return fmt.Errorf("scene %s is missing", id)
	}
	data, err := json.Marshal(d.Scenes[i])
	if err != nil {
		return err
	}
	var scene kit.Scene
	if err := json.Unmarshal(data, &scene); err != nil {
		return err
	}
	scene.ID = nextSceneID(d)
	scene.Title += " (copy)"
	d.Scenes = append(d.Scenes, kit.Scene{})
	copy(d.Scenes[i+2:], d.Scenes[i+1:])
	d.Scenes[i+1] = scene
	for key, value := range d.Overrides {
		if strings.HasPrefix(key, id+"--") {
			d.Overrides[scene.ID+strings.TrimPrefix(key, id)] = value
		}
	}
	resequence(d)
	return nil
}

func (p *Plugin) sceneActions(m *ui.Menu, id string) {
	if p.project == nil || p.project.Document == nil {
		return
	}
	d := p.project.Document
	i := sceneIndex(d, id)
	if i < 0 {
		return
	}
	run := func(change func(*kit.Document) error) { p.ctx.Post(func() { p.editStructure(change) }) }
	if m.Item("Duplicate scene").Chosen() {
		run(func(d *kit.Document) error { return duplicateScene(d, id) })
	}
	for _, step := range []struct {
		label string
		delta int
	}{{"Move earlier", -1}, {"Move later", 1}} {
		if m.Item(step.label).Disabled(i+step.delta < 0 || i+step.delta >= len(d.Scenes)).Chosen() {
			run(func(d *kit.Document) error {
				j := sceneIndex(d, id)
				if j < 0 || j+step.delta < 0 || j+step.delta >= len(d.Scenes) {
					return fmt.Errorf("scene cannot move")
				}
				d.Scenes[j], d.Scenes[j+step.delta] = d.Scenes[j+step.delta], d.Scenes[j]
				resequence(d)
				return nil
			})
		}
	}
	m.Submenu("Layout", func(m *ui.Menu) {
		for _, l := range d.Snapshot.Layouts {
			if m.Item(l.Title).Disabled(!layoutFits(d, l, d.Scenes[i].Parts)).Checked(l.Ref == d.Scenes[i].Layout).Chosen() {
				run(func(d *kit.Document) error {
					j := sceneIndex(d, id)
					if j < 0 {
						return fmt.Errorf("scene is missing")
					}
					d.Scenes[j].Layout = l.Ref
					return nil
				})
			}
		}
	})
	m.Submenu("Add component to scene", func(m *ui.Menu) {
		for _, c := range d.Snapshot.Components {
			proposed := append(append([]kit.Part(nil), d.Scenes[i].Parts...), kit.Part{Component: c.Ref})
			_, fitErr := layoutForParts(d, proposed)
			if m.Item(c.Title).Disabled(fitErr != nil).Chosen() {
				run(func(d *kit.Document) error { return appendPart(d, id, c.Ref) })
			}
		}
	})
	if m.Item("Swap components").Disabled(len(d.Scenes[i].Parts) != 2).Chosen() {
		run(func(d *kit.Document) error {
			j := sceneIndex(d, id)
			if j < 0 || len(d.Scenes[j].Parts) != 2 {
				return fmt.Errorf("two components are required")
			}
			parts := d.Scenes[j].Parts
			parts[0], parts[1] = parts[1], parts[0]
			return nil
		})
	}
	m.Separator()
	if m.Item("Remove scene").Disabled(len(d.Scenes) < 2).Chosen() {
		run(func(d *kit.Document) error {
			j := sceneIndex(d, id)
			if j < 0 {
				return fmt.Errorf("scene is missing")
			}
			d.Scenes = append(d.Scenes[:j], d.Scenes[j+1:]...)
			for key := range d.Overrides {
				if strings.HasPrefix(key, id+"--") {
					delete(d.Overrides, key)
				}
			}
			resequence(d)
			return nil
		})
	}
}

func appendPart(d *kit.Document, id string, ref kit.Ref) error {
	i := sceneIndex(d, id)
	if i < 0 {
		return fmt.Errorf("scene is missing")
	}
	if _, err := componentFor(d, ref); err != nil {
		return err
	}
	scene := &d.Scenes[i]
	proposed := append(append([]kit.Part(nil), scene.Parts...), kit.Part{Component: ref})
	layout, err := layoutForParts(d, proposed)
	if err != nil {
		return err
	}

	partID := ""
	for n := 1; partID == ""; n++ {
		candidate := fmt.Sprintf("part-%d", n)
		used := false
		for _, p := range scene.Parts {
			used = used || p.ID == candidate
		}
		if !used {
			partID = candidate
		}
	}
	scene.Layout = layout
	scene.Parts = append(scene.Parts, kit.Part{ID: partID, Component: ref})
	return nil
}
