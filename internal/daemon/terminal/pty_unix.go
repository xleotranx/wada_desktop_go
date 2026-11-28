//go:build !windows

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type unixPTY struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	pid      int
	waitOnce sync.Once
	exitCode int
	waitErr  error
	closed   sync.Once
}

func defaultShell(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		return sh
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// commandArgs runs a command with the shell, as a login shell where the
// shell takes -l, so the user's PATH from their profile applies.
func commandArgs(sh, command string) []string {
	switch filepath.Base(sh) {
	case "bash", "zsh", "fish", "ksh":
		return []string{"-l", "-c", command}
	}
	return []string{"-c", command}
}

func startPTY(opts StartOptions) (PTY, error) {
	cols, rows := normalizeSize(opts.Cols, opts.Rows)
	sh := defaultShell(opts.Shell)
	cmd := exec.Command(sh)
	if opts.Command != "" {
		cmd = exec.Command(sh, commandArgs(sh, opts.Command)...)
	}
	if opts.Directory != "" {
		cmd.Dir = opts.Directory
	}
	env := os.Environ()
	if len(opts.Env) > 0 {
		env = opts.Env
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, err
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	return &unixPTY{
		cmd:  cmd,
		ptmx: ptmx,
		pid:  pid,
	}, nil
}

func (p *unixPTY) Read(b []byte) (int, error) {
	return p.ptmx.Read(b)
}

func (p *unixPTY) Write(b []byte) (int, error) {
	return p.ptmx.Write(b)
}

func (p *unixPTY) Resize(cols, rows int) error {
	c, r := normalizeSize(cols, rows)
	return pty.Setsize(p.ptmx, &pty.Winsize{
		Cols: uint16(c),
		Rows: uint16(r),
	})
}

func (p *unixPTY) PID() int { return p.pid }

func (p *unixPTY) Wait() (int, error) {
	p.waitOnce.Do(func() {
		err := p.cmd.Wait()
		p.waitErr = err
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				p.exitCode = exitErr.ExitCode()
			} else {
				p.exitCode = 1
			}
		}
	})
	return p.exitCode, p.waitErr
}

func (p *unixPTY) Close() error {
	var err error
	p.closed.Do(func() {
		err = p.ptmx.Close()
		if p.pid > 0 {
			_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		}
		_, _ = p.Wait()
	})
	return err
}
