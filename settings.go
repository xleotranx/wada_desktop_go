package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// The settings tabs.
const (
	tabServers   = "servers"
	tabModels    = "models"
	tabShortcuts = "shortcuts"
)

// settingsNavWidth is the settings page's own navigation column.
const settingsNavWidth float32 = 258

// serverEditor is the add/edit dialog of one server connection.
type serverEditor struct {
	open     bool
	id       string // the connection being edited; empty when adding
	name     string
	local    bool // the built-in row keeps the "runs with the app" switch
	managed  bool
	url      string
	password string
	// A pairing link redeemed by this save: its token and the moment it
	// was issued become the connection's credential.
	paired   bool
	pairedAt int64
	show     bool   // the password field is revealed
	pairing  bool   // a pairing link is being redeemed
	note     string // why the last save did not go through
}

// openSettings shows the settings page; tab names the page when the
// caller has one in mind.
func (a *app) openSettings(tab string) {
	a.layout.settingsOpen = true
	if tab != "" {
		a.layout.settingsTab = tab
	}
	a.probeUntried()
}

// closeSettings returns to the view the rail had selected.
func (a *app) closeSettings() {
	a.layout.settingsOpen = false
	a.closeServerEditor()
	a.layout.serverConfirm = nil
}

// settingsDialogOpen reports a settings dialog working: the guard that
// leaves Escape to the dialog itself.
func (a *app) settingsDialogOpen() bool {
	return (a.layout.serverEditor != nil && a.layout.serverEditor.open) ||
		(a.layout.serverConfirm != nil && a.layout.serverConfirm.open)
}

// settingsPane is the settings surface in the window's card: the page's
// own navigation column beside the tab it shows.
func (a *app) settingsPane(c *ui.Context, p palette, headerH float32) {
	if !a.settingsDialogOpen() {
		c.OnShortcut(0, ui.KeyEscape, a.closeSettings)
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		a.settingsNav(c, p, headerH)
		ui.Box(c).Width(1).FillHeight().Background(p.hairline)
		switch a.layout.settingsTab {
		case tabModels:
			a.settingsModels(c, p)
		case tabShortcuts:
			a.settingsShortcuts(c, p)
		default:
			a.settingsServers(c, p)
		}
	})
}

// settingsNav is the settings page's own sidebar: the title, the close
// shape and the tabs.
func (a *app) settingsNav(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Width(settingsNavWidth).Background(p.sidebarBg).Children(func() {
		ui.Row(c).FillWidth().Height(headerH).DragWindow().Padding(0, 14).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Settings").FontSize(16).FontWeight(650).SingleLine()
			ui.Spacer(c)
			a.navButton(c, p, "Close settings", icClose, a.closeSettings, p.muted)
		})
		ui.Scroll(c).Grow(1).FillWidth().Padding(4, 10).Gap(2).Children(func() {
			a.settingsNavItem(c, p, tabServers, "Servers", icServer)
			a.settingsNavItem(c, p, tabModels, "Models", icCpu)
			a.settingsNavItem(c, p, tabShortcuts, "Keyboard", icKey)
			ui.Spacer(c)
			ui.Box(c).FillWidth().Height(1).Background(p.hairline).Margin(4, 2)
			// The About foot: the app's own name and version, so a bug
			// report says what build it belongs to.
			ui.Text(c, "Wada Desktop").FontSize(12).FontWeight(500).TextColor(p.muted).Margin(4, 2)
			ui.Text(c, "Version "+appVersion()).FontSize(11).TextColor(p.faint).Margin(2, 2)
		})
	})
}

func (a *app) settingsNavItem(c *ui.Context, p palette, tab, label string, icon *ui.SVG) {
	chosen := a.layout.settingsTab == tab
	c.Key("settings:" + tab)
	b := a.row(c, p, "Settings "+label, chosen)
	b.Height(36)
	b.OnClick(func() { a.layout.settingsTab = tab })
	b.Children(func() {
		tint := p.muted
		if chosen {
			tint = p.orange
		}
		ui.Icon(c, icon).FontSize(15).TextColor(tint)
		t := ui.Text(c, label).MarginStart(9).FontSize(13.5).SingleLine()
		if chosen {
			t.FontWeight(500)
		}
	})
}

