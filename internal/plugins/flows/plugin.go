// Package flows is the app's own set of workflows, written in Go against the
// workflow contract and started from the composer's mode picker or the command
// palette. The first is plan, code, review.
package flows

import (
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/kernel"
)

// Plugin brings the app's flows. The zero value is ready to install.
type Plugin struct{}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "flows", Title: "Flows",
		Description: "Ready-made workflows for the composer's mode picker and the command palette: plan, code, review.",
		Requires:    []kernel.Named{wf.ServiceKey},
	}
}

// Setup gives each flow a place in the composer's mode picker, where it takes
// the first message of a new chat, and a command that asks for a brief.
func (*Plugin) Setup(c *kernel.Context) error {
	svc := kernel.Require(c, wf.ServiceKey)
	for _, f := range []wf.Flow{planCodeReview} {
		for _, err := range []error{
			kernel.Contribute(c, chat.Targets, "flows."+f.ID, 0, chat.Target{
				Group: "Workflows", Title: f.Title, Summary: f.Summary,
				Take: func(d chat.Draft) { svc.Start(f, wf.Input{Brief: d.Text, Directory: d.Directory}) }}),
			kernel.Contribute(c, kernel.Commands, "flows."+f.ID, 0, kernel.Command{
				Title: f.Title + "…", Menu: "Agent", Run: func() { svc.Prompt(f) }}),
		} {
			if err != nil {
				return err
			}
		}
	}
	return nil
}
