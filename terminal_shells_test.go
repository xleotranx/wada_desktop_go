package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// TestTerminalShellMenu starts a shell from the menu and leaves the plain
// new-tab on the login shell.
func TestTerminalShellMenu(t *testing.T) {
	a := newTestApp(t)
	a.terminal.shells = []string{"/bin/zsh", "/bin/bash"}
	var commands [][]string
	a.runtime.newTerminal = func(o terminal.Options) (*terminal.Terminal, error) {
		commands = append(commands, slices.Clone(o.Command))
		return nil, errors.New("terminal unavailable in tests")
	}
	a.terminal.open = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("New terminal with a shell"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("New terminal: bash"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.terminal.tabs) != 1 || a.terminal.tabs[0].shell != "/bin/bash" {
		t.Fatalf("the menu started %+v", a.terminal.tabs)
	}
	if len(commands) == 0 || !slices.Equal(commands[len(commands)-1], []string{"/bin/bash"}) {
		t.Fatalf("shell commands %q", commands)
	}

	a.newTerminalTab()
	if len(a.terminal.tabs) != 2 || a.terminal.tabs[1].shell != "" {
		t.Fatalf("the plain tab reads %+v", a.terminal.tabs)
	}
	if got := commands[len(commands)-1]; got != nil {
		t.Fatalf("the plain tab ran %q", got)
	}
}

// TestLocalShellsPutsEnvFirst keeps the user's shell ahead of the system
// list and leaves missing programs out.
func TestLocalShellsPutsEnvFirst(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "myshell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	shells := localShells()
	if len(shells) == 0 || shells[0] != shell {
		t.Fatalf("localShells starts %v", shells)
	}
	seen := 0
	for _, got := range shells {
		if got == shell {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the shell stands %d times: %v", seen, shells)
	}
	missing := filepath.Join(dir, "missing")
	t.Setenv("SHELL", missing)
	for _, got := range localShells() {
		if got == missing {
			t.Fatalf("a missing shell was offered: %v", shells)
		}
	}
}