// settingsHeading opens a tab: its name, what it is for, and the one
// action the page is built around.
func (a *app) settingsHeading(c *ui.Context, p palette, title, blurb, action string, on func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(24).FontWeight(600).TextColor(p.text).SingleLine()
			ui.Spacer(c)
			if action != "" {
				a.pillButton(c, p, action, icPlus, on)
			}
		})
		ui.Text(c, blurb).FontSize(13).LineHeight(1.45).TextColor(p.muted)
	})
}

// settingsServers is the Servers tab: one card per connection, with its
// password and how it last answered.
func (a *app) settingsServers(c *ui.Context, p palette) {
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).MaxWidth(720).Margin(0, ui.Auto).FillWidth().
			Padding(36, 32, 40, 32).Gap(14).Children(func() {
			a.settingsHeading(c, p, "Servers",
				"Chats, models and history come from the active server. The app runs its own opencode server by default; point it at another one when your work lives there.",
				"Add server", a.addServer)
			for _, s := range a.connections.servers {
				a.serverCard(c, p, s)
			}
		})
	})
}

// serverCard is one connection: its name and state, its password, and the
// actions the app and the user have on it.
func (a *app) serverCard(c *ui.Context, p palette, s serverConn) {
	h := a.connections.serverHealth[s.id]
	active := s.id == a.connections.activeServer
	c.Key("server:" + s.id)
	ui.Box(c).FillWidth().Radius(14).Padding(14, 16).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icServer).FontSize(15).TextColor(p.muted).Shrink(0)
				ui.Text(c, s.name).FontSize(14).FontWeight(600).TextColor(p.text).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				if active {
					a.badge(c, "Active", components.BadgeAccent)
				} else if s.local {
					a.badge(c, "Local", components.BadgeNeutral)
				}
				ui.Spacer(c)
				if !active {
					a.linkButton(c, p, "Use "+s.name, "Use", func() { a.useServer(s.id) })
				}
				a.linkButton(c, p, "Edit "+s.name, "Edit", func() { a.editServer(s) })
				a.serverMenu(c, p, s)
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Box(c).Size(7, 7).Radius(999).Shrink(0).Background(healthTint(p, h.status))
				ui.Text(c, serverLine(s, h)).FontSize(12.5).TextColor(p.muted).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Icon(c, icKey).FontSize(11).TextColor(p.faint).Shrink(0)
				line := serverPasswordLine(s)
				// What the server's dials have kept: its listed chats and
				// folders, its catalog and how much of it is on, whatever
				// server is on show.
				scope := a.connections.scopes[s.id]
				if scope != nil && scope.sessionsLoaded {
					line += fmt.Sprintf("  ·  %d chats", len(scope.sessions))
				}
				if scope != nil && len(scope.projects) > 0 {
					line += fmt.Sprintf("  ·  %d projects", len(scope.projects))
				}
				if on, total := a.modelsEnabled(s.id, scope); total > 0 {
					line += fmt.Sprintf("  ·  %d of %d models on", on, total)
				}
				ui.Text(c, line).FontSize(12).TextColor(p.faint).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			})
			if h.status == healthReady {
				// The addresses the server names for itself, when they are
				// not the one the app already dialed.
				if reach := serverFactsLine(a.connections.serverFacts[s.id], s, h); reach != "" {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
						ui.Icon(c, icGlobe).FontSize(11).TextColor(p.faint).Shrink(0)
						ui.Text(c, reach).FontSize(12).TextColor(p.faint).
							SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					})
				}
				// The link the app minted for another client, while it is
				// still good for joining.
				if link := a.activePairLink(s.id); link != "" {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
						ui.Icon(c, icPlug).FontSize(11).TextColor(p.faint).Shrink(0)
						ui.Text(c, "Pairing link (5 min): "+link).FontSize(11.5).TextColor(p.faint).
							SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					})
				}
			}
		})
	})
}

