//go:build !windows

package harness

import (
	"os/exec"
	"sync"
	"syscall"
)

func configureCmd(cmd *exec.Cmd, _ string, _ []string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(cmd *exec.Cmd) (func() error, error) {
	if cmd.Process == nil {
		return func() error { return nil }, nil
	}
	pid := cmd.Process.Pid
	var once sync.Once
	var killErr error
	return func() error {
		once.Do(func() {
			if pid > 0 {
				killErr = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		return killErr
	}, nil
}
