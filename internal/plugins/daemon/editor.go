package daemon

import (
	"context"

	"wada-desktop-go/internal/contract/agent"
)

func (p *Plugin) FindWorkspaceFiles(ctx context.Context, workspace agent.WorkspaceID, query string) ([]string, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	return cli.FindWorkspaceFiles(ctx, string(workspace), query)
}

func (p *Plugin) OpenWorkspaceFile(ctx context.Context, workspace agent.WorkspaceID, path string) (agent.WorkspaceFile, error) {
	cli, err := p.current()
	if err != nil {
		return agent.WorkspaceFile{}, err
	}
	file, err := cli.OpenWorkspaceFile(ctx, string(workspace), path)
	return agent.WorkspaceFile(file), err
}

func (p *Plugin) SaveWorkspaceFile(ctx context.Context, workspace agent.WorkspaceID, path, text, revision string) (agent.WorkspaceFile, error) {
	cli, err := p.current()
	if err != nil {
		return agent.WorkspaceFile{}, err
	}
	file, err := cli.SaveWorkspaceFile(ctx, string(workspace), path, text, revision)
	return agent.WorkspaceFile(file), err
}
