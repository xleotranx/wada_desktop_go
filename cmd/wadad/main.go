// Command wadad runs Wada's headless coding-agent daemon.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"wada-desktop-go/internal/client"

	"wada-desktop-go/internal/daemon/runner"
	"wada-desktop-go/internal/daemon/terminal"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--terminal-worker" {
		if err := terminal.RunWorkerCLI(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--stdio-bridge" {
		if err := stdioBridge(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runner.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// stdioBridge carries a client's connection over stdio to this machine's
// wadad, starting it when none runs: what `ssh host wadad --stdio-bridge`
// gives a client that dials ssh://host.
func stdioBridge(args []string) error {
	fs := flag.NewFlagSet("wadad --stdio-bridge", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "Wada data directory (default WADA_DATA_DIR or OS config dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := *dataDir
	if dir == "" {
		var err error
		if dir, err = client.DefaultDataDir(); err != nil {
			return err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return client.ServeStdioBridge(ctx, dir, func() error {
		return client.StartDetached(dir, exe, "--data-dir", dir, "--listen", "auto")
	}, os.Stdin, os.Stdout)
}
