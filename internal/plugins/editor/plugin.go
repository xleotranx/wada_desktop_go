// Package editor shows and edits workspace text files in a Files tab.
package editor

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
)

const pane = "editor.files"

type document struct {
	id       agent.WorkspaceID
	query    string
	paths    []string
	path     string
	text     string
	file     agent.WorkspaceFile
	search   kernel.Task
	transfer kernel.Task
	problem  string
	findErr  string
}

func (d *document) dirty() bool { return d.text != d.file.Text }

type Plugin struct {
	ctx   *kernel.Context
	ws    workspace.Service
	files agent.WorkspaceEditor
	docs  map[agent.WorkspaceID]*document
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "editor", Title: "Workspace files",
		Description: "Find and edit text files in workspace tabs.",
		Requires:    []kernel.Named{workspace.ServiceKey, agent.EditorKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.ws = kernel.Require(c, workspace.ServiceKey)
	p.files = kernel.Require(c, agent.EditorKey)
	p.docs = map[agent.WorkspaceID]*document{}
	kernel.On(c, workspace.Changed, func(struct{}) {
		if t, ok := kernel.Use(c, tabs.ServiceKey); ok {
			if active, ok := t.Active(); ok && active.Pane == pane {
				p.show(active)
			}
		}
	})
	kernel.Contribute(c, tabs.Panes, pane, 35, tabs.PaneKind{
		Title: func(string) string { return "Files" }, Icon: lucide.FileText, Render: p.view, Show: p.show,
	})
	return kernel.Contribute(c, kernel.Commands, "editor.open", 0, kernel.Command{
		Title: "Workspace Files", Menu: "File", Keys: []string{"shift+cmd+e"},
		Run: func() {
			if t, ok := kernel.Use(c, tabs.ServiceKey); ok {
				t.Open(pane, "", t.Space())
			}
		},
	})
}

func (p *Plugin) show(t tabs.Tab) {
	w, ok := p.ws.At(t.Folder)
	if !ok || p.docs[w.ID] != nil {
		return
	}
	d := &document{id: w.ID}
	p.docs[w.ID] = d
	p.find(d)
}

func (p *Plugin) find(d *document) {
	query := strings.TrimSpace(d.query)
	kernel.Go(p.ctx, &d.search, func(ctx context.Context) ([]string, error) {
		return p.files.FindWorkspaceFiles(ctx, d.id, query)
	}, func(paths []string, err error) {
		if err != nil {
			d.findErr = err.Error()
			return
		}
		d.paths, d.findErr = paths, ""
	})
}

func (p *Plugin) open(d *document, path string) {
	if path == "" || d.dirty() || d.transfer.Running() {
		return
	}
	d.problem = ""
	kernel.Go(p.ctx, &d.transfer, func(ctx context.Context) (agent.WorkspaceFile, error) {
		return p.files.OpenWorkspaceFile(ctx, d.id, path)
	}, func(file agent.WorkspaceFile, err error) {
		if err != nil {
			d.problem = err.Error()
			return
		}
		d.path, d.text, d.file = path, file.Text, file
	})
}

func (p *Plugin) save(d *document) {
	if !d.dirty() || d.transfer.Running() {
		return
	}
	d.problem = ""
	path, text, revision := d.path, d.text, d.file.Revision
	kernel.Go(p.ctx, &d.transfer, func(ctx context.Context) (agent.WorkspaceFile, error) {
		return p.files.SaveWorkspaceFile(ctx, d.id, path, text, revision)
	}, func(file agent.WorkspaceFile, err error) {
		if err != nil {
			d.problem = "Save failed: " + err.Error()
			return
		}
		d.file, d.text = file, file.Text
	})
}

func (p *Plugin) view(c *ui.Context, t tabs.Tab) {
	pal := native.PaletteFor(c)
	w, ok := p.ws.At(t.Folder)
	d := p.docs[w.ID]
	if !ok || d == nil {
		ui.Text(c, "Choose an active workspace to open its files.").TextColor(pal.Muted).Padding(16)
		return
	}
	c.Key(string(d.id))
	ui.Row(c).Fill().Gap(12).Padding(14).Children(func() {
		ui.Column(c.Key("find")).Width(230).Gap(6).Children(func() {
			components.TextBox(c.Key("query"), &d.query, components.TextBoxOptions{}).FillWidth().
				Placeholder("Find files").OnSubmit(func() { p.find(d) })
			ui.Row(c).Gap(5).Children(func() {
				components.Button(c, "Search", components.ButtonSecondary).OnClick(func() { p.find(d) })
				components.Button(c, "Open path", components.ButtonSecondary).
					Disabled(d.dirty() || d.transfer.Running() || strings.TrimSpace(d.query) == "").
					OnClick(func() { p.open(d, strings.TrimSpace(d.query)) })
			})
			if d.findErr != "" {
				ui.Text(c, d.findErr).TextColor(pal.DangerText)
			}
			ui.Scroll(c.Key("results")).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					for _, path := range d.paths {
						components.Button(c.Key(path), path, components.ButtonGhost).FillWidth().Tooltip(path).
							Disabled(d.dirty() || d.transfer.Running()).OnClick(func() { p.open(d, path) })
					}
				})
			})
		})
		ui.Column(c.Key("document")).Grow(1).MinWidth(0).Gap(8).Children(func() {
			if d.problem != "" {
				ui.Text(c, d.problem).TextColor(pal.DangerText)
			}
			if d.path == "" {
				ui.Text(c, "Choose a text file to edit.").TextColor(pal.Muted)
				return
			}
			ui.Text(c, d.path).TextColor(pal.Text).SingleLine().Ellipsis("…").Tooltip(d.path)
			ui.Row(c).Gap(6).Children(func() {
				components.Button(c, "Save", components.ButtonPrimary).Disabled(!d.dirty() || d.transfer.Running()).
					OnClick(func() { p.save(d) })
				if d.dirty() {
					components.Button(c, "Discard draft", components.ButtonSecondary).Disabled(d.transfer.Running()).
						OnClick(func() { d.text, d.problem = d.file.Text, "" })
				}
				components.Button(c, "Reload", components.ButtonSecondary).Disabled(d.dirty() || d.transfer.Running()).
					OnClick(func() { p.open(d, d.path) })
			})
			ui.TextArea(c.Key(d.path), &d.text).Grow(1).MinHeight(0).FillWidth().
				Font("monospace").FontSize(13).ReadOnly(d.transfer.Running()).
				OnShortcut(ui.Cmd, ui.KeyS, func() { p.save(d) })
		})
	})
}
