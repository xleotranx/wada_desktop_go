//go:build !linux

package main

import (
	"os/exec"
)

// ownChild has nothing to do where the OS has no parent-death signal:
// on macOS the server shares this app's process group, so terminal and
// dev-harness signals reach it too, and canceling the agent's context
// kills it on every graceful path.
func ownChild(cmd *exec.Cmd) {}
