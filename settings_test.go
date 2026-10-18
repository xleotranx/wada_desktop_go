package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestSettingsOpenAndClose drives the settings page in and out: the rail's
// gear opens it on the Servers tab, the chat furniture steps aside, and
// Escape, the close shape and any rail place all leave it behind.
func TestSettingsOpenAndClose(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Grep wiring"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Grep wiring"
	tt := ui.NewTester(a.view, 1400, 920)

	if tt.HasText("Add server") {
		t.Fatal("the settings page stands open before anything asked")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("the rail gear: %v", err)
	}
	if !a.layout.settingsOpen {
		t.Fatal("the gear did not open the settings")
	}
	tt.Frame()
	if !tt.HasText("Add server") || !tt.HasText("Local server") {
		t.Fatalf("the Servers tab is not drawn: %v", tt.Texts())
	}
	// The page takes the card: the conversations and the composer sit it out.
	if tt.HasText("Pinned") || tt.HasText("Compose") {
		t.Fatalf("the chat furniture stayed under the settings: %v", tt.Texts())
	}
	// The title bar names the page where the conversation's title stood.
	if title, ok := tt.Find("Settings"); !ok || title.Y >= 48 {
		t.Fatalf("the title bar does not name the settings: %+v", title)
	}

	// The nav moves to Models and back to Servers.
	if err := tt.Click("Settings Models"); err != nil {
		t.Fatalf("the Models item: %v", err)
	}
	if a.layout.settingsTab != tabModels {
		t.Fatalf("tab %q after the click", a.layout.settingsTab)
	}
	tt.Frame()
	if err := tt.Click("Settings Servers"); err != nil {
		t.Fatalf("the Servers item: %v", err)
	}
	if a.layout.settingsTab != tabServers {
		t.Fatalf("tab %q after the click back", a.layout.settingsTab)
	}

	// Escape closes the page.
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if a.layout.settingsOpen {
		t.Fatal("Escape did not close the settings")
	}
	tt.Frame()
	if tt.HasText("Add server") || !tt.HasText("Pinned") {
		t.Fatalf("the chat did not come back: %v", tt.Texts())
	}

	// The close shape does the same, and a rail place does too: the
	// selection lands back where it was.
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Close settings"); err != nil {
		t.Fatal(err)
	}
	if a.layout.settingsOpen {
		t.Fatal("the close shape did not close the settings")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Home"); err != nil {
		t.Fatal(err)
	}
	if a.layout.settingsOpen {
		t.Fatal("a rail place did not leave the settings")
	}
}

// TestServerMenuKeepsThePage: choosing from a row's menu leaves the page
// where it was. (Escape with a native menu up is the menu's own key to
// take; the headless tester does not model that, so it stays untested.)
func TestServerMenuKeepsThePage(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatal(err)
	}
	if len(tt.Menu()) == 0 {
		t.Fatal("the menu did not open")
	}
	if err := tt.ChooseMenuItem("Test connection"); err != nil {
		t.Fatal(err)
	}
	if !a.layout.settingsOpen || a.layout.settingsTab != tabServers {
		t.Fatalf("the menu disturbed the page: open %v tab %q", a.layout.settingsOpen, a.layout.settingsTab)
	}
	tt.CloseMenu()
	tt.Frame()
	if !tt.HasText("Add server") {
		t.Fatalf("the page left with the menu: %v", tt.Texts())
	}
}

// TestServerRowShowsState keeps the row's words honest: the address, how
// the server last answered and whether a password is set, in every state
// the health map can hold.
func TestServerRowShowsState(t *testing.T) {
	local := defaultLocalServer()
	local.password = "key-1"
	remote := serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"}

	for _, tc := range []struct {
		name string
		cfg  serverConn
		h    serverHealth
		want []string
	}{
		{"running local", local, serverHealth{status: healthReady, detail: "0.5.5", addr: "http://127.0.0.1:50112"},
			[]string{"http://127.0.0.1:50112", "Connected · 0.5.5", "Password set"}},
		{"starting local", local, serverHealth{status: healthConnecting},
			[]string{"Starting…", "Password set"}},
		{"stopped local", local, serverHealth{status: healthStopped},
			[]string{"Stopped", "Password set"}},
		{"untested remote", remote, serverHealth{},
			[]string{"http://10.0.0.12:4096", "Not tested yet", "No password set"}},
		{"refused remote", remote, serverHealth{status: healthDown, detail: "the password was refused"},
			[]string{"Not connected — the password was refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := serverLine(tc.cfg, tc.h)
			for _, want := range tc.want {
				if !strings.Contains(line+serverPasswordLine(tc.cfg), want) {
					t.Fatalf("row %q / %q lacks %q", line, serverPasswordLine(tc.cfg), want)
				}
			}
		})
	}

	// The card draws the active badge and the state dot.
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.setHealth("srv-box", serverHealth{status: healthDown, detail: "no answer"})
	a.layout.settingsOpen = true
	a.layout.settingsTab = tabServers
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Active") || !tt.HasText("Local server") || !tt.HasText("Build box") {
		t.Fatalf("the rows are off: %v", tt.Texts())
	}
	if !tt.HasText("Not connected — no answer") {
		t.Fatalf("the failing server reads wrong: %v", tt.Texts())
	}
	if !tt.HasText("Use Build box") {
		t.Fatalf("the other server has no way to be used: %v", tt.Texts())
	}
}

