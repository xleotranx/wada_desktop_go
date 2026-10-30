// Package flows contributes Wada's conversational workflow definitions.
package flows

import (
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
)

type Plugin struct{}

func New() kernel.Plugin { return &Plugin{} }
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "flows", Title: "Built-in workflows", Description: "Plan, code, review in the current conversation.", Requires: []kernel.Named{chat.ControllerKey}}
}
func (*Plugin) Setup(c *kernel.Context) error {
	f := planCodeReview
	kernel.Contribute(c, wf.Definitions, f.ID, 0, f)
	kernel.Contribute(c, chat.Workflows, f.ID, 0, chat.Workflow{Title: f.Title, Summary: f.Summary})
	return kernel.Contribute(c, kernel.Commands, f.ID, 0, kernel.Command{Title: f.Title, Menu: "Agent", Run: func() {
		kernel.Emit(c, chat.WorkflowSelected, f.ID)
		ctl := kernel.Require(c, chat.ControllerKey)
		ctl.SetDraft(ctl.DraftText())
	}})
}
