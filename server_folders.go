package main

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// browseState is the server-folder dialog: the folder its listing hangs
// off, where inside it the user is looking, what the server listed there
// and what the browse was opened for.
type browseState struct {
	request requestState

	open bool
	// location is the one folder the browse names to the server; rel walks
	// from it, so looking around files no project row the folder did not
	// already have.
	location string
	rel      string
	path     string // the path field, absolute in the server's spelling
	entries  []opencode.FileEntry
	loading  bool
	note     string
	draft    bool // the browse settles a draft's project, rather than starting a chat
}

// dir is the folder on show, in the server's own spelling.
func (st *browseState) dir() string {
	return path.Join(st.location, st.rel)
}

// browseServerFolders walks the server's own folders: the system's panel
// is this machine's to hand over, so the local server keeps it. A browse
// opened for the draft settles the draft's project; one opened from the
// sidebar starts a chat in the folder it lands on.
func (a *app) browseServerFolders(forDraft bool) {
	if a.localActive() {
		a.layout.selectAddedProject = forDraft
		a.openProjectDialog()
		return
	}
	a.closeBrowse()
	st := &browseState{open: true, draft: forDraft, loading: true}
	a.layout.browse = st
	// The chat's own folder is where the user already is; with none, the
	// browse opens on the folder the server works in by default.
	if start := a.chatDir(); start != "" {
		st.location, st.rel, st.path = start, ".", start
		a.browseList()
		return
	}
	cfg := a.activeServerConn()
	runRequest(a, &st.request, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return "", err
		}
		return ag.Location(ctx, "")
	}, func(dir string, err error) {
		if a.layout.browse != st || !st.open {
			return
		}
		if err != nil {
			st.loading = false
			st.note = "The server named no folder to start in: " + err.Error()
			return
		}
		st.location, st.rel, st.path = dir, ".", dir
		a.browseList()
	})
}

// browseList reads the folder on show and fills the dialog with what the
// server listed there.
func (a *app) browseList() {
	st := a.layout.browse
	if st == nil {
		return
	}
	st.loading, st.note = true, ""
	cfg, location, rel := a.activeServerConn(), st.location, st.rel
	runRequest(a, &st.request, func(ctx context.Context) ([]opencode.FileEntry, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ag, err := opencode.NewClient(cfg.transport())
		if err != nil {
			return nil, err
		}
		return ag.ListDir(ctx, location, rel)
	}, func(entries []opencode.FileEntry, err error) {
		if a.layout.browse != st || !st.open {
			return
		}
		st.loading = false
		if err != nil {
			st.entries, st.note = nil, "The folder did not open: "+err.Error()
			return
		}
		st.entries = entries
		st.path = st.dir()
	})
}

// browseGo reads the folder the path field names: an absolute path lands
// where it says, a relative one under the browse's own.
func (a *app) browseGo() {
	st := a.layout.browse
	if st == nil {
		return
	}
	typed := strings.TrimSpace(st.path)
	if typed == "" {
		return
	}
	rel := typed
	if strings.HasPrefix(typed, "/") {
		// An absolute path is still walked from the location, so naming it
		// files no project row of its own.
		var err error
		if rel, err = filepath.Rel(st.location, typed); err != nil {
			st.note = "That path is not on the server: " + err.Error()
			return
		}
	}
	st.rel = path.Clean(rel)
	a.browseList()
}

// browseUp steps one folder up, past the browse's own root when asked.
func (a *app) browseUp() {
	st := a.layout.browse
	if st == nil {
		return
	}
	st.rel = path.Clean(path.Join(st.rel, ".."))
	a.browseList()
}

// browsePick settles the folder on show: the draft works in it, or a chat
// starts in it.
func (a *app) browsePick() {
	st := a.layout.browse
	if st == nil || st.loading || st.note != "" {
		return
	}
	dir, draft := st.dir(), st.draft
	a.closeBrowse()
	if draft {
		a.selectDraftProject(dir)
		return
	}
	a.startChatIn(dir)
}