// TestServerUseSwitches queues the dial, clears what renders over the new
// server and hands its pins back. The old server's conversations and
// catalog stay in their scope, so a switch back shows them again.
func TestServerUseSwitches(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.layout.settingsOpen = true
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_local", Title: "Local chat"}}
	a.conversation.target.selectSession("ses_local")
	a.conversation.selected = "Local chat"
	a.scope().models = []opencode.Model{{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true}}
	a.scope().modelsLoaded = true
	a.scope().pinned["ses_local"] = true
	a.scope().pinOrder = []string{"ses_local"}
	a.scopeFor("srv-box").setPins([]string{"ses_remote"})

	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Use Build box"); err != nil {
		t.Fatalf("the Use action: %v", err)
	}
	if a.connections.activeServer != "srv-box" {
		t.Fatalf("active server %q after the click", a.connections.activeServer)
	}
	if a.conversation.target.session != "" || len(a.scope().sessions) != 0 || len(a.scope().models) != 0 || a.scope().modelsLoaded {
		t.Fatalf("the opened server's view did not start fresh: %q %v %v", a.conversation.target.session, a.scope().sessions, a.scope().models)
	}
	local := a.connections.scopes[localServerID]
	if len(local.sessions) != 1 || !local.sessionsLoaded || len(local.models) != 1 || !local.modelsLoaded {
		t.Fatalf("the local scope was thrown away: %+v", local)
	}
	if !a.scope().pinned["ses_remote"] || len(a.scope().pinOrder) != 1 || a.scope().pinOrder[0] != "ses_remote" || a.scope().pinned["ses_local"] {
		t.Fatalf("pins did not follow the switch: %v %v", a.scope().pinned, a.scope().pinOrder)
	}
	if got := a.scopeFor(localServerID).pinOrder; len(got) != 1 || got[0] != "ses_local" {
		t.Fatalf("the local pins were not kept: %v", a.connections.scopes)
	}
	select {
	case job := <-commandsOf(a):
		if job.kind != "server" || job.server.id != "srv-box" || len(job.pins) != 1 || job.pins[0] != "ses_remote" {
			t.Fatalf("switch job %+v", job)
		}
	default:
		t.Fatal("the switch queued no dial")
	}
	// The local server is only run while it is active.
	if h := a.connections.serverHealth[localServerID]; h.status != healthStopped {
		t.Fatalf("the local server's health says %+v", h)
	}
	tt.Frame()
	if !tt.HasText("Stopped") || !tt.HasText("Use Local server") {
		t.Fatalf("the stopped local row reads wrong: %v", tt.Texts())
	}
	if err := tt.Click("Server options, Local server"); err == nil {
		t.Fatal("the stopped local row kept a menu")
	}
	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatalf("the active row lost its menu: %v", err)
	}
	if menu := tt.Menu(); len(menu) < 2 || menu[0] != "Reconnect" {
		t.Fatalf("the active row's menu offers %v", menu)
	}
	tt.CloseMenu()
}

// TestServerMenuSwitches picks a server out of the sidebar's server menu,
// the same road Settings' Use action takes.
func TestServerMenuSwitches(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Server menu, Local server"); err != nil {
		t.Fatalf("the server pill did not open: %v", err)
	}
	menu := tt.Menu()
	if len(menu) < 3 || menu[0] != "Local server" || menu[1] != "Build box" || !slices.Contains(menu, "Server settings…") {
		t.Fatalf("the server menu offers %v", menu)
	}
	if err := tt.ChooseMenuItem("Local server"); err == nil {
		t.Fatal("the active server stayed choosable")
	}
	if err := tt.ChooseMenuItem("Build box"); err != nil {
		t.Fatal(err)
	}
	if a.connections.activeServer != "srv-box" {
		t.Fatal("the menu did not switch servers")
	}
	select {
	case job := <-commandsOf(a):
		if job.kind != "server" || job.server.id != "srv-box" {
			t.Fatalf("switch job %+v", job)
		}
	default:
		t.Fatal("the switch queued no dial")
	}
}

