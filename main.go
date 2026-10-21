package main

import (
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/inspector"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/notifications"
	"wada-desktop-go/internal/plugins/opencode"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/pi"
	"wada-desktop-go/internal/plugins/sessions"
	"wada-desktop-go/internal/plugins/settings"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/workspace"
	"wada-desktop-go/internal/shell"
)

// main is the composition: the plugins this build installs, in order.
func main() {
	shell.Run(
		&opencode.Plugin{},
		&pi.Plugin{}, // off by default; turn OpenCode off to use it
		&chat.Plugin{},
		&models.Plugin{},
		&usage.Plugin{},
		&attachments.Plugin{},
		&mentions.Plugin{},  // needs a backend that can read files; OpenCode can
		&workspace.Plugin{}, // folders and the conversations started here, in workspace.db
		&sessions.Plugin{},
		&palette.Plugin{},
		&notifications.Plugin{},
		&inspector.Plugin{},
		&settings.Plugin{},
	)
}
