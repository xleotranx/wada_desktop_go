package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The settings tabs.
const (
	tabServers = "servers"
	tabModels  = "models"
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
	show     bool   // the password field is revealed
	note     string // why the last save did not go through
}

// openSettings shows the settings page; tab names the page when the
// caller has one in mind.
func (a *app) openSettings(tab string) {
	a.settingsOpen = true
	if tab != "" {
		a.settingsTab = tab
	}
	a.probeUntried()
}

// closeSettings returns to the view the rail had selected.
func (a *app) closeSettings() {
	a.settingsOpen = false
	a.serverEditor = nil
	a.serverConfirm = nil
}

// settingsDialogOpen reports a settings dialog working: the guard that
// leaves Escape to the dialog itself.
func (a *app) settingsDialogOpen() bool {
	return (a.serverEditor != nil && a.serverEditor.open) ||
		(a.serverConfirm != nil && a.serverConfirm.open)
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
		if a.settingsTab == tabModels {
			a.settingsModels(c, p)
		} else {
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
			ui.Text(c, "Settings").FontSize(15.5).FontWeight(600).SingleLine()
			ui.Spacer(c)
			a.navButton(c, p, "Close settings", icClose, a.closeSettings, p.muted)
		})
		ui.Scroll(c).Grow(1).FillWidth().Padding(4, 10).Gap(2).Children(func() {
			a.settingsNavItem(c, p, tabServers, "Servers", icServer)
			a.settingsNavItem(c, p, tabModels, "Models", icCpu)
		})
	})
}

func (a *app) settingsNavItem(c *ui.Context, p palette, tab, label string, icon *ui.SVG) {
	chosen := a.settingsTab == tab
	c.Key("settings:" + tab)
	b := a.row(c, p, "Settings "+label, chosen)
	b.Height(34)
	b.OnClick(func() { a.settingsTab = tab })
	b.Children(func() {
		tint := p.muted
		if chosen {
			tint = p.text
		}
		ui.Icon(c, icon).FontSize(15).TextColor(tint)
		ui.Text(c, label).MarginStart(9).FontSize(13.5).SingleLine()
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
			for _, s := range a.servers {
				a.serverCard(c, p, s)
			}
		})
	})
}

// serverCard is one connection: its name and state, its password, and the
// actions the app and the user have on it.
func (a *app) serverCard(c *ui.Context, p palette, s serverConn) {
	h := a.serverHealth[s.id]
	active := s.id == a.activeServer
	c.Key("server:" + s.id)
	ui.Box(c).FillWidth().Radius(14).Padding(14, 16).
		Background(p.bg).Border(1, p.hairline).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icServer).FontSize(15).TextColor(p.muted).Shrink(0)
				ui.Text(c, s.name).FontSize(14).FontWeight(600).TextColor(p.text).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				if active {
					a.badge(c, p, "Active", p.accent, p.accentText)
				} else if s.local {
					a.badge(c, p, "Local", p.selected, p.text)
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
				passwordLine := serverPasswordLine(s)
				if active && a.modelsLoaded {
					on, total := a.enabledModelCount()
					passwordLine += fmt.Sprintf("  ·  %d of %d models on", on, total)
				}
				ui.Text(c, passwordLine).FontSize(12).TextColor(p.faint).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			})
		})
	})
}

// serverMenu holds the actions that do not earn a row shape of their own.
// A server the app runs itself has no menu while it is stopped: Use is the
// only thing to do with it.
func (a *app) serverMenu(c *ui.Context, p palette, s serverConn) {
	active := s.id == a.activeServer
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
				a.probe(s)
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
	a.serverEditor = &serverEditor{open: true}
}

// editServer opens the editor on one connection, its password shown as the
// fields carry it.
func (a *app) editServer(s serverConn) {
	a.serverEditor = &serverEditor{
		open:     true,
		id:       s.id,
		name:     s.name,
		local:    s.local,
		managed:  s.managed,
		url:      s.url,
		password: s.password,
	}
}

// saveServerEditor writes the dialog's fields onto the connection,
// validating them first; the active server dials again with the new
// details, any other is probed so the list tells the truth at once.
func (a *app) saveServerEditor() {
	st := a.serverEditor
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
		base, err := apiBase(url)
		if err != nil {
			st.note = err.Error()
			return
		}
		// The stored address is the canonical one: the row, the probe and
		// the dial all show and use the same spelling.
		url = strings.TrimSuffix(base, "/api")
	}
	a.serverEditor = nil
	if st.id == "" {
		s := serverConn{id: newServerID(), name: name, url: url, password: st.password}
		a.servers = append(a.servers, s)
		a.saveState()
		a.probe(s)
		return
	}
	edited := false
	for i := range a.servers {
		if a.servers[i].id != st.id {
			continue
		}
		a.servers[i].name = name
		a.servers[i].managed = st.managed
		a.servers[i].url = url
		a.servers[i].password = st.password
		edited = true
		break
	}
	if !edited {
		return
	}
	a.saveState()
	if st.id == a.activeServer {
		a.reconnect()
		return
	}
	if s, ok := a.serverByID(st.id); ok {
		a.probe(s)
	}
}

