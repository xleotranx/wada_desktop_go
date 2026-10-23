//go:build linux

package opencode

import (
	"os/exec"
	"syscall"
)

// startOwned starts cmd so that it dies when this app dies.
func startOwned(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd.Start()
}
