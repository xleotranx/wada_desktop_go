// Package folderpicker lets the user choose where a chat works: "Open
// Folder…" and the folder picker in a new chat's composer. The folders come
// from the workspace record, which knows nothing of the chat.
package folderpicker

import (
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin is the folder picker.
type Plugin struct {
	ctx       *kernel.Context
	chat      chat.Controller
	workspace workspace.Service
	picker    onechat.PickerState
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "folderpicker", Title: "Folder picker",
		Description: "Open Folder… and the composer's folder picker for a new chat.",
		Requires:    []kernel.Named{chat.ControllerKey, workspace.ServiceKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.workspace = kernel.Require(c, workspace.ServiceKey)
	kernel.Contribute(c, kernel.Commands, "folderpicker.open-folder", 1, kernel.Command{
		Title: "Open Folder…", Menu: "File", Keys: []string{"cmd+o"},
		Run: func() { p.workspace.ChooseFolder(p.chat.NewChat) }})
	return kernel.Contribute(c, chat.ComposerAccessory, "folderpicker.folder", -20, shell.View{Render: p.chip})
}
