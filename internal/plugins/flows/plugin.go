// Package flows is the app's own set of workflows, written in Go against the
// workflow contract and started from the composer's mode picker or the command
// palette. The first is plan, code, review.
package flows

import (
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"

	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin brings the app's flows.
type Plugin struct{}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "flows", Title: "Built-in workflows",
		Description: "Adds Plan → Code → Review to the composer's mode picker and All chats. Uses the Workflow engine.",
		Requires:    []kernel.Named{wf.ServiceKey},
	}
}

// Setup gives each flow a place in the composer's mode picker, where it takes
// the next composer message, and a command that asks for a brief. Each starts a
// separate run in the current chat's folder.
func (*Plugin) Setup(c *kernel.Context) error {
	svc := kernel.Require(c, wf.ServiceKey)
	for _, f := range []wf.Flow{planCodeReview} {
		kernel.Contribute(c, chat.Targets, "flows."+f.ID, 0, chat.Target{
			Group: "Workflows", Title: f.Title, Summary: f.Summary,
			Take: func(d chat.Draft) { svc.Start(f, wf.Input{Brief: d.Text, Directory: d.Directory}) }})
		kernel.Contribute(c, kernel.Commands, "flows."+f.ID, 0, kernel.Command{
			Title: f.Title + "…", Menu: "Agent", Run: func() { svc.Prompt(f) }})
	}
	return nil
}