// TestServerEditorAddsRemote drives the add dialog: a nameless save stays
// with a note, a real one lands in the list and is probed.
func TestServerEditorAddsRemote(t *testing.T) {
	a := newTestApp(t)
	probed := []string{}
	a.runtime.probe = func(cfg serverConn) { probed = append(probed, cfg.id) }
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Add server"); err != nil {
		t.Fatalf("the Add server action: %v", err)
	}
	if a.layout.serverEditor == nil || !a.layout.serverEditor.open {
		t.Fatal("the editor did not open")
	}
	tt.Frame()
	if !tt.HasText("Add a server") || !tt.HasText("Address") {
		t.Fatalf("the editor is not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverEditor == nil || !strings.Contains(a.layout.serverEditor.note, "name") {
		t.Fatalf("a nameless server saved anyway: %+v", a.layout.serverEditor)
	}
	a.layout.serverEditor.name = "Build box"
	a.layout.serverEditor.url = "not an address"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverEditor == nil || a.layout.serverEditor.note == "" {
		t.Fatal("a bad address saved anyway")
	}
	a.layout.serverEditor.url = "10.0.0.12:4096"
	a.layout.serverEditor.password = "s3cret"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverEditor != nil {
		t.Fatal("the editor stayed open after a save")
	}
	if len(a.connections.servers) != 2 {
		t.Fatalf("servers after the add: %+v", a.connections.servers)
	}
	added := a.connections.servers[1]
	if added.name != "Build box" || added.url != "http://10.0.0.12:4096" || added.password != "s3cret" || added.local {
		t.Fatalf("the added server reads %+v", added)
	}
	if len(probed) != 1 || probed[0] != added.id {
		t.Fatalf("the new server was not probed: %v", probed)
	}
	// The list shows it with the password it was given.
	tt.Frame()
	if !tt.HasText("Build box") || !tt.HasText("Password set") {
		t.Fatalf("the row is not drawn: %v", tt.Texts())
	}
}

// TestServerEditorEditsLocalPassword keeps the local row's editor on its
// own terms: no address to type, a password to set, and a redial because
// the password travels with the server the app starts.
func TestServerEditorEditsLocalPassword(t *testing.T) {
	a := newTestApp(t)
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Edit Local server"); err != nil {
		t.Fatalf("the Edit action: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Edit Local server") || !tt.HasText("Run it with the app") {
		t.Fatalf("the local editor is not drawn: %v", tt.Texts())
	}
	if tt.HasText("Address") {
		t.Fatalf("the managed server asks for an address: %v", tt.Texts())
	}
	a.layout.serverEditor.password = "chosen-key"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.connections.servers[0].password != "chosen-key" || a.connections.servers[0].managed != true || a.connections.servers[0].url != "" {
		t.Fatalf("the local server reads %+v", a.connections.servers[0])
	}
	select {
	case job := <-commandsOf(a):
		if job.kind != "server" || job.server.password != "chosen-key" {
			t.Fatalf("the redial job %+v", job)
		}
	default:
		t.Fatal("editing the active server's password did not queue a redial")
	}

	// Pointing the local row at a server the user runs themselves needs an
	// address, and dials it instead of starting one.
	a.connections.servers[0].password = ""
	if err := tt.Click("Edit Local server"); err != nil {
		t.Fatal(err)
	}
	a.layout.serverEditor.name = "Local server"
	a.layout.serverEditor.managed = false
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverEditor == nil || a.layout.serverEditor.note == "" {
		t.Fatal("an addressless self-run server saved")
	}
	a.layout.serverEditor.url = "http://127.0.0.1:4096"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.connections.servers[0].managed || a.connections.servers[0].url != "http://127.0.0.1:4096" {
		t.Fatalf("the self-run local server reads %+v", a.connections.servers[0])
	}
}

// TestDeleteServerConfirms keeps the local row and the active one out of
// harm's way, and takes a menu Delete through its dialog.
func TestDeleteServerConfirms(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers,
		serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"},
		serverConn{id: "srv-lab", name: "Lab", url: "http://10.0.0.13:4096", password: "k"},
	)
	a.scopeFor("srv-box").setPins([]string{"ses_x"})
	a.connections.modelOff[modelKey("srv-box", "opencode-go", "beam-free")] = true
	a.setHealth("srv-box", serverHealth{status: healthReady, detail: "0.5.5"})
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	// The local row offers no Delete at all: reconnecting is all it has.
	if err := tt.Click("Server options, Local server"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); len(menu) != 1 || menu[0] != "Reconnect" {
		t.Fatalf("the local server's menu offers %v", menu)
	}
	tt.CloseMenu()

	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete…"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverConfirm == nil || !a.layout.serverConfirm.open {
		t.Fatal("no confirmation was asked for")
	}
	tt.Frame()
	if !tt.HasText("Delete “Build box”?") {
		t.Fatalf("the dialog is not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if len(a.connections.servers) != 2 || a.connections.servers[1].id != "srv-lab" {
		t.Fatalf("servers after the delete: %+v", a.connections.servers)
	}
	if _, ok := a.connections.scopes["srv-box"]; ok {
		t.Fatalf("the deleted server kept its pins: %v", a.connections.scopes)
	}
	if a.connections.modelOff[modelKey("srv-box", "opencode-go", "beam-free")] {
		t.Fatalf("the deleted server kept its model choices: %v", a.connections.modelOff)
	}
	if _, ok := a.connections.serverHealth["srv-box"]; ok {
		t.Fatal("the deleted server kept its health")
	}

	// The active row refuses the delete outright.
	a.connections.activeServer = "srv-lab"
	a.requestDeleteServer(a.connections.servers[1])
	if a.layout.serverConfirm != nil {
		t.Fatal("the active server asked for a delete confirmation")
	}
}

