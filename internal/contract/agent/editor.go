package agent

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

var EditorKey = kernel.NewKey[WorkspaceEditor]("agent.editor")

// WorkspaceEditor opens and saves regular text files in an active workspace.
type WorkspaceEditor interface {
	FindWorkspaceFiles(ctx context.Context, workspace WorkspaceID, query string) ([]string, error)
	OpenWorkspaceFile(ctx context.Context, workspace WorkspaceID, path string) (WorkspaceFile, error)
	SaveWorkspaceFile(ctx context.Context, workspace WorkspaceID, path, text, revision string) (WorkspaceFile, error)
}

type WorkspaceFile struct {
	Text     string
	Revision string
}
