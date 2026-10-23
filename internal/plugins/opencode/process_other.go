//go:build !linux && !windows

package opencode

import (
	"os/exec"
)

// startOwned starts cmd. There is nothing more to do where the OS has no
// parent-death signal: on macOS the server shares this app's process group,
// so terminal and dev-harness signals reach it too, and canceling the
// agent's context kills it on every graceful path.
func startOwned(cmd *exec.Cmd) error { return cmd.Start() }