// serverMenu holds the actions that do not earn a row shape of their own.
// A server the app runs itself has no menu while it is stopped: Use is the
// only thing to do with it.
func (a *app) serverMenu(c *ui.Context, p palette, s serverConn) {
	active := s.id == a.connections.activeServer
	if s.local && s.managed && !active {
		return
	}
	c.Key("servermenu:" + s.id)
	b := ui.ButtonBase(c).Label("Server options, "+s.name).Size(26, 26).Radius(7).
		Center().Tooltip("Server options")
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Menu(func(m *ui.Menu) {
		if active {
			if m.Item("Reconnect").Chosen() {
				a.reconnect()
			}
		} else if !s.managed {
			if m.Item("Test connection").Chosen() {
				a.runtime.probe(s)
			}
		}
		if a.connections.serverHealth[s.id].status == healthReady {
			if m.Item("Copy pairing link").Chosen() {
				a.copyPairLink(s)
			}
		}
		if !s.local {
			m.Separator()
			item := m.Item("Delete…")
			item.Disabled(active)
			if item.Chosen() {
				a.requestDeleteServer(s)
			}
		}
	})
	b.Children(func() { ui.Icon(c, icEllipsis).FontSize(14).TextColor(p.muted) })
}

// addServer opens the editor on a blank remote connection.
func (a *app) addServer() {
	a.closeServerEditor()
	a.layout.serverEditor = &serverEditor{open: true}
}

// editServer opens the editor on one connection, its password shown as the
// fields carry it.
func (a *app) editServer(s serverConn) {
	a.closeServerEditor()
	a.layout.serverEditor = &serverEditor{
		open:     true,
		id:       s.id,
		name:     s.name,
		local:    s.local,
		managed:  s.managed,
		url:      s.url,
		password: s.password,
		paired:   s.paired,
		pairedAt: s.pairedAt,
	}
}

// saveServerEditor writes the dialog's fields onto the connection,
// validating them first; the active server dials again with the new
// details, any other is probed so the list tells the truth at once.
func (a *app) saveServerEditor() {
	st := a.layout.serverEditor
	if st == nil {
		return
	}
	name := strings.TrimSpace(st.name)
	if name == "" {
		st.note = "Give the server a name."
		return
	}
	url := strings.TrimSpace(st.url)
	switch {
	case st.managed:
		url = "" // the app picks where its own server listens
	default:
		// A pairing link is not an address: redeem it first, and the save
		// carries on with the token it hands back.
		if _, _, ok := pairLink(url); ok && !st.pairing {
			st.pairing = true
			st.note = ""
			a.runtime.pair(url, func(addr, token string, err error) {
				if a.layout.serverEditor != st || !st.open || !st.pairing || st.url != url {
					return // the dialog closed, or another save moved on
				}
				st.pairing = false
				if err != nil {
					st.note = "The pairing link did not work: " + err.Error()
					return
				}
				st.url, st.password = addr, token
				st.paired, st.pairedAt = true, time.Now().UnixMilli()
				a.saveServerEditor()
			})
			return
		}
		base, err := opencode.APIBase(url)
		if err != nil {
			st.note = err.Error()
			return
		}
		// The stored address is the canonical one: the row, the probe and
		// the dial all show and use the same spelling.
		url = strings.TrimSuffix(base, "/api")
	}
	a.closeServerEditor()
	if st.id == "" {
		s := serverConn{id: newServerID(), name: name, url: url, password: st.password,
			paired: st.paired, pairedAt: st.pairedAt}
		a.connections.servers = append(a.connections.servers, s)
		a.saveState()
		a.runtime.probe(s)
		return
	}
	edited := false
	for i := range a.connections.servers {
		if a.connections.servers[i].id != st.id {
			continue
		}
		// The row keeps its pairing until a save replaces the credential:
		// a link redeemed just now carries its own issue time, a password
		// typed over the token takes its place, and an emptied field leaves
		// nothing paired at all.
		a.invalidateServerWork(st.id)
		was := a.connections.servers[i]
		paired, pairedAt := was.paired, was.pairedAt
		switch {
		case st.paired && st.pairedAt != was.pairedAt:
			paired, pairedAt = true, st.pairedAt
		case was.paired && st.password != was.password, st.password == "":
			paired, pairedAt = false, 0
		}
		a.connections.servers[i].name = name
		a.connections.servers[i].managed = st.managed
		a.connections.servers[i].url = url
		a.connections.servers[i].password = st.password
		a.connections.servers[i].paired = paired
		a.connections.servers[i].pairedAt = pairedAt
		edited = true
		break
	}
	if !edited {
		return
	}
	a.saveState()
	if st.id == a.connections.activeServer {
		a.reconnect()
		return
	}
	if s, ok := a.serverByID(st.id); ok {
		a.runtime.probe(s)
	}
}

