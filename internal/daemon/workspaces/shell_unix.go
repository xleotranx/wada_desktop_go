//go:build !windows

package workspaces

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// shellCommand runs a command with the user's shell, as a login shell where
// it takes -l, so their PATH applies.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	switch filepath.Base(sh) {
	case "bash", "zsh", "fish", "ksh":
		return exec.CommandContext(ctx, sh, "-l", "-c", command)
	}
	return exec.CommandContext(ctx, sh, "-c", command)
}
