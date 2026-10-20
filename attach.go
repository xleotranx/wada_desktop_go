package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// attachBatchLimit bounds one drop: the draft is a message, not a folder
// view, and every file is quoted in full. The byte cap keeps a handful of
// large files from turning the composer into a megabyte of text.
const (
	attachBatchLimit = 8
	attachBatchBytes = 512 * 1024
)

// quotedFile is one file read off the UI thread, with its own error so one
// unreadable file does not lose its neighbours from the same drop.
type quotedFile struct {
	path string
	data []byte
	err  error
}

// pickAttachDialog asks the system for files to quote. Remote chats cannot
// read this machine's disk, so the button that calls it is local-only.
func (a *app) pickAttachDialog(done func(paths []string)) {
	win := a.runtime.win
	inBackground(a, func(context.Context) ([]string, error) {
		return mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       "Quote files",
			Message:     "Their contents join the message in code fences.",
			ButtonLabel: "Quote",
			Multiple:    true,
		})
	}, func(paths []string, err error) {
		if err != nil {
			a.streamLine("The file chooser did not open: " + err.Error())
			return
		}
		done(paths)
	})
}

// quoteFiles reads dropped or chosen files and appends each as a fenced
// block, after the repeated files the draft already holds. Reading happens
// off the UI thread; the append rechecks the conversation it started in.
func (a *app) quoteFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	if !a.localActive() {
		a.notify(ui.Toast{Title: "Files cannot be quoted into a remote chat",
			Description: "Open a local chat, or quote a path on the server by typing it.",
			Type:        components.ToastError})
		return
	}
	unique := make([]string, 0, min(len(paths), attachBatchLimit))
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if len(unique) < attachBatchLimit {
			unique = append(unique, path)
		}
	}
	if len(unique) == 0 {
		return
	}
	overLimit := len(seen) > attachBatchLimit
	revision, dir := a.conversation.target.revision, a.chatDir()
	runRequest(a, &a.layout.quoteRequest, func(context.Context) ([]quotedFile, error) {
		quotes := make([]quotedFile, 0, len(unique))
		total := 0
		for _, path := range unique {
			abs, data, err := expandFile(path)
			if err == nil && total+len(data) > attachBatchBytes {
				err = errors.New("the batch is full")
			}
			if err == nil && !textBytes(data) {
				err = errors.New("that file is not text")
			}
			if err == nil {
				total += len(data)
			}
			quotes = append(quotes, quotedFile{path: abs, data: data, err: err})
		}
		return quotes, nil
	}, func(quotes []quotedFile, err error) {
		if a.runtime.closed || a.conversation.target.revision != revision || a.chatDir() != dir {
			return
		}
		if err != nil {
			a.notify(ui.Toast{Title: "The files were not read", Description: err.Error(),
				Type: components.ToastError})
			return
		}
		added, repeated := 0, 0
		var failed []string
		for _, quote := range quotes {
			if quote.err != nil {
				failed = append(failed, filepath.Base(quote.path)+": "+quote.err.Error())
				continue
			}
			mark := "(file: " + quote.path + ")"
			if strings.Contains(a.conversation.draft, mark) {
				repeated++
				continue
			}
			a.conversation.draft += quoteMarkup(quote.path, quote.data)
			added++
		}
		if added > 0 {
			a.layout.composerFocus = true
			title := fmt.Sprintf("Quoted %d files", added)
			if added == 1 {
				title = "Quoted one file"
			}
			if overLimit {
				title += fmt.Sprintf(" (first %d)", attachBatchLimit)
			}
			a.notify(ui.Toast{Title: title, Type: components.ToastSuccess})
		}
		if len(failed) > 0 {
			description := failed[0]
			if len(failed) > 1 {
				description = fmt.Sprintf("%s and %d more", description, len(failed)-1)
			}
			a.notify(ui.Toast{Title: "Some files were not quoted",
				Description: description, Type: components.ToastError})
		} else if added == 0 && repeated > 0 {
			a.notify(ui.Toast{Title: "Already in the draft"})
		}
	})
}

// textBytes reports a file the draft can quote as text. Images and other
// binary files need a protocol part this chat does not send yet.
func textBytes(data []byte) bool {
	return bytes.IndexByte(data, 0) < 0 && utf8.Valid(data)
}

// quoteMarkup is how one file joins the draft: a path the agent can read
// and the file in a fence tagged with its language.
func quoteMarkup(abs string, data []byte) string {
	return "\n\n" + "(file: " + abs + ")\n```" + codeLang(abs) + "\n" + string(data) + "\n```\n"
}

// chatDropZone lets the transcript take files dropped anywhere over it,
// dimming the page while they hover and quoting what lands. The check runs
// on an input event, not as an unconditional render action.
func (a *app) chatDropZone(c *ui.Context, p palette, area ui.Element) {
	if area.FileDragOver() {
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).
			Background(p.bg.Alpha(.86)).Center().Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(9).Padding(11, 15).Radius(12).
				Background(p.detailBg).Border(1, p.orange).Children(func() {
				ui.Icon(c, icFileDoc).FontSize(16).TextColor(p.orange)
				ui.Text(c, "Drop files to quote them").FontSize(13).TextColor(p.text)
			})
		})
	}
	if files := area.DroppedFiles(); len(files) > 0 {
		a.quoteFiles(files)
	}
}
