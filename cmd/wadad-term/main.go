// Command wadad-term runs Wada's detached interactive PTY terminal worker.
package main

import (
	"log"
	"os"

	"wada-desktop-go/internal/daemon/terminal"
)

func main() {
	if err := terminal.RunWorkerCLI(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