// requestDeleteServer shows the dialog that stands between a click and a
// connection gone for good. The local and active rows are never dropped.
func (a *app) requestDeleteServer(s serverConn) {
	if s.local || s.id == a.activeServer {
		return
	}
	a.serverConfirm = &confirmState{open: true, sid: s.id, what: s.name}
}

// deleteServerConfirmed drops the connection after the dialog's say-so.
func (a *app) deleteServerConfirmed() {
	st := a.serverConfirm
	a.serverConfirm = nil
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
				a.pillButton(c, p, "Disable all", nil, func() { a.setAllModelsEnabled(false) })
				a.pillButton(c, p, "Enable all", nil, func() { a.setAllModelsEnabled(true) })
			})
			if total > 0 && enabled == 0 {
				ui.Text(c, "Every model is off — the composer's picker is empty. Turn at least one back on.").
					FontSize(12.5).TextColor(p.orange)
			}
			textField(c, p, &a.modelsQuery, "Search models")
			if !a.modelsLoaded {
				ui.Text(c, a.catalogNote(srv)).FontSize(13).TextColor(p.muted)
			} else if total == 0 {
				ui.Text(c, "The server reported no models.").FontSize(13).TextColor(p.muted)
			} else if len(rows) == 0 {
				ui.Text(c, "No models match.").FontSize(13).TextColor(p.muted)
			}
			ui.List(c.Key("settings-models"), &a.modelsList, len(rows)).Grow(1).FillWidth().Gap(1).
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

// catalogNote words why the Models tab has nothing to show yet.
func (a *app) catalogNote(srv serverConn) string {
	h := a.serverHealth[srv.id]
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
	model    *agentModel
}

func (r catalogRow) key() string {
	if r.model == nil {
		return "provider:" + r.provider
	}
	return "model:" + r.model.providerID + "/" + r.model.id
}

// catalogRows is the Models tab's lines: one heading per provider with its
// models under it, narrowed by the search field.
func (a *app) catalogRows() []catalogRow {
	query := strings.ToLower(strings.TrimSpace(a.modelsQuery))
	byProvider := map[string][]agentModel{}
	providers := []string{}
	for _, m := range a.models {
		if query != "" && !strings.Contains(strings.ToLower(m.name), query) &&
			!strings.Contains(strings.ToLower(m.providerID), query) &&
			!strings.Contains(strings.ToLower(m.id), query) {
			continue
		}
		if _, seen := byProvider[m.providerID]; !seen {
			providers = append(providers, m.providerID)
		}
		byProvider[m.providerID] = append(byProvider[m.providerID], m)
	}
	sort.Strings(providers)
	rows := []catalogRow{}
	for _, prov := range providers {
		models := byProvider[prov]
		sort.Slice(models, func(i, j int) bool { return models[i].name < models[j].name })
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
	for _, m := range a.models {
		if m.providerID != provider {
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
func (a *app) modelRow(c *ui.Context, p palette, m agentModel) {
	on := !a.modelDisabled(m)
	ui.Row(c).FillWidth().Height(46).Padding(0, 10).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, m.name).FontSize(13.5).TextColor(p.text).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				if m.free {
					a.badge(c, p, "Free", p.bg, p.greenBadge).Border(1, p.hairline)
				}
			})
			ui.Text(c, m.providerID+"/"+m.id).FontSize(11.5).TextColor(p.faint).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
		c.Key("model-switch:" + m.providerID + "/" + m.id)
		sw := ui.Switch(c, &on).Label("Keep " + m.name + " in the composer's picker")
		sw.OnClick(func() { a.setModelEnabled(m, on) })
	})
}

// modelDisabled reports one model of the active server turned off.
func (a *app) modelDisabled(m agentModel) bool {
	return a.modelOff[modelKey(a.activeServer, m.providerID, m.id)]
}

// setModelEnabled turns one model of the active server on or off; the
// composer's picker follows at once.
func (a *app) setModelEnabled(m agentModel, on bool) {
	key := modelKey(a.activeServer, m.providerID, m.id)
	if on {
		delete(a.modelOff, key)
	} else {
		a.modelOff[key] = true
	}
	a.saveState()
}

