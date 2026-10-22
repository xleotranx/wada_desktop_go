// Package flows is the app's own set of workflows, written in Go against the
// workflow contract and started from the command palette. The first is plan,
// code, review.
package flows

import (
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/kernel"
)

// Plugin brings the app's flows. The zero value is ready to install.
type Plugin struct{}

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "flows", Title: "Flows",
		Description: "Ready-made workflows for the command palette: plan, code, review.",
		Requires:    []kernel.Named{wf.ServiceKey},
	}
}

func (*Plugin) Setup(c *kernel.Context) error {
	svc := kernel.Require(c, wf.ServiceKey)
	for _, f := range []wf.Flow{planCodeReview} {
		err := kernel.Contribute(c, kernel.Commands, "flows."+f.ID, 0, kernel.Command{
			Title: f.Title + "…", Menu: "Agent", Run: func() { svc.Prompt(f) }})
		if err != nil {
			return err
		}
	}
	return nil
}