// requestDeleteServer shows the dialog that stands between a click and a
// connection gone for good. The local and active rows are never dropped.
func (a *app) requestDeleteServer(s serverConn) {
	if s.local || s.id == a.connections.activeServer {
		return
	}
	a.layout.serverConfirm = &confirmState{open: true, sid: s.id, what: s.name}
}

// deleteServerConfirmed drops the connection after the dialog's say-so.
func (a *app) deleteServerConfirmed() {
	st := a.layout.serverConfirm
	a.layout.serverConfirm = nil
	if st == nil || st.sid == "" {
		return
	}
	a.dropServer(st.sid)
}

// settingsModels is the Models tab: the active server's catalog, one
// switch per model, and the bulk actions over it.
func (a *app) settingsModels(c *ui.Context, p palette) {
	srv := a.activeServerConn()
	rows := a.catalogRows()
	enabled, total := a.enabledModelCount()
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Column(c).MaxWidth(720).Margin(0, ui.Auto).FillWidth().FillHeight().
			Padding(36, 32, 24, 32).Gap(12).Children(func() {
			a.settingsHeading(c, p, "Models",
				"The composer's picker offers the models that stay on. The catalog comes from "+srv.name+"; switching servers changes the list.",
				"", nil)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, fmt.Sprintf("%d of %d on", enabled, total)).FontSize(12.5).TextColor(p.muted)
				ui.Spacer(c)
				disableAll := a.pillButton(c, p, "Disable all", nil, func() { a.setAllModelsEnabled(false) })
				disableAll.Disabled(total == 0)
				enableAll := a.pillButton(c, p, "Enable all", nil, func() { a.setAllModelsEnabled(true) })
				enableAll.Disabled(total == 0)
			})
			if total > 0 && enabled == 0 {
				ui.Text(c, "Every model is off — the composer's picker is empty. Turn at least one back on.").
					FontSize(12.5).TextColor(p.orange)
			}
			textField(c, "Search models", &a.layout.modelsQuery, "Search models")
			if !a.scope().modelsLoaded {
				components.Skeleton(c, components.SkeletonOptions{Lines: 5, Label: "Loading the model catalog"})
				ui.Text(c, a.catalogNote(srv)).FontSize(13).TextColor(p.muted)
			} else if total == 0 {
				ui.Text(c, "The server reported no models.").FontSize(13).TextColor(p.muted)
			} else if len(rows) == 0 {
				ui.Text(c, "No models match.").FontSize(13).TextColor(p.muted)
			}
			ui.List(c.Key("settings-models"), &a.layout.modelsList, len(rows)).Grow(1).FillWidth().Gap(1).
				ItemKey(func(i int) any { return rows[i].key() }).
				Rows(func(row ui.ListRow) {
					r := rows[row.Index]
					if r.model == nil {
						a.providerRow(row.Context, p, r.provider)
					} else {
						a.modelRow(row.Context, p, *r.model)
					}
				})
		})
	})
}