// TestModelsTabToggles drives the Models tab: the counts, the per-model
// switch, and the two bulk actions over the whole catalog.
func TestModelsTabToggles(t *testing.T) {
	a := newTestApp(t)
	a.scope().models = []opencode.Model{
		{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true},
		{ProviderID: "opencode-go", ID: "minimax-m3", Name: "MiniMax-M3", Variants: []string{"low", "high"}},
		{ProviderID: "wada", ID: "flash", Name: "Flash", Free: true},
	}
	a.scope().modelsLoaded = true
	a.layout.settingsOpen = true
	a.layout.settingsTab = tabModels
	tt := ui.NewTester(a.view, 1400, 920)

	if !tt.HasText("3 of 3 on") || !tt.HasText("Beam Free") || !tt.HasText("Flash") {
		t.Fatalf("the catalog is not drawn: %v", tt.Texts())
	}
	// The providers head their groups, alphabetically.
	texts := tt.Texts()
	if at(texts, "opencode-go") > at(texts, "wada") {
		t.Fatalf("providers are out of order: %v", texts)
	}

	// One switch takes one model out of the picker.
	if err := tt.Click("Keep MiniMax-M3 in the composer's picker"); err != nil {
		t.Fatalf("the switch: %v", err)
	}
	if !a.connections.modelOff[modelKey(localServerID, "opencode-go", "minimax-m3")] {
		t.Fatalf("the switch did not take: %v", a.connections.modelOff)
	}
	tt.Frame()
	if !tt.HasText("2 of 3 on") {
		t.Fatalf("the count did not follow: %v", tt.Texts())
	}
	if got := a.filteredModels(); len(got) != 2 {
		t.Fatalf("the picker offers %+v", got)
	}

	// Disable all empties the picker and warns; Enable all refills it.
	if err := tt.Click("Disable all"); err != nil {
		t.Fatal(err)
	}
	if on, total := a.enabledModelCount(); on != 0 || total != 3 {
		t.Fatalf("after Disable all: %d of %d", on, total)
	}
	if len(a.filteredModels()) != 0 {
		t.Fatal("the picker still offers models")
	}
	tt.Frame()
	if !tt.HasText("Every model is off") {
		t.Fatalf("the warning is missing: %v", tt.Texts())
	}
	if err := tt.Click("Enable all"); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.enabledModelCount(); on != 3 {
		t.Fatalf("%d models on after Enable all", on)
	}

	// The search narrows the rows without changing what is on.
	a.layout.modelsQuery = "wada"
	tt.Frame()
	if tt.HasText("Beam Free") || !tt.HasText("Flash") {
		t.Fatalf("the search did not narrow: %v", tt.Texts())
	}
	a.layout.modelsQuery = ""
}

// TestModelsFollowTheServer keeps each server's choices its own: each lists
// its own catalog, and turning a model off on one leaves the other's alone.
func TestModelsFollowTheServer(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	// The local server's dial filled its catalog; the free default is off here.
	a.scope().models = []opencode.Model{{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true}}
	a.scope().modelsLoaded = true
	a.setModelEnabled(a.scope().models[0], false)
	if on, _ := a.enabledModelCount(); on != 0 {
		t.Fatal("the switch did not take on the local server")
	}
	a.connections.activeServer = "srv-box"
	// The build box's scope starts empty and fills on its own dial.
	a.scope().models = []opencode.Model{{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true}}
	a.scope().modelsLoaded = true
	if on, _ := a.enabledModelCount(); on != 1 {
		t.Fatal("the switch leaked across servers")
	}
	if len(a.filteredModels()) != 1 {
		t.Fatal("the picker follows the wrong server's choices")
	}
}

// TestServerCardShowsItsScope lists what each server's dials have kept, so
// a card tells its chats, folders and catalog whatever server is on show.
func TestServerCardShowsItsScope(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.connections.activeServer = "srv-box" // the build box stands alone, no pipe served it yet
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_a"}, {ID: "ses_b"}, {ID: "ses_c"}, {ID: "ses_d"}}
	a.scope().projects = []string{"/srv/app", "/srv/docs", "/srv/labs"}
	a.scope().models = []opencode.Model{
		{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true},
		{ProviderID: "opencode-go", ID: "minimax-m3", Name: "MiniMax-M3"},
	}
	a.scope().modelsLoaded = true
	a.connections.modelOff[modelKey("srv-box", "opencode-go", "minimax-m3")] = true
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("4 chats") || !tt.HasText("3 projects") || !tt.HasText("1 of 2 models on") {
		t.Fatalf("the build box's card does not tell its scope: %v", tt.Texts())
	}
}

