// Command wadad runs Wada's headless coding-agent daemon.
package main

import (
	"log"
	"os"

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
	if err := runner.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
