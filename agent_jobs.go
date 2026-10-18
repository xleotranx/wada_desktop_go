package main

import "wada-desktop-go/internal/runner"

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
	return runner.Connection{ID: cfg.id, Name: cfg.name, Directory: a.preferences.home, Config: cfg.transport(), Pins: a.scope().pinOrder}
}
