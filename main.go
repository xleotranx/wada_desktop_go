package main

import (
	"os"
	"wada-desktop-go/internal/desktop"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/features/coding"
	terminalfeature "wada-desktop-go/internal/features/terminal"
	"wada-desktop-go/internal/opencode"
)

func main() {
	modules := []feature.Factory{coding.New, terminalfeature.New}
	for _, arg := range os.Args[1:] {
		if arg == "--chat-only" {
			modules = nil
		}
	}
	desktop.Run(opencode.Backend{}, modules...)
}
