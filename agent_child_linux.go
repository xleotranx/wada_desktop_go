//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// ownChild makes the child process die when this app dies.
func ownChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
