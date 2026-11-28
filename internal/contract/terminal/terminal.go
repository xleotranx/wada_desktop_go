// Package terminal declares the shared contract between backend plugins
// (such as daemon) and UI plugins providing interactive PTY terminals.
package terminal

import (
	"context"
	"time"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	// ServiceKey provides PTY terminal management operations.
	ServiceKey = kernel.NewKey[Service]("terminal.service")

	// Updated is emitted on the UI thread when a terminal is created, exited, or killed.
	Updated = kernel.NewTopic[Info]("terminal.updated")
)

// PaneKind is the tabs.Panes kind that shows a terminal; a tab's Ref is the
// terminal's ID.
const PaneKind = "terminal.shell"

// Info describes a detached daemon-managed PTY terminal session.
type Info struct {
	ID        string
	Name      string
	Directory string
	Shell     string
	Cols      int
	Rows      int
	PID       int
	Running   bool
	ExitCode  int
	CreatedAt time.Time
}

// CreateRequest opens a new detached PTY terminal.
type CreateRequest struct {
	Name      string
	Directory string
	Shell     string
	Cols      int
	Rows      int
}

// AttachRequest attaches to a terminal and optionally resizes its PTY.
type AttachRequest struct {
	ID   string
	Cols int
	Rows int
}

// Stream represents an active bidirectional PTY attachment.
type Stream interface {
	Info() Info
	Snapshot() []byte
	Output() <-chan []byte
	WriteInput(data []byte) error
	Resize(cols, rows int) error
	Close() error
}

// Service manages detached PTY terminals owned by the backend.
type Service interface {
	List(ctx context.Context) ([]Info, error)
	Create(ctx context.Context, req CreateRequest) (Info, error)
	Attach(ctx context.Context, req AttachRequest) (Stream, error)
	Kill(ctx context.Context, id string) error
}
