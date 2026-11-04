// Package cowork contributes Wada's everyday-work behavior and folder tools.
package cowork

import (
	"context"
	"encoding/json"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/documents"
)

type Plugin struct{}

func New() kernel.Plugin { return &Plugin{} }
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "cowork", Title: "Cowork tools", Description: "Find and read work documents, revise files and create everyday deliverables in the selected folder.", Requires: []kernel.Named{shell.HostKey}}
}
func (*Plugin) Setup(c *kernel.Context) error {
	protected := kernel.Require(c, shell.HostKey).DataDir()
	reader := func(ctx context.Context) (documents.Reader, error) {
		result := make(chan documents.Reader, 1)
		c.Post(func() { r, _ := kernel.Use(c, documents.ReaderKey); result <- r })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-result:
			return r, nil
		}
	}
	return kernel.Contribute(c, agent.ProjectTools, "cowork.workspace", 0, agent.ProjectTool{Kind: "cowork", Instructions: instructions, Execute: func(ctx context.Context, dir, action string, input json.RawMessage) (string, error) {
		return workspaceOperation(ctx, dir, protected, reader, action, input)
	}})
}
