// Command wadad runs Wada's headless coding-agent daemon.
package main

import (
	"log"
	"os"

	"wada-desktop-go/internal/daemon/runner"
)

func main() {
	if err := runner.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
