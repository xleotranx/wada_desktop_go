package folderpicker

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// Picker IDs that aren't workspaces; a workspace's ID is its directory, which
// is absolute.
const (
	inbox      = "(inbox)"
	addProject = "(add)"
)

// chip is the composer's workspace picker. It shows only for a new chat that
// hasn't been sent: a conversation keeps the workspace it started in.
func (p *Plugin) chip(c *ui.Context) {
	conv := p.chat.Current()
	if conv.Session != "" || conv.Starting {
		return
	}
	pal := native.PaletteFor(c)
	spaces := p.workspace.Workspaces()
	options := make([]onechat.Option, 0, len(spaces)+2)
	inboxDir := ""
	for _, w := range spaces {
		if box, ok := p.workspace.At(""); ok && box.ID == w.ID {
			inboxDir = w.Directory
			continue
		}
		options = append(options, onechat.Option{ID: w.Directory, Label: p.workspace.Name(w.Directory), Detail: native.Tilde(w.Directory)})
	}
	options = append(options,
		onechat.Option{ID: inbox, Label: "Inbox", Detail: "Chats without a project"},
		onechat.Option{ID: addProject, Label: "Add project…"})
	label, selected, tooltip := "Inbox", inbox, "Workspace: the inbox"
	if conv.Directory != "" && conv.Directory != inboxDir {
		name := p.workspace.Name(conv.Directory)
		label, selected, tooltip = name, conv.Directory, "Workspace: "+name+" · "+native.Tilde(conv.Directory)
	}
	picked := onechat.Picker(c, &p.picker, onechat.PickerOptions{
		Label: label, SearchLabel: "Search workspaces", EmptyText: "No workspaces match",
		Icon: lucide.Folder, Options: options, Selected: selected, Width: 300,
	})
	picked.Trigger.MaxWidth(180).Tooltip(tooltip)
	if selected == inbox {
		picked.Trigger.TextColor(pal.Muted)
	}
	if chosen := picked.Chosen; chosen != "" {
		// Choosing is not rendering's job: act once the frame is done.
		p.ctx.Post(func() { p.pick(chosen) })
	}
}

func (p *Plugin) pick(id string) {
	switch id {
	case addProject:
		p.workspace.ChooseProject(func(w agent.Workspace) { p.chat.SetDirectory(w.Directory) })
	case inbox:
		p.chat.SetDirectory("")
	default:
		p.chat.SetDirectory(id)
	}
}
