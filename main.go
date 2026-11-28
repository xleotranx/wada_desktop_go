package main

import (
	"log"
	"os"

	"wada-desktop-go/internal/daemon/runner"
	"wada-desktop-go/internal/daemon/terminal"
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/daemon"
	"wada-desktop-go/internal/plugins/explore"
	"wada-desktop-go/internal/plugins/filechanges"
	"wada-desktop-go/internal/plugins/folderpicker"
	"wada-desktop-go/internal/plugins/markup"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/modes"
	"wada-desktop-go/internal/plugins/notifications"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/skills"
	"wada-desktop-go/internal/plugins/starters"
	"wada-desktop-go/internal/plugins/tabs"
	terminalplugin "wada-desktop-go/internal/plugins/terminal"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/workers"
	"wada-desktop-go/internal/plugins/workspace"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/plugins/inspector"
	"github.com/xleotranx/wada-kit/plugins/settings"
	"github.com/xleotranx/wada-kit/shell"
)

// wada names the app, its data directory and env prefix (WADA_*), and its window and menus.
var wada = shell.App{
	Name: "Wada", Folder: "wada-desktop-go", Env: "WADA",
	Width: 1400, Height: 900, MinWidth: 900, MinHeight: 600,
	Menus: []string{"File", "Chat", "View", "Agent", "Help"},
}

// main launches the headless daemon subprocess (--daemon), detached PTY
// terminal worker (--terminal-worker), or desktop shell composed of plugins in order.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--daemon":
			if err := runner.Run(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "--terminal-worker":
			if err := terminal.RunWorkerCLI(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		}
	}

	shell.Run(wada, append([]kernel.Factory{
		daemon.New,
		chat.New,
		models.New,
		modes.New, // the mode for outgoing messages
		usage.New,
		filechanges.New, // session or local Git changes, below Context in the work panel
		workers.New,     // harness picker, supervised CLI workers and Git worktrees
		terminalplugin.New,
		attachments.New,
		markup.New,       // annotate attached screenshots before sending
		mentions.New,     // files from the conversation folder
		skills.New,       // type / to attach a skill; needs a backend that has them
		workspace.New,    // folders and the conversations started here, in workspace.db
		folderpicker.New, // Open Folder… and the new chat's folder in the composer
		tabs.New,         // open chats and pages as tabs across the title strip
		explore.New,      // read-only side chats in a drawer, from selected text
		starters.New,     // the new chat's folder starters
		palette.New,
		notifications.New,
		inspector.New,
		settings.New,
	}, measured...)...)
}
