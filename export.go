package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// transcriptMarkdown turns the conversation as it stands into markdown:
// what was asked, the answers that landed, what the agent ran and the
// engine notes still explaining themselves.
func transcriptMarkdown(title string, blocks []chat.Block, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "_Exported from Wada on %s._\n\n---\n\n", now.Format(time.RFC1123))
	for _, block := range blocks {
		switch block.Kind {
		case chat.KindUser:
			fmt.Fprintf(&b, "## You\n\n%s\n\n", block.Text)
		case chat.KindText, chat.KindStream:
			fmt.Fprintf(&b, "## Codex\n\n%s\n\n", block.Text)
			if line := usageLine(block.Cost, block.Tokens); line != "" {
				fmt.Fprintf(&b, "_%s_\n\n", line)
			}
		case chat.KindActivity:
			fmt.Fprintf(&b, "*Used %s*\n\n", block.Text)
			if len(block.Detail) > 0 {
				fmt.Fprintf(&b, "```sh\n%s\n```\n\n", strings.Join(block.Detail, "\n"))
			}
		case chat.KindNote:
			fmt.Fprintf(&b, "> %s\n\n", block.Text)
		case chat.KindWorking, chat.KindPermission:
			// The loader and a still-waiting ask are transient, not
			// transcript.
		}
	}
	return b.String()
}

// exportTranscript asks the system for a destination and writes the open
// conversation there in markdown. A draft that never sent asks for
// nothing: its export would be blank.
func (a *app) exportTranscript() {
	sid := string(a.conversation.target.session)
	title := a.conversation.selected
	if sid == "" || title == "" {
		return
	}
	blocks := a.conversation.transcript.Snapshot()
	a.runtime.pickExport(func(path string) {
		if path == "" {
			return
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			path += ".md"
		}
		if werr := os.WriteFile(path, []byte(transcriptMarkdown(title, blocks, time.Now())), 0o644); werr != nil {
			a.notify(ui.Toast{Title: "The transcript did not export", Description: werr.Error(),
				Type: components.ToastError})
			return
		}
		a.notify(ui.Toast{Title: "Transcript exported", Description: path,
			Type: components.ToastSuccess})
	})
}

// pickSaveDialog is the real export chooser: the system's save panel,
// opened in the background, its answer landing on the window.
func (a *app) pickSaveDialog(done func(path string)) {
	win := a.runtime.win
	inBackground(a, func(context.Context) (string, error) {
		return mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      win,
			Title:       "Export transcript",
			Message:     "The conversation lands there in markdown.",
			DefaultPath: exportName(a.conversation.selected) + ".md",
			ButtonLabel: "Export",
		})
	}, func(path string, err error) {
		if err != nil {
			a.streamLine("The export chooser did not open: " + err.Error())
			return
		}
		done(path)
	})
}

// exportName keeps a title legible as a filename: folder separators and
// control marks turn into dashes, margins fall away, the whole thing
// stays under sixty runes, and an empty result stands as "conversation".
func exportName(title string) string {
	var b strings.Builder
	for _, r := range title {
		if r == '/' || r == '\\' || r == ':' || unicode.IsControl(r) {
			r = '-'
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(strings.Trim(strings.TrimSpace(b.String()), "."))
	runes := []rune(name)
	if len(runes) > 60 {
		runes = runes[:60]
	}
	name = strings.TrimSpace(string(runes))
	if name == "" {
		return "conversation"
	}
	return name
}