// settingsShortcuts is the Keyboard tab: every command key the window
// answers, grouped by what it moves, so the keys are discoverable without
// a manual. The divider keys ride under the groups because they belong to
// a focused divider rather than to the window.
func (a *app) settingsShortcuts(c *ui.Context, p palette) {
	groups := []struct {
		title string
		rows  [][2]string
	}{
		{"General", [][2]string{
			{"Command palette", "⌘K"},
			{"Settings", "⌘,"},
			{"Keyboard shortcuts", "⌘/"},
			{"Close a dialog or find", "Esc"},
		}},
		{"Chats", [][2]string{
			{"New chat", "⌘N"},
			{"Open a recent chat", "⌘1…9"},
			{"Find in chat", "⌘F"},
			{"Find next match", "⌘G"},
			{"Find previous match", "⌘⇧G"},
			{"Search chats", "⌘⇧F"},
			{"Choose a model", "⌃⇧M"},
		}},
		{"Workspace", [][2]string{
			{"Toggle sidebar", "⌘B"},
			{"Toggle terminal", "⌘J / ⌃`"},
			{"Toggle tools", "⌘⇧J"},
			{"Open Files", "⌘P"},
			{"Open Changes", "⌃⇧G"},
			{"Move a focused divider", "← → / ↑ ↓"},
			{"Reset a focused divider", "Home / End"},
		}},
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).MaxWidth(720).Margin(0, ui.Auto).FillWidth().
			Padding(36, 32, 40, 32).Gap(20).Children(func() {
			a.settingsHeading(c, p, "Keyboard",
				"Every command the window answers. Dragging a workspace divider resizes its pane; double-clicking it returns the pane to its opening width.",
				"", nil)
			for _, group := range groups {
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, group.title).FontSize(12).FontWeight(600).
						TextColor(p.faint).MarginStart(2)
					ui.Box(c).FillWidth().Radius(12).Background(p.detailBg).
						Border(1, p.hairline).Clip().Children(func() {
						ui.Column(c).Children(func() {
							for i, row := range group.rows {
								if i > 0 {
									ui.Box(c).FillWidth().Height(1).Background(p.hairline)
								}
								ui.Row(c).FillWidth().Height(40).Padding(0, 14).
									AlignItems(ui.Center).Children(func() {
									ui.Text(c, row[0]).FontSize(13).TextColor(p.text).SingleLine()
									ui.Spacer(c)
									a.keycap(c, p, row[1])
								})
							}
						})
					})
				})
			}
		})
	})
}

// keycap is the quiet bordered badge a shortcut is written in.
func (a *app) keycap(c *ui.Context, p palette, keys string) {
	ui.Box(c).Height(23).PaddingX(7).Radius(5).Center().
		Background(p.bg).Border(1, p.hairline).Children(func() {
		ui.Text(c, keys).FontSize(11.5).Font("monospace").TextColor(p.muted).SingleLine()
	})
}

// catalogNote words why the Models tab has nothing to show yet.
func (a *app) catalogNote(srv serverConn) string {
	h := a.connections.serverHealth[srv.id]
	if h.status == healthDown {
		if h.detail != "" {
			return srv.name + " did not answer — " + h.detail
		}
		return srv.name + " did not answer."
	}
	return "Loading the catalog from " + srv.name + "…"
}

// catalogRow is one line of the Models tab: a provider heading, or one of
// its models.
type catalogRow struct {
	provider string
	model    *opencode.Model
}

func (r catalogRow) key() string {
	if r.model == nil {
		return "provider:" + r.provider
	}
	return "model:" + r.model.ProviderID + "/" + r.model.ID
}

// catalogRows is the Models tab's lines: one heading per provider with its
// models under it, narrowed by the search field.
func (a *app) catalogRows() []catalogRow {
	query := strings.ToLower(strings.TrimSpace(a.layout.modelsQuery))
	byProvider := map[string][]opencode.Model{}
	providers := []string{}
	for _, m := range a.scope().models {
		if query != "" && !strings.Contains(strings.ToLower(m.Name), query) &&
			!strings.Contains(strings.ToLower(m.ProviderID), query) &&
			!strings.Contains(strings.ToLower(m.ID), query) {
			continue
		}
		if _, seen := byProvider[m.ProviderID]; !seen {
			providers = append(providers, m.ProviderID)
		}
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], m)
	}
	sort.Strings(providers)
	rows := []catalogRow{}
	for _, prov := range providers {
		models := byProvider[prov]
		sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
		rows = append(rows, catalogRow{provider: prov})
		for i := range models {
			rows = append(rows, catalogRow{model: &models[i]})
		}
	}
	return rows
}