// TestComposerPickerHidesDisabledModels drives the picker the settings
// feed: turned-off models stay out, an empty offer says why, and Manage
// models opens the tab behind it.
func TestComposerPickerHidesDisabledModels(t *testing.T) {
	a := newTestApp(t)
	a.scope().models = []opencode.Model{
		{ProviderID: "opencode-go", ID: "beam-free", Name: "Beam Free", Free: true},
		{ProviderID: "opencode-go", ID: "minimax-m3", Name: "MiniMax-M3"},
	}
	a.scope().modelsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_z", Title: "Free turn",
		Model: opencode.ModelRef{ProviderID: "opencode-go", ID: "beam-free"}}}
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Free turn"
	a.setModelEnabled(a.scope().models[1], false)
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Model Beam Free"); err != nil {
		t.Fatalf("opening the picker: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Beam Free") || tt.HasText("MiniMax-M3") {
		t.Fatalf("the picker ignored the settings: %v", tt.Texts())
	}
	if err := tt.Click("Manage models…"); err != nil {
		t.Fatalf("the manage row: %v", err)
	}
	if a.layout.pickerOpen || !a.layout.settingsOpen || a.layout.settingsTab != tabModels {
		t.Fatalf("manage did not land on the Models tab: picker %v settings %v tab %q",
			a.layout.pickerOpen, a.layout.settingsOpen, a.layout.settingsTab)
	}

	// Everything off: the picker says so and still offers the way out.
	a.layout.settingsOpen = false
	a.setAllModelsEnabled(false)
	a.layout.pickerOpen = true
	tt.Frame()
	if !tt.HasText("Every model is off") {
		t.Fatalf("the empty offer is unexplained: %v", tt.Texts())
	}
	if !tt.HasText("Manage models…") {
		t.Fatalf("the way to the settings is gone: %v", tt.Texts())
	}
}

// TestDraftModelChoiceSticks keeps a draft's model pick where the composer
// can read it: the button shows the chosen model, the first words carry it
// into the conversation the server opens, and a fresh chat drops back to
// the default.
func TestDraftModelChoiceSticks(t *testing.T) {
	a := newTestApp(t)
	a.scope().modelsLoaded = true
	a.scope().models = []opencode.Model{
		{ProviderID: "opencode-go", ID: "longcat-2.5-preview-free", Name: "LongCat", Free: true},
		{ProviderID: "opencode", ID: "glm-5.3-flash-free", Name: "GLM", Free: true},
	}
	tt := ui.NewTester(a.view, 1400, 920)

	a.chooseModel(a.scope().models[1], "max")
	if ref := a.sessionModelOf(""); ref.ID != "glm-5.3-flash-free" || ref.Variant != "max" {
		t.Fatalf("the draft reads %+v", ref)
	}
	tt.Frame()
	if !tt.HasText("GLM") || !tt.HasText("max") {
		t.Fatalf("the button does not show the draft's model: %v", tt.Texts())
	}

	a.conversation.draft = "hello"
	a.send()
	select {
	case job := <-commandsOf(a):
		if job.kind != "prompt" || job.provider != "opencode" || job.model != "glm-5.3-flash-free" ||
			job.variant != "max" {
			t.Fatalf("the send carried %+v", job)
		}
	default:
		t.Fatal("the send queued nothing")
	}

	a.startNewChat()
	if ref := a.sessionModelOf(""); ref.ID != "longcat-2.5-preview-free" || ref.Variant != "" {
		t.Fatalf("a fresh draft reads %+v", ref)
	}
}

// TestSendCarriesTheDefault keeps a new turn on the model the settings
// enabled: the job names it, so the runner does not fall back on its own.
func TestSendCarriesTheDefault(t *testing.T) {
	a := newTestApp(t)
	a.scope().models = []opencode.Model{{ProviderID: "wada", ID: "flash", Name: "Flash", Free: true}}
	a.scope().modelsLoaded = true
	a.conversation.draft = "hello"
	a.send()
	select {
	case job := <-commandsOf(a):
		if job.kind != "prompt" || job.provider != "wada" || job.model != "flash" {
			t.Fatalf("send job %+v", job)
		}
	default:
		t.Fatal("the send queued nothing")
	}
}

// TestPairLinkSplits keeps the pairing links the editor accepts honest:
// the address comes back the way the editor stores one, and anything that
// is not a pairing link reads false.
func TestPairLinkSplits(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantAddr string
		wantCode string
		bad      bool
	}{
		{"http://127.0.0.1:49374/auth/connect/abc123", "http://127.0.0.1:49374", "abc123", false},
		{"  http://127.0.0.1:49374/auth/connect/abc123  ", "http://127.0.0.1:49374", "abc123", false},
		{"127.0.0.1:49374/auth/connect/abc-1_2.3~x", "http://127.0.0.1:49374", "abc-1_2.3~x", false},
		{"https://box.local/opencode/auth/connect/k9/", "https://box.local/opencode", "k9", false},
		{"http://10.0.0.12:4096", "", "", true},
		{"http://10.0.0.12:4096/auth/connect/", "", "", true},
		{"http://10.0.0.12:4096/auth/connect/a/b", "", "", true},
		{"", "", "", true},
	} {
		addr, code, ok := pairLink(tc.in)
		if tc.bad {
			if ok {
				t.Fatalf("pairLink(%q) = %q %q, want a miss", tc.in, addr, code)
			}
			continue
		}
		if !ok || addr != tc.wantAddr || code != tc.wantCode {
			t.Fatalf("pairLink(%q) = %q %q %v; want %q %q", tc.in, addr, code, ok, tc.wantAddr, tc.wantCode)
		}
	}
}

