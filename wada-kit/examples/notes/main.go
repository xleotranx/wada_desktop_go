// Notes is the smallest app built on the kit: one plugin with a page, a
// command in the menu bar, a saved setting and a settings section, beside
// the kit's own settings page and inspector. Run it with
//
//	go run ./examples/notes
//
// NOTES_DATA_DIR keeps its settings elsewhere; the other variables
// shell.App.Env names work too.
package main

import (
	"slices"
	"strings"

	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"github.com/xleotranx/wada-kit/plugins/inspector"
	kitsettings "github.com/xleotranx/wada-kit/plugins/settings"
	kitshell "github.com/xleotranx/wada-kit/shell"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

func main() {
	kitshell.Run(kitshell.App{
		Name: "Notes", Folder: "wada-kit-notes", Env: "NOTES",
		Width: 900, Height: 640, MinWidth: 600, MinHeight: 400,
	},
		&notes{},
		&inspector.Plugin{},
		&kitsettings.Plugin{},
	)
}

// notes keeps a list of short notes in its settings.
type notes struct {
	ctx    *kernel.Context
	host   shell.Host
	saved  *kernel.Setting[[]string]
	newest *kernel.Setting[bool] // newest first
	draft  string
	focus  bool
}

func (*notes) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "notes", Title: "Notes", Locked: true,
		Description: "Short notes, kept in the settings.", Requires: []kernel.Named{shell.HostKey}}
}

func (n *notes) Setup(c *kernel.Context) error {
	n.ctx = c
	n.host = kernel.Require(c, shell.HostKey)
	n.saved = kernel.NewSetting(c, "list", []string{})
	n.newest = kernel.NewSetting(c, "newest-first", true)
	for _, err := range []error{
		kernel.Contribute(c, shell.Pages, "notes.page", 0, shell.Page{Title: func() string { return "Notes" }, Render: n.page}),
		kernel.Contribute(c, kernel.Commands, "notes.new", 0, kernel.Command{
			Title: "New Note", Menu: "File", Keys: []string{"cmd+n"},
			Run: func() { n.host.ShowPage("notes.page"); n.focus = true }}),
		kernel.Contribute(c, settings.Sections, "notes.settings", 10, settings.Section{
			Title: "Notes", Description: "How the list is ordered.", Render: n.settings}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *notes) page(c *ui.Context) {
	pal := native.PaletteFor(c)
	list := n.saved.Get()
	order := make([]int, len(list)) // indexes into list, in the order shown
	for i := range order {
		order[i] = i
	}
	if n.newest.Get() {
		slices.Reverse(order)
	}
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		box := components.TextBox(c, &n.draft, components.TextBoxOptions{}).FillWidth().
			Label("New note").Placeholder("Write a note and press Return")
		box.OnSubmit(n.add)
		if n.focus {
			box.Focus()
			n.focus = false
		}
		if len(list) == 0 {
			ui.Text(c, "No notes yet.").FontSize(13).TextColor(pal.Muted)
		}
		for _, i := range order {
			c.Key(i) // a note keeps its key wherever it shows
			ui.Box(c).FillWidth().Padding(10, 12).Radius(8).Background(pal.Raised).Children(func() {
				ui.Text(c, list[i]).FontSize(14).TextColor(pal.Text)
			})
		}
	})
}

func (n *notes) add() {
	text := strings.TrimSpace(n.draft)
	if text == "" {
		return
	}
	n.saved.Set(append(n.saved.Get(), text))
	n.draft = ""
}

func (n *notes) settings(c *ui.Context) {
	pal := native.PaletteFor(c)
	on := n.newest.Get()
	value := on
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
		ui.Text(c, "Newest first").FontSize(13.5).TextColor(pal.Text).Grow(1)
		components.Switch(c, &value, "Newest first", components.SwitchOptions{HideLabel: true})
	})
	if value != on { // a frame changes nothing; the change goes after it
		n.ctx.Post(func() { n.newest.Set(value) })
	}
}