// providerRow is the heading of one provider's models.
func (a *app) providerRow(c *ui.Context, p palette, provider string) {
	on, total := 0, 0
	for _, m := range a.scope().models {
		if m.ProviderID != provider {
			continue
		}
		total++
		if !a.modelDisabled(m) {
			on++
		}
	}
	ui.Row(c).FillWidth().Height(30).Padding(0, 10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, provider).FontSize(12).FontWeight(600).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		ui.Text(c, fmt.Sprintf("%d of %d on", on, total)).FontSize(11.5).TextColor(p.faint)
	})
}

// modelRow is one model with the switch that keeps it in the composer's
// picker.
func (a *app) modelRow(c *ui.Context, p palette, m opencode.Model) {
	on := !a.modelDisabled(m)
	ui.Row(c).FillWidth().Height(46).Padding(0, 10).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, m.Name).FontSize(13.5).TextColor(p.text).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				if m.Free {
					a.badge(c, "Free", components.BadgeSuccess)
				}
			})
			ui.Text(c, m.ProviderID+"/"+m.ID).FontSize(11.5).TextColor(p.faint).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
		c.Key("model-switch:" + m.ProviderID + "/" + m.ID)
		sw := ui.Switch(c, &on).Label("Keep " + m.Name + " in the composer's picker")
		sw.OnClick(func() { a.setModelEnabled(m, on) })
	})
}

// modelDisabled reports one model of the active server turned off.
func (a *app) modelDisabled(m opencode.Model) bool {
	return a.connections.modelOff[modelKey(a.connections.activeServer, m.ProviderID, m.ID)]
}

// modelsEnabled counts one server's catalog, the models its user turned
// off left out; the active server's count is what the picker offers.
func (a *app) modelsEnabled(serverID string, scope *serverScope) (on, total int) {
	if scope == nil || !scope.modelsLoaded {
		return 0, 0
	}
	for _, m := range scope.models {
		if !a.connections.modelOff[modelKey(serverID, m.ProviderID, m.ID)] {
			on++
		}
		total++
	}
	return on, total
}

// setModelEnabled turns one model of the active server on or off; the
// composer's picker follows at once.
func (a *app) setModelEnabled(m opencode.Model, on bool) {
	key := modelKey(a.connections.activeServer, m.ProviderID, m.ID)
	if on {
		delete(a.connections.modelOff, key)
	} else {
		a.connections.modelOff[key] = true
	}
	a.saveState()
}

// setAllModelsEnabled turns the whole catalog of the active server on or
// off in one move.
func (a *app) setAllModelsEnabled(on bool) {
	for _, m := range a.scope().models {
		key := modelKey(a.connections.activeServer, m.ProviderID, m.ID)
		if on {
			delete(a.connections.modelOff, key)
		} else {
			a.connections.modelOff[key] = true
		}
	}
	a.saveState()
}

// enabledModelCount is how many of the catalog's models the picker offers.
func (a *app) enabledModelCount() (on, total int) {
	return a.modelsEnabled(a.connections.activeServer, a.scope())
}

// settingsDialogs carries the dialogs opened by the settings: the server
// editor and the delete confirmation.
func (a *app) settingsDialogs(c *ui.Context, p palette) {
	if st := a.layout.serverEditor; st != nil && st.open {
		title := "Add a server"
		if st.id != "" {
			title = "Edit " + st.name
		}
		dialog(c, p, &st.open, 460, title, func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).SingleLine()
			ui.Text(c, "A server your chats can run on, over the opencode HTTP API.").
				FontSize(12.5).TextColor(p.muted)
			settingsField(c, p, "Name", "What the list calls it.", &st.name, "Build box", false, nil)
			if st.local {
				hint := "The app starts its own opencode serve on a port it picks."
				if !st.managed {
					hint = "Point the app at a server you started yourself."
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Switch(c, &st.managed).Label("Run it with the app")
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, "Run it with the app").FontSize(13).TextColor(p.text)
						ui.Text(c, hint).FontSize(11.5).TextColor(p.muted)
					})
				})
			}
			if !st.managed {
				settingsField(c, p, "Address", "Where the server listens, e.g. http://127.0.0.1:4096 — or a pairing link from opencode pair.",
					&st.url, "http://127.0.0.1:4096", false, nil)
			}
			settingsField(c, p, "Password", passwordHint(st), &st.password, passwordPlaceholder(st), !st.show, func() {
				a.linkButton(c, p, "Generate a password", "Generate", func() { st.password = opencode.Secret() })
				ui.Checkbox(c, &st.show, "Show").MarginStart(6)
			})
			if st.pairing {
				ui.Text(c, "Pairing…").FontSize(12).TextColor(p.muted).SingleLine()
			} else if st.note != "" {
				ui.Text(c, st.note).FontSize(12).TextColor(p.danger).SingleLine()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
					a.closeServerEditor()
				}
				if components.Button(c, "Save", components.ButtonPrimary).Clicked() {
					a.saveServerEditor()
				}
			})
		})
	}

	if st := a.layout.serverConfirm; st != nil && st.open {
		title := st.what
		if title == "" {
			title = "this server"
		}
		dialog(c, p, &st.open, 380, "Delete server", func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icTrash).FontSize(14).TextColor(p.orange)
				ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine()
			})
			ui.Text(c, "Its password and model choices go with it. The chats stay on the server.").
				FontSize(12.5).TextColor(p.muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if components.Button(c, "Cancel", components.ButtonSecondary).Clicked() {
					a.layout.serverConfirm = nil
				}
				if components.Button(c, "Delete", components.ButtonPrimary).Clicked() {
					a.deleteServerConfirmed()
				}
			})
		})
	}
}

