package workspaces

import (
	"context"
	"os"
	"os/exec"
)

// shellCommand runs a command with cmd.exe.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	sh := os.Getenv("COMSPEC")
	if sh == "" {
		sh = "cmd.exe"
	}
	return exec.CommandContext(ctx, sh, "/C", command)
}