// TestRedeemPairLink trades a link for its token over the wire: the code
// rides the path, the request asks for JSON and carries no password, and
// the server's refusal comes back as its own words.
func TestRedeemPairLink(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Errorf("the redeem carried a password")
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("the redeem asked for %q", got)
		}
		seen = r.URL.Path
		switch r.URL.Path {
		case "/auth/connect/good":
			w.Write([]byte(`{"token":"paired-token"}`))
		case "/auth/connect/wrapped":
			w.Write([]byte(`{"data":{"token":"wrapped-token"}}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"_tag":"UnauthorizedError","message":"Pairing link expired or already used"}`))
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	addr, token, err := redeemPairLink(ctx, srv.URL+"/auth/connect/good")
	if err != nil || token != "paired-token" || addr != srv.URL {
		t.Fatalf("redeem = %q %q %v", addr, token, err)
	}
	if seen != "/auth/connect/good" {
		t.Fatalf("the redeem asked %q", seen)
	}
	if _, token, err := redeemPairLink(ctx, srv.URL+"/auth/connect/wrapped"); err != nil || token != "wrapped-token" {
		t.Fatalf("a wrapped answer read %q %v", token, err)
	}
	if _, _, err := redeemPairLink(ctx, srv.URL+"/auth/connect/spent"); err == nil ||
		!strings.Contains(err.Error(), "expired or already used") {
		t.Fatalf("a spent link reads %v", err)
	}
	if _, _, err := redeemPairLink(ctx, "http://10.0.0.12:4096"); err == nil {
		t.Fatal("a plain address redeemed as a link")
	}
	if _, _, err := redeemPairLink(ctx, "http://127.0.0.1:1/auth/connect/x"); err == nil {
		t.Fatal("a dead address redeemed fine")
	}
}

// TestServerEditorPairsALink takes a link through the editor: Save hands
// the link to the pairing, and what lands back — the address and the token
// — is what the connection is saved with.
func TestServerEditorPairsALink(t *testing.T) {
	a := newTestApp(t)
	a.layout.settingsOpen = true
	probed := []string{}
	a.runtime.probe = func(cfg serverConn) { probed = append(probed, cfg.id) }
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Add server"); err != nil {
		t.Fatalf("the Add server action: %v", err)
	}
	a.layout.serverEditor.name = "Build box"
	a.layout.serverEditor.url = "http://10.0.0.12:49374/auth/connect/abc"
	link := ""
	paired := false
	a.runtime.pair = func(got string, done func(addr, token string, err error)) {
		link = got
		paired = true
		done("http://10.0.0.12:49374", "paired-token", nil)
	}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !paired || link != "http://10.0.0.12:49374/auth/connect/abc" {
		t.Fatalf("the save handed the pairing %q (paired %v)", link, paired)
	}
	if a.layout.serverEditor != nil {
		t.Fatalf("the editor stayed open after a paired save: %+v", a.layout.serverEditor)
	}
	if len(a.connections.servers) != 2 {
		t.Fatalf("servers after the paired add: %+v", a.connections.servers)
	}
	added := a.connections.servers[1]
	if added.url != "http://10.0.0.12:49374" || added.password != "paired-token" {
		t.Fatalf("the paired server reads %+v", added)
	}
	if len(probed) != 1 || probed[0] != added.id {
		t.Fatalf("the paired server was not probed: %v", probed)
	}

	// A link the server refuses keeps the dialog on its feet, with the
	// reason in its note and the link still in the field.
	if err := tt.Click("Add server"); err != nil {
		t.Fatal(err)
	}
	a.layout.serverEditor.name = "Stale box"
	a.layout.serverEditor.url = "http://10.0.0.12:49374/auth/connect/spent"
	a.runtime.pair = func(_ string, done func(addr, token string, err error)) {
		done("", "", errors.New("Pairing link expired or already used"))
	}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.layout.serverEditor == nil || !strings.Contains(a.layout.serverEditor.note, "Pairing link expired") {
		t.Fatalf("a refused link reads %+v", a.layout.serverEditor)
	}
	if len(a.connections.servers) != 2 {
		t.Fatalf("a refused link added a server: %+v", a.connections.servers)
	}
}