// browseModal is the server-folder dialog: the folder on show, the folders
// and files the server listed there, and the one action the browse was
// opened for.
func (a *app) browseModal(c *ui.Context, p palette) {
	st := a.layout.browse
	if st == nil || !st.open {
		return
	}
	pick := "Start a chat here"
	if st.draft {
		pick = "Work here"
	}
	dialog(c, p, &st.open, 460, func() {
		ui.Text(c, "Browse the server").FontSize(14).FontWeight(600).SingleLine()
		ui.Text(c, "Folders on "+a.activeServerConn().name+"'s machine; the chat runs in the one picked.").
			FontSize(12.5).TextColor(p.muted)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			field := ui.Box(c).Grow(1).Height(34).Radius(8).Padding(0, 10).
				Background(p.composerBg).Border(1, p.composerBorder).Justify(ui.Center)
			if field.FocusWithin() {
				field.Border(1, p.composerBorderFocus)
			}
			field.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
					ui.Icon(c, icFolder).FontSize(13).TextColor(p.muted)
					ui.TextInputBase(c, &st.path).FontSize(12.5).
						Placeholder("/home/you/project").FillWidth().Label("Folder on the server").
						OnTextKey(func(k ui.TextKey) bool {
							if k.Key == ui.KeyEnter && k.Mods == 0 {
								a.browseGo()
								return true
							}
							return false
						})
				})
			})
			goBtn := ui.ButtonBase(c).Label("Go to the folder").Height(34).Radius(8).PaddingX(11).
				Center().Tooltip("Go to the folder")
			if goBtn.Hovered() {
				goBtn.Background(p.hover)
			}
			goBtn.OnClick(a.browseGo)
			goBtn.Children(func() { ui.Text(c, "Go").FontSize(13).TextColor(p.text) })
			up := ui.ButtonBase(c).Label("Up one folder").Size(34, 34).Radius(8).Center().
				Tooltip("Up one folder")
			if up.Hovered() {
				up.Background(p.hover)
			}
			up.OnClick(a.browseUp)
			up.Children(func() { ui.Icon(c, icChevUp).FontSize(16).TextColor(p.text) })
		})
		ui.Scroll(c).FillWidth().MaxHeight(240).Gap(1).Children(func() {
			if st.loading {
				ui.Row(c).Height(28).Padding(0, 8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Reading the server…").FontSize(12.5).TextColor(p.muted)
				})
			}
			for _, e := range st.entries {
				e := e // fresh per row, the closures keep their own entry
				if !e.Directory {
					// Files are orientation: what is in the folder, without
					// a way to pick one as a workspace.
					ui.Row(c).Height(26).Padding(0, 8).AlignItems(ui.Center).Gap(7).Children(func() {
						ui.Icon(c, icFileDoc).FontSize(12).TextColor(p.faint)
						ui.Text(c, e.Name).FontSize(12.5).TextColor(p.faint).SingleLine()
					})
					continue
				}
				c.Key("browse:" + e.Path)
				row := ui.ButtonBase(c).Label("Open "+e.Name).Height(28).Radius(7).Padding(0, 8).
					Justify(ui.Start).FillWidth()
				if row.Hovered() {
					row.Background(p.hover)
				}
				row.OnClick(func() { st.rel = e.Path; a.browseList() })
				row.Children(func() {
					ui.Icon(c, icFolder).FontSize(13).TextColor(p.muted)
					ui.Text(c, e.Name).MarginStart(7).FontSize(12.5).TextColor(p.text).
						Grow(1).SingleLine()
				})
			}
			if !st.loading && len(st.entries) == 0 {
				ui.Text(c, "Nothing in here").FontSize(12).TextColor(p.muted).Padding(6)
			}
		})
		if st.note != "" {
			ui.Text(c, st.note).FontSize(12).TextColor(p.danger).SingleLine()
		}
		ui.Text(c, st.dir()).FontSize(11.5).TextColor(p.muted).SingleLine().Ellipsis("…")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
				a.closeBrowse()
			}
			choose := components.Button(c, pick, components.ButtonPrimary)
			choose.OnClick(a.browsePick)
			choose.Disabled(st.loading || st.note != "")
		})
	})
}

func (a *app) closeBrowse() {
	if a.layout.browse != nil {
		a.layout.browse.request.stop()
		a.layout.browse = nil
	}
}
