package main

import (
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/design"
	"wada-desktop-go/internal/plugins/documents"
	"wada-desktop-go/internal/plugins/explore"
	"wada-desktop-go/internal/plugins/filechanges"
	"wada-desktop-go/internal/plugins/flows"
	"wada-desktop-go/internal/plugins/folderpicker"
	"wada-desktop-go/internal/plugins/jobs"
	"wada-desktop-go/internal/plugins/markup"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/modes"
	"wada-desktop-go/internal/plugins/notifications"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/pig"
	"wada-desktop-go/internal/plugins/skills"
	"wada-desktop-go/internal/plugins/tabs"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/video"
	"wada-desktop-go/internal/plugins/workflow"
	"wada-desktop-go/internal/plugins/workspace"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/plugins/inspector"
	"github.com/xleotranx/wada-kit/plugins/settings"
	"github.com/xleotranx/wada-kit/shell"
)

// wada names the app, where its data lives (the folder and the WADA_
// variables) and its window and menus. The Folder stays what it was before
// the app was named Wada Cowork, so the data it already has is kept.
var wada = shell.App{
	Name: "Wada Cowork", Folder: "wada-desktop-go", Env: "WADA",
	Width: 1400, Height: 900, MinWidth: 900, MinHeight: 600,
	Menus: []string{"File", "Chat", "Jobs", "View", "Agent", "Help"},
}

// main is the composition: the plugins this build installs, in order.
func main() {
	shell.Run(wada, append([]kernel.Factory{
		pig.New,
		chat.New,
		models.New,
		modes.New, // the mode, or a workflow for the next message
		usage.New,
		filechanges.New, // session or local Git changes, below Context in the work panel
		attachments.New,
		markup.New,       // annotate attached screenshots before sending
		mentions.New,     // files from the conversation folder
		skills.New,       // type / to attach a skill; needs a backend that has them
		workspace.New,    // folders and the conversations started here, in workspace.db
		folderpicker.New, // Open Folder… and the new chat's folder in the composer
		tabs.New,         // open chats as tabs across the window; replaces the sidebar
		explore.New,      // read-only side chats in a drawer, from selected text
		design.New,       // a canvas of the agent's web pages: comments, notes, tweaks, present
		video.New,        // HyperFrames videos: stage, timeline, checks, render
		documents.New,    // Word, PowerPoint, Excel, PDF and more as Markdown, in the app
		jobs.New,         // reads a folder, works on a goal, keeps the brief, in jobs.db
		workflow.New,     // owns conversational tasks and fresh reviewer children
		flows.New,        // plan, code, review, from the composer or command palette
		palette.New,
		notifications.New,
		inspector.New,
		settings.New,
	}, measured...)...)
}