// passwordHint words what the password field means for the server being edited.
func passwordHint(st *serverEditor) string {
	switch {
	case st.local && st.managed:
		return "The app starts opencode serve with this password. Leave it empty and it generates one for each run."
	case st.local:
		return "The password the local server was started with."
	default:
		return "The password that server was started with, or the token a pairing link handed back. Leave it empty for a server that takes connections bare."
	}
}

// passwordPlaceholder is what the empty password field suggests.
func passwordPlaceholder(st *serverEditor) string {
	if st.local && st.managed {
		return "Generated if empty"
	}
	return "The server's password"
}

// settingsField is one labeled field of a settings dialog: the label with
// an optional control beside it, the box under both, and the hint under
// the box. A secret field hides what it holds.
func settingsField(c *ui.Context, p palette, label, hint string, value *string, placeholder string, secret bool, beside func()) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(p.text)
			ui.Spacer(c)
			if beside != nil {
				beside()
			}
		})
		in := components.TextBox(c, value, components.TextBoxOptions{}).
			FillWidth().FontSize(13).Placeholder(placeholder).Label(label)
		if secret {
			in.Password()
		}
		if hint != "" {
			ui.Text(c, hint).FontSize(11.5).LineHeight(1.4).TextColor(p.muted)
		}
	})
}

// healthTint is the status dot's color: green once a server answered,
// orange while one is on its way, red when it did not, faint untested.
func healthTint(p palette, status string) ui.Color {
	switch status {
	case healthReady:
		return p.greenBadge
	case healthConnecting:
		return p.orange
	case healthDown:
		return p.danger
	default:
		return p.faint
	}
}

// badge is a small rounded tag beside a name, in Onevra's status tones.
func (a *app) badge(c *ui.Context, label string, tone components.BadgeTone) ui.Element {
	return components.Badge(c, label, components.BadgeOptions{Tone: tone})
}

// linkButton is the plain-text action of a settings row: label names it
// for assistive technology and the tooltip, text is what the row shows.
// It takes Onevra's quiet ghost face and accent text.
func (a *app) linkButton(c *ui.Context, p palette, label, text string, on func()) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(28).Radius(8).PaddingX(9).Center().
		Cursor(ui.CursorPointer).Shrink(0).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(on)
	b.Children(func() {
		ui.Text(c, text).FontSize(13).FontWeight(500).TextColor(p.orange).SingleLine()
	})
	return b
}

// pillButton is a bordered action of the settings headers, in Onevra's
// secondary weight so it sits beside a title without shouting.
func (a *app) pillButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func()) ui.Element {
	b := components.Button(c, "", components.ButtonSecondary).Label(label).
		Shrink(0).Cursor(ui.CursorPointer)
	b.OnClick(on)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(14).TextColor(p.text)
		}
		components.Text(c, label).FontSize(13).TextColor(p.text).SingleLine()
	})
	return b
}
