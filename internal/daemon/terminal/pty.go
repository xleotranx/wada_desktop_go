// Package terminal implements Wada's detached cross-platform PTY terminal
// worker, headless VT buffer, authenticated local IPC, and daemon supervisor.
package terminal

import "io"

// StartOptions configures a spawned interactive PTY shell.
type StartOptions struct {
	Directory string
	Shell     string
	Cols      int
	Rows      int
	Env       []string // the whole environment; empty for the daemon's
	// Command, when set, is run by the shell instead of an interactive
	// session.
	Command string
}

// PTY abstracts an OS pseudo-terminal across POSIX PTY and Windows ConPTY.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	PID() int
	Wait() (int, error)
}

func normalizeSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if cols > 1000 {
		cols = 1000
	}
	if rows > 500 {
		rows = 500
	}
	return cols, rows
}
