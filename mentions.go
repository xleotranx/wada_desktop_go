package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// mentionLimit bounds the @-file menu: enough to pick from, few enough
// that it stays a hint beside the words being written.
const mentionLimit = 6

// mentionState is the composer's @-file menu: the word it is open for,
// the files the search answered with, and the row the arrows are on.
type mentionState struct {
	query    string
	seen     string
	dir      string
	server   string
	synced   bool
	selected int
	entries  []opencode.FileEntry
	loading  bool
	note     string
	// muted is set by Escape: the menu stays away until the @-word is
	// gone, so dismissing it does not make it flicker back on the next key.
	muted   bool
	request requestState
}

// mentionToken finds the @-word being typed at the end of the draft: the
// index of its @ and the text after it. An @ inside a word, as in an
// address, is not one.
func mentionToken(draft string) (start int, query string, ok bool) {
	i := strings.LastIndexByte(draft, '@')
	if i < 0 {
		return 0, "", false
	}
	if i > 0 {
		switch draft[i-1] {
		case ' ', '\n', '\t':
		default:
			return 0, "", false
		}
	}
	rest := draft[i+1:]
	if strings.ContainsAny(rest, " \t\n") {
		return 0, "", false
	}
	return i, rest, true
}

// mentionDir is the folder an @-word refers to: the chat's own folder, or
// this machine's home for a local chat that has none yet. A server's chat
// without a folder has nothing to offer.
func (a *app) mentionDir() string {
	if dir := a.chatDir(); dir != "" {
		return dir
	}
	if a.localActive() {
		return a.preferences.home
	}
	return ""
}

// mentionOpen reports the @-file menu answering the composer right now.
func (a *app) mentionOpen() bool {
	m := &a.layout.mention
	if m.muted || a.layout.slashHelp || a.layout.quickOpen || len(a.slashMatches()) > 0 {
		return false
	}
	_, _, ok := mentionToken(a.conversation.draft)
	return ok
}

// mentionChoices is what the menu lists: recently opened files before a
// letter is typed, the search's answers after.
func (a *app) mentionChoices() []opencode.FileEntry {
	m := &a.layout.mention
	if len(m.entries) <= mentionLimit {
		return m.entries
	}
	return m.entries[:mentionLimit]
}

// closeMention folds the menu away and cancels the search behind it.
func (a *app) closeMention() {
	m := &a.layout.mention
	m.request.stop()
	m.query, m.seen, m.dir, m.server, m.selected = "", "", "", "", 0
	m.synced = false
	m.entries, m.loading, m.note = nil, false, ""
	m.muted = false
}

// muteMention hides the menu for the @-word being typed; a new word, or
// deleting this one, brings it back.
func (a *app) muteMention() {
	a.closeMention()
	a.layout.mention.muted = true
}

// syncMention keeps the @-file menu in step with the draft: it opens for a
// trailing @-word, lists recent files before a letter, searches the chat's
// folder as the word grows, and closes when the word ends or is dismissed.
// The draft is the editor's only change signal, so this runs from the
// composer's build; the synced word makes it a no-op on later frames.
func (a *app) syncMention() {
	_, query, ok := mentionToken(a.conversation.draft)
	if !ok || len(a.slashMatches()) > 0 {
		a.closeMention()
		return
	}
	m := &a.layout.mention
	dir, server := a.mentionDir(), a.connections.activeServer
	if m.muted || m.synced && m.seen == query && m.dir == dir && m.server == server {
		return
	}
	m.synced, m.seen, m.query, m.dir, m.server, m.selected = true, query, query, dir, server, 0
	m.entries, m.loading, m.note = nil, false, ""
	m.request.stop()
	if dir == "" {
		m.note = "Choose a project to mention its files."
		return
	}
	if query == "" {
		m.entries = a.recentFilesFor(dir)
		if len(m.entries) == 0 {
			m.note = "Type a file name."
		}
		return
	}
	if a.runtime.background == nil {
		// The first frames may come before the window's dispatcher is
		// installed; the next frame tries again.
		m.synced = false
		return
	}
	cfg := a.activeServerConn()
	m.loading = true
	runRequest(a, &m.request, func(ctx context.Context) ([]opencode.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		entries, err := ag.FindFiles(ctx, dir, query, filesFound)
		if err != nil {
			return nil, err
		}
		out := make([]opencode.FileEntry, 0, len(entries))
		for _, entry := range entries {
			if !entry.Directory {
				out = append(out, entry)
			}
		}
		return out, nil
	}, func(entries []opencode.FileEntry, err error) {
		if !m.synced || m.seen != query || m.dir != dir || m.server != server || m.muted {
			return // another word moved on, or the menu was dismissed
		}
		m.loading = false
		if err != nil {
			m.note = "The file search did not run: " + err.Error()
			return
		}
		m.entries = entries
		if len(entries) == 0 {
			m.note = "No files match."
		}
	})
}

