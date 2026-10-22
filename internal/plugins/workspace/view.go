package workspace

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// Picker IDs that aren't folders; a folder's ID is its path, which is
// absolute.
const (
	noFolder     = "(none)"
	chooseFolder = "(choose)"
)

// chip is the composer's folder picker. It shows only for a new chat that
// hasn't been sent: a conversation keeps the folder it started in.
func (p *Plugin) chip(c *ui.Context) {
	conv := p.chat.Current()
	if conv.Session != "" || conv.Starting {
		return
	}
	pal := native.PaletteFor(c)
	options := make([]onechat.Option, 0, len(p.folders)+2)
	for _, f := range p.folders {
		options = append(options, onechat.Option{ID: f.Path, Label: filepath.Base(f.Path), Detail: native.Tilde(filepath.Dir(f.Path))})
	}
	options = append(options,
		onechat.Option{ID: noFolder, Label: "No folder", Detail: "The agent's default"},
		onechat.Option{ID: chooseFolder, Label: "Choose folder…"})
	label, selected, tooltip := "No folder", noFolder, "Folder: the agent's default"
	if conv.Directory != "" {
		label, selected, tooltip = filepath.Base(conv.Directory), conv.Directory, "Folder: "+native.Tilde(conv.Directory)
	}
	picked := onechat.Picker(c, &p.picker, onechat.PickerOptions{
		Label: label, SearchLabel: "Search folders", EmptyText: "No folders match",
		Icon: lucide.Folder, Options: options, Selected: selected, Width: 300,
	})
	picked.Trigger.MaxWidth(180).Tooltip(tooltip)
	if conv.Directory == "" {
		picked.Trigger.TextColor(pal.Muted)
	}
	if chosen := picked.Chosen; chosen != "" {
		// Choosing is not rendering's job: act once the frame is done.
		p.ctx.Post(func() { p.pick(chosen) })
	}
}

func (p *Plugin) pick(id string) {
	switch id {
	case chooseFolder:
		p.ChooseFolder(p.chat.SetDirectory)
	case noFolder:
		p.chat.SetDirectory("")
	default:
		p.chat.SetDirectory(id)
	}
}