// TestAPIBaseNormalizes keeps the typed addresses of the editor honest.
func TestAPIBaseNormalizes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		bad  bool
	}{
		{"http://10.0.0.12:4096", "http://10.0.0.12:4096/api", false},
		{"10.0.0.12:4096", "http://10.0.0.12:4096/api", false},
		{"http://10.0.0.12:4096/", "http://10.0.0.12:4096/api", false},
		{"https://box.local/api", "https://box.local/api", false},
		{"  http://127.0.0.1:5000/base/  ", "http://127.0.0.1:5000/base/api", false},
		{"", "", true},
		{"not an address", "", true},
	} {
		got, err := opencode.APIBase(tc.in)
		if tc.bad {
			if err == nil {
				t.Fatalf("apiBase(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("apiBase(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

// TestServerEditorKeepsThePairing walks a paired row through the editor:
// an edit that leaves the token alone keeps its month, a password typed
// over it takes its place, and the row says which credential it holds.
func TestServerEditorKeepsThePairing(t *testing.T) {
	a := newTestApp(t)
	a.layout.settingsOpen = true
	issued := time.Now().Add(-3 * 24 * time.Hour).UnixMilli()
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box",
		url: "http://10.0.0.12:4096", password: "token-1", paired: true, pairedAt: issued})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Paired · signs in for") {
		t.Fatalf("a paired row does not say so: %v", tt.Texts())
	}

	if err := tt.Click("Edit Build box"); err != nil {
		t.Fatal(err)
	}
	a.layout.serverEditor.name = "Build box 2"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	got := a.connections.servers[1]
	if !got.paired || got.pairedAt != issued || got.password != "token-1" {
		t.Fatalf("editing dropped the pairing: %+v", got)
	}

	// A password typed in the token's place is a password again.
	if err := tt.Click("Edit Build box 2"); err != nil {
		t.Fatal(err)
	}
	a.layout.serverEditor.password = "typed-key"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if got := a.connections.servers[1]; got.paired || got.pairedAt != 0 || got.password != "typed-key" {
		t.Fatalf("a typed password kept a pairing: %+v", got)
	}
	if !tt.HasText("Password set") {
		t.Fatalf("the row does not read as a password: %v", tt.Texts())
	}
}

// TestMintPairLink asks a server for a link the way the settings do: the
// code rides the path on the address the server names for itself, a
// refusal comes back in its own words, and a server that mints nothing is
// told apart.
func TestMintPairLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pw, ok := r.BasicAuth(); !ok || pw != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/pair" || r.Method != "POST" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"code":"abc-123","expires_in":300}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	link, err := mintPairLink(ctx, serverConn{url: srv.URL, password: "key"},
		opencode.Facts{URLs: []string{"https://box.tailnet:4096/"}})
	if err != nil || link != "https://box.tailnet:4096/auth/connect/abc-123" {
		t.Fatalf("mint = %q %v", link, err)
	}
	// A server that names no address of its own is reached where it was
	// dialed, the way the settings row spells it.
	link, err = mintPairLink(ctx, serverConn{url: srv.URL, password: "key"}, opencode.Facts{})
	if err != nil || link != srv.URL+"/auth/connect/abc-123" {
		t.Fatalf("mint = %q %v", link, err)
	}
	if _, err := mintPairLink(ctx, serverConn{url: srv.URL, password: "wrong"}, opencode.Facts{}); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused mint reads %v", err)
	}
	if _, err := mintPairLink(ctx, serverConn{url: srv.URL, password: "wrong", paired: true}, opencode.Facts{}); err == nil ||
		!strings.Contains(err.Error(), "pairing has lapsed") {
		t.Fatalf("a lapsed pairing reads %v", err)
	}
	if _, err := mintPairLink(ctx, serverConn{url: "http://127.0.0.1:1", password: "key"}, opencode.Facts{}); err == nil {
		t.Fatal("a dead address minted a link")
	}
}

// TestCopyPairLink runs the card's action end to end: the menu mints a
// link on the server, the clipboard gets it, and the row shows it while it
// is good for joining.
func TestCopyPairLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"abc-123","expires_in":300}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL, password: "key"})
	a.setHealth("srv-box", serverHealth{status: healthReady, detail: "2.0.32", addr: srv.URL})
	a.setFacts("srv-box", opencode.Facts{URLs: []string{srv.URL}})
	copied := ""
	a.runtime.copy = func(text string) error { copied = text; return nil }
	a.layout.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Copy pairing link"); err != nil {
		t.Fatal(err)
	}
	want := srv.URL + "/auth/connect/abc-123"
	if copied != want || a.activePairLink("srv-box") != want {
		t.Fatalf("copied %q, link %q; want %q", copied, a.activePairLink("srv-box"), want)
	}
	tt.CloseMenu()
	tt.Frame()
	if !tt.HasText("Pairing link (5 min): " + want) {
		t.Fatalf("the row does not show the link: %v", tt.Texts())
	}

	// A link the clock has passed is nothing to show, and a server that
	// answered nothing never got one.
	a.connections.pairLinks["srv-box"] = mintedLink{link: want, at: time.Now().Add(-6 * time.Minute)}
	tt.Frame()
	if tt.HasText("Pairing link") {
		t.Fatal("a lapsed link is still shown")
	}

	// A clipboard that refuses says so in the transcript instead.
	a.runtime.copy = func(string) error { return errors.New("no clipboard") }
	a.copyPairLink(a.connections.servers[1])
	if a.activePairLink("srv-box") != want {
		t.Fatal("the link did not land despite the clipboard's refusal")
	}
	found := false
	for _, b := range a.conversation.transcript.Rows() {
		if strings.Contains(b.Text, "could not reach the clipboard") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal is not in the transcript: %+v", a.conversation.transcript.Rows())
	}
}

