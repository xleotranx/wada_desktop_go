// Package harness supervises cross-platform external coding CLI processes
// (ACP, Claude Code, Codex) and normalizes their streams onto protocol.Event.
package harness

import (
	"context"
	"io"
	"os/exec"
	"sync"
)

// ManagedProcess wraps a subprocess bound to an OS process tree (process group
// on POSIX, Job Object on Windows) so Kill cleans up child processes reliably.
type ManagedProcess struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	cmd      *exec.Cmd
	killTree func() error

	waitOnce sync.Once
	waitErr  error
	done     chan struct{}
}

// StartProcess starts bin with args in dir inside a managed process tree.
func StartProcess(ctx context.Context, dir, bin string, args, env []string) (*ManagedProcess, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	configureCmd(cmd, bin, args)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}

	killTree, err := attachProcessTree(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	return &ManagedProcess{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   stderr,
		cmd:      cmd,
		killTree: killTree,
		done:     make(chan struct{}),
	}, nil
}

// Done closes when Wait finishes or the process exits after Wait is called.
func (p *ManagedProcess) Done() <-chan struct{} { return p.done }

// Wait waits for the process to exit and closes Done.
func (p *ManagedProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.done)
	})
	return p.waitErr
}

// Kill forcefully terminates the entire managed process tree.
func (p *ManagedProcess) Kill() error {
	if p == nil || p.killTree == nil {
		return nil
	}
	return p.killTree()
}
