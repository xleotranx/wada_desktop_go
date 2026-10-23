package main

import (
	"wada-desktop-go/internal/plugins/activity"
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/flows"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/notifications"
	"wada-desktop-go/internal/plugins/opencode"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/pi"
	"wada-desktop-go/internal/plugins/tabs"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/workflow"
	"wada-desktop-go/internal/plugins/workspace"

	"github.com/xleotranx/wada-kit/plugins/inspector"
	"github.com/xleotranx/wada-kit/plugins/settings"
	"github.com/xleotranx/wada-kit/shell"
)

// wada names the app, where its data lives (the folder and the WADA_
// variables) and its window and menus.
var wada = shell.App{
	Name: "Wada", Folder: "wada-desktop-go", Env: "WADA",
	Width: 1400, Height: 900, MinWidth: 900, MinHeight: 600,
	Menus: []string{"File", "Chat", "View", "Agent", "Help"},
}

// main is the composition: the plugins this build installs, in order.
func main() {
	shell.Run(wada,
		opencode.New,
		pi.New, // off by default; turn OpenCode off to use it
		chat.New,
		models.New,
		usage.New,
		activity.New, // the files a conversation changed and its last command, in the work panel
		attachments.New,
		mentions.New,  // needs a backend that can read files; OpenCode can
		workspace.New, // folders and the conversations started here, in workspace.db
		tabs.New,      // open chats as tabs across the window; replaces the sidebar
		workflow.New,  // runs flows through hidden conversations
		flows.New,     // plan, code, review, from the command palette
		palette.New,
		notifications.New,
		inspector.New,
		settings.New,
	)
}
