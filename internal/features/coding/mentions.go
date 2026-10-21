package coding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// mentionLimit bounds the @-file menu: enough to pick from, few enough
// that it stays a hint beside the words being written.
const mentionLimit = 6

// mentionState is the composer's @-file menu: the word it is open for,
// the files the search answered with, and the row the arrows are on.
type MentionState struct {
	Query    string
	Seen     string
	Dir      string
	Server   string
	Synced   bool
	Selected int
	Entries  []workspace.FileEntry
	Loading  bool
	Note     string
	// muted is set by Escape: the menu stays away until the @-word is
	// gone, so dismissing it does not make it flicker back on the next key.
	Muted   bool
	Request feature.Request
}

// mentionToken finds the @-word being typed at the end of the draft: the
// index of its @ and the text after it. An @ inside a word, as in an
// address, is not one.
func MentionToken(draft string) (start int, query string, ok bool) {
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
func (a *Module) MentionDir() string {
	if dir := a.host.Context().Directory; dir != "" {
		return dir
	}
	if a.host.Context().Local {
		return a.host.Context().Home
	}
	return ""
}

// mentionOpen reports the @-file menu answering the composer right now.
func (a *Module) MentionOpen() bool {
	m := &a.Mention
	if m.Muted || a.host.ComposerBlocked() || a.QuickOpen {
		return false
	}
	_, _, ok := MentionToken(a.host.Draft())
	return ok
}

// mentionChoices is what the menu lists: recently opened files before a
// letter is typed, the search's answers after.
func (a *Module) MentionChoices() []workspace.FileEntry {
	m := &a.Mention
	if len(m.Entries) <= mentionLimit {
		return m.Entries
	}
	return m.Entries[:mentionLimit]
}

// closeMention folds the menu away and cancels the search behind it.
func (a *Module) CloseMention() {
	m := &a.Mention
	m.Request.Stop()
	m.Query, m.Seen, m.Dir, m.Server, m.Selected = "", "", "", "", 0
	m.Synced = false
	m.Entries, m.Loading, m.Note = nil, false, ""
	m.Muted = false
}

// muteMention hides the menu for the @-word being typed; a new word, or
// deleting this one, brings it back.
func (a *Module) MuteMention() {
	a.CloseMention()
	a.Mention.Muted = true
}

// syncMention keeps the @-file menu in step with the draft: it opens for a
// trailing @-word, lists recent files before a letter, searches the chat's
// folder as the word grows, and closes when the word ends or is dismissed.
// The draft is the editor's only change signal, so this runs from the
// composer's build; the synced word makes it a no-op on later frames.
func (a *Module) SyncMention() {
	_, query, ok := MentionToken(a.host.Draft())
	if !ok || a.host.ComposerBlocked() {
		a.CloseMention()
		return
	}
	m := &a.Mention
	dir, server := a.MentionDir(), a.host.Context().Server
	if m.Muted || m.Synced && m.Seen == query && m.Dir == dir && m.Server == server {
		return
	}
	m.Synced, m.Seen, m.Query, m.Dir, m.Server, m.Selected = true, query, query, dir, server, 0
	m.Entries, m.Loading, m.Note = nil, false, ""
	m.Request.Stop()
	if dir == "" {
		m.Note = "Choose a project to mention its files."
		return
	}
	if query == "" {
		m.Entries = a.RecentFilesFor(dir)
		if len(m.Entries) == 0 {
			m.Note = "Type a file name."
		}
		return
	}
	if a.host.Background == nil {
		// The first frames may come before the window's dispatcher is
		// installed; the next frame tries again.
		m.Synced = false
		return
	}
	client := a.host.Context().Workspace
	m.Loading = true
	feature.Run(a.host, &m.Request, func(ctx context.Context) ([]workspace.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ag := client
		if ag == nil {
			return nil, fmt.Errorf("workspace files unavailable")
		}
		entries, err := ag.FindFiles(ctx, dir, query, 50)
		if err != nil {
			return nil, err
		}
		out := make([]workspace.FileEntry, 0, len(entries))
		for _, entry := range entries {
			if !entry.Directory {
				out = append(out, entry)
			}
		}
		return out, nil
	}, func(entries []workspace.FileEntry, err error) {
		if !m.Synced || m.Seen != query || m.Dir != dir || m.Server != server || m.Muted {
			return // another word moved on, or the menu was dismissed
		}
		m.Loading = false
		if err != nil {
			m.Note = "The file search did not run: " + err.Error()
			return
		}
		m.Entries = entries
		if len(entries) == 0 {
			m.Note = "No files match."
		}
	})
}

// insertMention writes the chosen file into the draft in place of the
// @-word, leaving the caret after a space for the words that follow.
func (a *Module) InsertMention(path string) {
	start, _, ok := MentionToken(a.host.Draft())
	if !ok {
		return
	}
	a.host.SetDraft(a.host.Draft()[:start] + "@" + path + " ")

	a.CloseMention()
}

// completeMention puts the menu's selected file into the draft, as Enter
// does while the menu is open. It reports whether the draft took one, so
// the half-typed word is not sent to the agent.
func (a *Module) CompleteMention() bool {
	if !a.MentionOpen() {
		return false
	}
	choices := a.MentionChoices()
	if len(choices) == 0 {
		return false
	}
	a.InsertMention(choices[min(max(a.Mention.Selected, 0), len(choices)-1)].Path)
	return true
}

// mentionMenu is the file list above the composer while an @-word is
// being typed. Its slot stays in the tree when empty, as the slash menu's
// does, so the composer keeps its focus identity across frames.
func (a *Module) MentionMenu(c *ui.Context, p native.Palette) {
	ui.Column(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().
		Children(func() {
			if !a.MentionOpen() {
				return
			}
			m := &a.Mention
			ui.Column(c).Padding(0, 0, 5, 0).Children(func() {
				ui.Column(c).FillWidth().Radius(10).Padding(4).Gap(1).
					Background(p.Bg).Border(1, p.Hairline).Children(func() {
					choices := a.MentionChoices()
					if len(choices) == 0 {
						ui.Row(c).FillWidth().Height(26).Padding(0, 8).AlignItems(ui.Center).Gap(7).Children(func() {
							if m.Loading {
								ui.Spinner(c).Size(11, 11).Label("Searching files")
							} else {
								ui.Icon(c, lucide.Search()).FontSize(12).TextColor(p.Faint)
							}
							ui.Text(c, m.MentionNote()).FontSize(11.5).TextColor(p.Muted).SingleLine()
						})
						return
					}
					m.Selected = min(max(m.Selected, 0), len(choices)-1)
					for i, entry := range choices {
						a.MentionRow(c, p, entry, i == m.Selected)
					}
				})
			})
		})
}

// mentionNote words what the empty menu is waiting for.
func (m *MentionState) MentionNote() string {
	if m.Loading {
		return "Searching files…"
	}
	if m.Note != "" {
		return m.Note
	}
	return "Type a file name."
}

func (a *Module) MentionRow(c *ui.Context, p native.Palette, entry workspace.FileEntry, selected bool) {
	c.Key("mention:" + entry.Path)
	b := ui.ButtonBase(c).Label("Mention "+entry.Path).Height(32).Radius(7).
		Padding(0, 8).FillWidth().Justify(ui.Start).AlignItems(ui.Center).Gap(8).
		Cursor(ui.CursorPointer).Tooltip(entry.Path)
	if selected {
		b.Background(p.Selected)
	}
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { a.InsertMention(entry.Path) })
	b.Children(func() {
		ui.Icon(c, lucide.FileText()).FontSize(13).TextColor(p.Muted).Shrink(0)
		ui.Text(c, filepath.Base(entry.Path)).FontSize(12.5).TextColor(p.Text).Shrink(0)
		if dir := filepath.Dir(entry.Path); dir != "." && dir != "" {
			ui.Text(c, dir).FontSize(11).TextColor(p.Faint).Grow(1).MinWidth(0).
				SingleLine().Ellipsis("…")
		} else {
			ui.Spacer(c)
		}
	})
}
