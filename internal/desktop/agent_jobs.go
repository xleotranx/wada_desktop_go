package desktop

import (
	"context"
	"wada-desktop-go/internal/agent"

	"wada-desktop-go/internal/runner"
)

// queueCommand makes admission visible before the UI advances its state.
func (a *app) queueCommand(command runner.Command) runner.Admission {
	result := runner.Closed
	if !a.runtime.closed && a.runtime.submitCommand != nil {
		result = a.runtime.submitCommand(runner.Generation(a.connections.route), command)
	}
	switch result {
	case runner.Accepted:
		a.conversation.commandError = ""
	case runner.Busy:
		a.conversation.commandError = "The connection is busy. Try again."
	case runner.Closed:
		a.conversation.commandError = "The connection is closed."
	case runner.Invalid:
		a.conversation.commandError = "That action has no valid target."
	}
	return result
}
func (a *app) runnerConnection() runner.Connection {
	cfg := a.activeServerConn()
	directory := a.preferences.home
	backend := a.backend
	return runner.Connection{Projects: a.hasWorkspaceFeatures(), ID: cfg.id, Name: cfg.name, Address: cfg.url, Directory: directory, Pins: a.scope().pinOrder, Open: func(ctx context.Context) (agent.Client, error) {
		return backend.Start(ctx, cfg.transport(), directory)
	}}
}