// TestProbeServer checks the connection test the settings run: the facts
// out of /info, and the credential's refusal told apart from a dead
// address — a pairing token's lapse reading as its own thing.
func TestProbeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pw, ok := r.BasicAuth()
		if !ok || pw == "wrong" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if pw == "fresh-token" {
			w.Write([]byte(`{"version":"0.5.5","pid":42,"urls":["http://box.local:4096"],` +
				`"capabilities":{"persistentPty":true}}`))
			return
		}
		w.Write([]byte(`{"version":"0.5.5"}`))
	}))
	defer srv.Close()

	facts, err := probeServer(context.Background(), serverConn{url: srv.URL, password: "right-key"})
	if err != nil || facts.Version != "0.5.5" {
		t.Fatalf("probe %+v %v", facts, err)
	}
	facts, err = probeServer(context.Background(), serverConn{url: srv.URL, password: "fresh-token", paired: true})
	if err != nil || facts.Version != "0.5.5" || facts.PID != 42 || len(facts.URLs) != 1 || !facts.PTY {
		t.Fatalf("probe %+v %v", facts, err)
	}
	if _, err := probeServer(context.Background(), serverConn{url: srv.URL, password: "wrong"}); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused password reads %v", err)
	}
	if _, err := probeServer(context.Background(), serverConn{url: srv.URL, password: "wrong", paired: true}); err == nil ||
		!strings.Contains(err.Error(), "pairing has lapsed") {
		t.Fatalf("a lapsed pairing reads %v", err)
	}
	if _, err := probeServer(context.Background(), serverConn{url: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("a dead address probed fine")
	}
}

// TestPairedServerRow words what the settings show a paired connection:
// the token's remaining month, its lapse once the month is up, and the
// address the server names for itself when it is not the stored one.
func TestPairedServerRow(t *testing.T) {
	now := time.Now()
	s := serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096",
		password: "tok", paired: true, pairedAt: now.Add(-10 * 24 * time.Hour).UnixMilli()}
	if line := serverPasswordLine(s); line != "Paired · signs in for 19 more days" &&
		line != "Paired · signs in for 20 more days" {
		t.Fatalf("a paired row reads %q", line)
	}
	s.pairedAt = now.Add(-31 * 24 * time.Hour).UnixMilli()
	if line := serverPasswordLine(s); !strings.Contains(line, "lapsed") {
		t.Fatalf("a lapsed pairing reads %q", line)
	}
	if line := serverPasswordLine(serverConn{password: "k"}); line != "Password set" {
		t.Fatalf("an ordinary password reads %q", line)
	}

	// The server's own address shows only when the app does not already
	// name it; its own dialed address keeps the row quiet.
	facts := opencode.Facts{URLs: []string{"http://10.0.0.12:4096/", "http://box.tailnet:4096"}}
	if got := serverFactsLine(facts, s, serverHealth{addr: "http://10.0.0.12:4096"}); got != "Reachable at http://box.tailnet:4096" {
		t.Fatalf("the reachable line reads %q", got)
	}
	if got := serverFactsLine(opencode.Facts{URLs: []string{"http://10.0.0.12:4096"}}, s, serverHealth{addr: "http://10.0.0.12:4096"}); got != "" {
		t.Fatalf("an already-named address still reads %q", got)
	}

	// The refusal's wording depends on what the credential is.
	if got := opencode.RefusedLine(true); !strings.Contains(got, "pairing has lapsed") {
		t.Fatalf("a paired refusal reads %q", got)
	}
	if got := opencode.RefusedLine(false); !strings.Contains(got, "refused") {
		t.Fatalf("a password refusal reads %q", got)
	}
}

// TestBootOnAStoredRemote keeps a restart honest: the app boots onto the
// server it was last using, with its own list and pins, and the local
// server it left behind reads as stopped.
func TestBootOnAStoredRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.preferences.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096", password: "k"})
	a.scopeFor("srv-box").setPins([]string{"ses_box"})
	a.connections.activeServer = "srv-box"
	a.saveState()
	a.closeMetadata()

	b := newTestApp(t)
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if b.connections.activeServer != "srv-box" || b.activeServerConn().id != "srv-box" {
		t.Fatalf("the app would dial %+v", b.activeServerConn())
	}
	if len(b.connections.servers) != 2 || b.connections.servers[1].name != "Build box" || b.connections.servers[1].password != "k" {
		t.Fatalf("servers %+v", b.connections.servers)
	}
	if len(b.scope().pinOrder) != 1 || b.scope().pinOrder[0] != "ses_box" || !b.scope().pinned["ses_box"] {
		t.Fatalf("pins %+v %v", b.scope().pinOrder, b.scope().pinned)
	}
	if h := b.connections.serverHealth[localServerID]; h.status != healthStopped {
		t.Fatalf("the idle local server reads %+v", h)
	}
}

// at is the first index of a text in the frame's list, or -1.
func at(texts []string, s string) int {
	for i, text := range texts {
		if text == s {
			return i
		}
	}
	return -1
}