// setAllModelsEnabled turns the whole catalog of the active server on or
// off in one move.
func (a *app) setAllModelsEnabled(on bool) {
	for _, m := range a.models {
		key := modelKey(a.activeServer, m.providerID, m.id)
		if on {
			delete(a.modelOff, key)
		} else {
			a.modelOff[key] = true
		}
	}
	a.saveState()
}

// enabledModelCount is how many of the catalog's models the picker offers.
func (a *app) enabledModelCount() (on, total int) {
	for _, m := range a.models {
		if !a.modelDisabled(m) {
			on++
		}
	}
	return on, len(a.models)
}

// settingsDialogs carries the dialogs opened by the settings: the server
// editor and the delete confirmation.
func (a *app) settingsDialogs(c *ui.Context, p palette) {
	if st := a.serverEditor; st != nil && st.open {
		title := "Add a server"
		if st.id != "" {
			title = "Edit " + st.name
		}
		dialog(c, p, &st.open, 460, func() {
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
				settingsField(c, p, "Address", "Where the server listens, e.g. http://127.0.0.1:4096.",
					&st.url, "http://127.0.0.1:4096", false, nil)
			}
			settingsField(c, p, "Password", passwordHint(st), &st.password, passwordPlaceholder(st), !st.show, func() {
				a.linkButton(c, p, "Generate a password", "Generate", func() { st.password = serverKey() })
				ui.Checkbox(c, &st.show, "Show").MarginStart(6)
			})
			if st.note != "" {
				ui.Text(c, st.note).FontSize(12).TextColor(p.danger).SingleLine()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
				cancel.OnClick(func() { a.serverEditor = nil })
				cancel.Children(func() {
					ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
				})
				save := ui.PrimaryButton(c, "Save")
				save.OnClick(a.saveServerEditor)
			})
		})
	}

	if st := a.serverConfirm; st != nil && st.open {
		title := st.what
		if title == "" {
			title = "this server"
		}
		dialog(c, p, &st.open, 380, func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icTrash).FontSize(14).TextColor(p.orange)
				ui.Text(c, "Delete “"+title+"”?").FontSize(14).FontWeight(600).SingleLine()
			})
			ui.Text(c, "Its password and model choices go with it. The chats stay on the server.").
				FontSize(12.5).TextColor(p.muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c).Label("Cancel").Height(28).Radius(999).PaddingX(12)
				cancel.OnClick(func() { a.serverConfirm = nil })
				cancel.Children(func() {
					ui.Text(c, "Cancel").FontSize(13).TextColor(p.text)
				})
				del := ui.ButtonBase(c).Label("Delete").Height(28).Radius(999).PaddingX(12).
					Background(p.orange)
				del.OnClick(a.deleteServerConfirmed)
				del.Children(func() {
					ui.Text(c, "Delete").FontSize(13).FontWeight(500).TextColor(p.dangerText)
				})
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
		return "The password that server was started with. Leave it empty for a server that takes connections bare."
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
		box := ui.Box(c).Shrink(0).FillWidth().Radius(8).Padding(8, 10).
			Background(p.composerBg).Border(1, p.composerBorder)
		if box.FocusWithin() {
			box.Border(1, p.composerBorderFocus)
		}
		box.Children(func() {
			in := ui.TextInputBase(c, value).FontSize(13).Placeholder(placeholder).Label(label)
			if secret {
				in.Password()
			}
		})
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

// badge is a small rounded tag beside a name.
func (a *app) badge(c *ui.Context, p palette, label string, bg, fg ui.Color) ui.Element {
	b := ui.Box(c).Height(19).Radius(999).Padding(0, 8).Center().Shrink(0).Background(bg)
	b.Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(500).TextColor(fg).SingleLine()
	})
	return b
}

// linkButton is the plain-text action of a settings row: label names it
// for assistive technology and the tooltip, text is what the row shows.
func (a *app) linkButton(c *ui.Context, p palette, label, text string, on func()) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(26).Radius(8).PaddingX(9).Center().
		Cursor(ui.CursorPointer).Shrink(0).Tooltip(label)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(on)
	b.Children(func() {
		ui.Text(c, text).FontSize(12.5).TextColor(p.text).SingleLine()
	})
	return b
}

// pillButton is a bordered action of the settings headers.
func (a *app) pillButton(c *ui.Context, p palette, label string, icon *ui.SVG, on func()) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(30).Radius(999).PaddingX(11).Gap(6).
		Center().Cursor(ui.CursorPointer).Shrink(0).
		Background(p.bg).Border(1, p.hairline)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.OnClick(on)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(13).TextColor(p.text)
		}
		ui.Text(c, label).FontSize(13).TextColor(p.text).SingleLine()
	})
	return b
}