// insertMention writes the chosen file into the draft in place of the
// @-word, leaving the caret after a space for the words that follow.
func (a *app) insertMention(path string) {
	start, _, ok := mentionToken(a.conversation.draft)
	if !ok {
		return
	}
	a.conversation.draft = a.conversation.draft[:start] + "@" + path + " "
	a.layout.composerFocus = true
	a.closeMention()
}

// completeMention puts the menu's selected file into the draft, as Enter
// does while the menu is open. It reports whether the draft took one, so
// the half-typed word is not sent to the agent.
func (a *app) completeMention() bool {
	if !a.mentionOpen() {
		return false
	}
	choices := a.mentionChoices()
	if len(choices) == 0 {
		return false
	}
	a.insertMention(choices[min(max(a.layout.mention.selected, 0), len(choices)-1)].Path)
	return true
}

// mentionMenu is the file list above the composer while an @-word is
// being typed. Its slot stays in the tree when empty, as the slash menu's
// does, so the composer keeps its focus identity across frames.
func (a *app) mentionMenu(c *ui.Context, p palette) {
	ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().
		Children(func() {
			if !a.mentionOpen() {
				return
			}
			m := &a.layout.mention
			ui.Column(c).Padding(0, 0, 5, 0).Children(func() {
				ui.Column(c).FillWidth().Radius(10).Padding(4).Gap(1).
					Background(p.bg).Border(1, p.hairline).Children(func() {
					choices := a.mentionChoices()
					if len(choices) == 0 {
						ui.Row(c).FillWidth().Height(26).Padding(0, 8).AlignItems(ui.Center).Gap(7).Children(func() {
							if m.loading {
								ui.Spinner(c).Size(11, 11).Label("Searching files")
							} else {
								ui.Icon(c, icSearch).FontSize(12).TextColor(p.faint)
							}
							ui.Text(c, m.mentionNote()).FontSize(11.5).TextColor(p.muted).SingleLine()
						})
						return
					}
					m.selected = min(max(m.selected, 0), len(choices)-1)
					for i, entry := range choices {
						a.mentionRow(c, p, entry, i == m.selected)
					}
				})
			})
		})
}

// mentionNote words what the empty menu is waiting for.
func (m *mentionState) mentionNote() string {
	if m.loading {
		return "Searching files…"
	}
	if m.note != "" {
		return m.note
	}
	return "Type a file name."
}

func (a *app) mentionRow(c *ui.Context, p palette, entry opencode.FileEntry, selected bool) {
	c.Key("mention:" + entry.Path)
	b := ui.ButtonBase(c).Label("Mention "+entry.Path).Height(32).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer).Tooltip(entry.Path)
	if selected {
		b.Background(p.selected)
	}
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(func() { a.insertMention(entry.Path) })
	b.Children(func() {
		ui.Icon(c, icFileDoc).FontSize(13).TextColor(p.muted).Shrink(0)
		ui.Text(c, filepath.Base(entry.Path)).FontSize(12.5).TextColor(p.text).Shrink(0)
		if dir := filepath.Dir(entry.Path); dir != "." && dir != "" {
			ui.Text(c, dir).FontSize(11).TextColor(p.faint).Grow(1).MinWidth(0).
				SingleLine().Ellipsis("…")
		} else {
			ui.Spacer(c)
		}
	})
}
