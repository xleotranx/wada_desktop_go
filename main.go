package main

import (
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/opencode"
	"wada-desktop-go/internal/plugins/sessions"
	"wada-desktop-go/internal/plugins/settings"
	"wada-desktop-go/internal/shell"
)

// main is the composition: the plugins this build installs, in order.
func main() {
	shell.Run(
		&opencode.Plugin{},
		&chat.Plugin{},
		&models.Plugin{},
		&sessions.Plugin{},
		&settings.Plugin{},
	)
}
